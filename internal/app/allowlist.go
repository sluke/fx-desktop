package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
)

// fx writes persistent allow rules in two places in ~/.fx/settings.json:
//
//	permission                 user scope, shared by every workspace
//	workspaces[path].permission local scope, shadows the user rules
//
// Each category is a map of pattern to "allow". The TUI's /allowlist writes
// exactly this shape. Editing it here keeps the operation native; the file is
// read and rewritten through the same settings.lock fx itself uses, and the
// replacement is atomic.
var allowlistToolNames = map[string]string{
	"read":          "Read files",
	"write":         "Write files",
	"edit":          "Edit files",
	"glob":          "Glob files",
	"grep":          "Grep files",
	"skill":         "Skills",
	"install_skill": "Install skills",
	"subagent":      "Subagents",
}

func fxSettingsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fx", "settings.json"), nil
}

func settingsLockPath(path string) string {
	return strings.TrimSuffix(path, filepath.Ext(path)) + ".lock"
}

func allowlistWorkspaceKey(workspace string) string {
	if workspace == "" {
		return ""
	}
	path, err := filepath.Abs(workspace)
	if err != nil {
		path = filepath.Clean(workspace)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func readSettingsRoot(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fx settings: %w", err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse fx settings: %w", err)
	}
	if root == nil {
		root = make(map[string]json.RawMessage)
	}
	return root, nil
}

func decodeSettingsObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return make(map[string]json.RawMessage), nil
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if value == nil {
		value = make(map[string]json.RawMessage)
	}
	return value, nil
}

func decodePermission(raw json.RawMessage) (map[string]map[string]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return make(map[string]map[string]string), nil
	}
	var value map[string]map[string]string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	if value == nil {
		value = make(map[string]map[string]string)
	}
	return value, nil
}

func encodeSettingsValue(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func localWorkspaceKey(workspaces map[string]json.RawMessage, workspace string) string {
	key := allowlistWorkspaceKey(workspace)
	if _, ok := workspaces[key]; ok {
		return key
	}
	for existing := range workspaces {
		if filepath.Clean(existing) == key {
			return existing
		}
	}
	return key
}

func allowlistCategory(kind, tool string) (string, error) {
	switch kind {
	case "tool":
		category := strings.TrimSpace(tool)
		if category == "" {
			return "", fmt.Errorf("Choose a tool")
		}
		if _, ok := allowlistToolNames[category]; !ok {
			return "", fmt.Errorf("Unsupported tool category")
		}
		return category, nil
	case "command":
		return "bash", nil
	case "url":
		return "url", nil
	case "domain":
		return "web_fetch", nil
	}
	return "", fmt.Errorf("Choose a rule kind")
}

// allowlistTarget maps the UI kind (and tool category) to the storage category
// and key. Patterns are the exact strings fx stores.
func allowlistTarget(kind, tool, pattern string) (category, key string, err error) {
	category, err = allowlistCategory(kind, tool)
	if err != nil {
		return "", "", err
	}
	if kind == "tool" {
		return category, "*", nil
	}
	key = strings.TrimSpace(pattern)
	if key == "" {
		return "", "", fmt.Errorf("Enter a pattern")
	}
	if strings.ContainsRune(key, 0) {
		return "", "", fmt.Errorf("The pattern cannot contain NUL")
	}
	if kind == "domain" {
		key = strings.TrimPrefix(key, "domain:")
		if key == "" || strings.ContainsAny(key, " \t\r\n") {
			return "", "", fmt.Errorf("Enter a domain such as example.com")
		}
		key = "domain:" + key
	}
	return category, key, nil
}

func mutatePermission(root map[string]json.RawMessage, workspace, scope string, create bool, fn func(map[string]map[string]string)) error {
	switch scope {
	case "user":
		raw, exists := root["permission"]
		if !exists && !create {
			return nil
		}
		perm, err := decodePermission(raw)
		if err != nil {
			return fmt.Errorf("parse user permission rules: %w", err)
		}
		fn(perm)
		if len(perm) == 0 {
			delete(root, "permission")
		} else {
			root["permission"] = encodeSettingsValue(perm)
		}
		return nil
	case "local":
		rawWorkspaces, exists := root["workspaces"]
		if !exists && !create {
			return nil
		}
		workspaces, err := decodeSettingsObject(rawWorkspaces)
		if err != nil {
			return fmt.Errorf("parse fx workspaces: %w", err)
		}
		key := localWorkspaceKey(workspaces, workspace)
		rawWS, wsExists := workspaces[key]
		if !wsExists && !create {
			return nil
		}
		ws, err := decodeSettingsObject(rawWS)
		if err != nil {
			return fmt.Errorf("parse workspace settings: %w", err)
		}
		perm, err := decodePermission(ws["permission"])
		if err != nil {
			return fmt.Errorf("parse workspace permission rules: %w", err)
		}
		fn(perm)
		if len(perm) == 0 {
			delete(ws, "permission")
		} else {
			ws["permission"] = encodeSettingsValue(perm)
		}
		if len(ws) == 0 {
			delete(workspaces, key)
		} else {
			workspaces[key] = encodeSettingsValue(ws)
		}
		if len(workspaces) == 0 {
			delete(root, "workspaces")
		} else {
			root["workspaces"] = encodeSettingsValue(workspaces)
		}
		return nil
	}
	return fmt.Errorf("Choose local or user scope")
}

func writeSettingsRoot(path string, root map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("create settings temporary file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace fx settings: %w", err)
	}
	return nil
}

// allowlistApplyAt is the pure settings mutation used by the native Rules
// page and its tests. It returns an error rather than touching the file if the
// rule is malformed.
func allowlistApplyAt(path, workspace, action, scope, kind, tool, pattern string) error {
	var category, key string
	var err error
	switch action {
	case "reset-all":
		// Reset scope does not need a kind or pattern.
	case "reset":
		if category, err = allowlistCategory(kind, tool); err != nil {
			return err
		}
	default:
		if category, key, err = allowlistTarget(kind, tool, pattern); err != nil {
			return err
		}
	}
	if scope != "local" && scope != "user" {
		return fmt.Errorf("Choose local or user scope")
	}
	if scope == "local" && workspace == "" {
		return fmt.Errorf("This workspace has no directory")
	}
	unlock, err := lockSettingsFile(settingsLockPath(path))
	if err != nil {
		return err
	}
	defer unlock()
	root, err := readSettingsRoot(path)
	if err != nil {
		return err
	}
	switch action {
	case "add":
		err = mutatePermission(root, workspace, scope, true, func(perm map[string]map[string]string) {
			if perm[category] == nil {
				perm[category] = make(map[string]string)
			}
			perm[category][key] = "allow"
		})
	case "remove":
		err = mutatePermission(root, workspace, scope, false, func(perm map[string]map[string]string) {
			delete(perm[category], key)
			if len(perm[category]) == 0 {
				delete(perm, category)
			}
		})
	case "reset":
		err = mutatePermission(root, workspace, scope, false, func(perm map[string]map[string]string) {
			delete(perm, category)
		})
	case "reset-all":
		err = mutatePermission(root, workspace, scope, false, func(perm map[string]map[string]string) {
			for category := range perm {
				delete(perm, category)
			}
		})
	default:
		return fmt.Errorf("Unsupported allowlist action")
	}
	if err != nil {
		return err
	}
	return writeSettingsRoot(path, root)
}

func allowlistRulesAt(path, workspace string) ([]chrome.AllowlistRule, error) {
	root, err := readSettingsRoot(path)
	if err != nil {
		return nil, err
	}
	var rules []chrome.AllowlistRule
	if raw, ok := root["permission"]; ok {
		perm, err := decodePermission(raw)
		if err != nil {
			return nil, fmt.Errorf("parse user permission rules: %w", err)
		}
		rules = appendAllowlistRules(rules, "user", perm)
	}
	if rawWorkspaces, ok := root["workspaces"]; ok {
		workspaces, err := decodeSettingsObject(rawWorkspaces)
		if err != nil {
			return nil, fmt.Errorf("parse fx workspaces: %w", err)
		}
		if raw, ok := workspaces[localWorkspaceKey(workspaces, workspace)]; ok {
			ws, err := decodeSettingsObject(raw)
			if err != nil {
				return nil, fmt.Errorf("parse workspace settings: %w", err)
			}
			perm, err := decodePermission(ws["permission"])
			if err != nil {
				return nil, fmt.Errorf("parse workspace permission rules: %w", err)
			}
			rules = appendAllowlistRules(rules, "local", perm)
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		a, b := rules[i], rules[j]
		if a.Scope != b.Scope {
			return a.Scope < b.Scope
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.Pattern < b.Pattern
	})
	return rules, nil
}

func appendAllowlistRules(out []chrome.AllowlistRule, scope string, perm map[string]map[string]string) []chrome.AllowlistRule {
	categories := make([]string, 0, len(perm))
	for category := range perm {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		keys := make([]string, 0, len(perm[category]))
		for key := range perm[category] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		kind, label := allowlistCategoryLabel(category)
		for _, key := range keys {
			pattern := key
			switch kind {
			case "domain":
				pattern = strings.TrimPrefix(key, "domain:")
			case "tool":
				pattern = key
			}
			out = append(out, chrome.AllowlistRule{
				Scope: scope, Kind: kind, Key: key, Label: label,
				Pattern: pattern, Decision: perm[category][key],
			})
		}
	}
	return out
}

func allowlistCategoryLabel(category string) (kind, label string) {
	switch category {
	case "bash":
		return "command", "Command"
	case "url":
		return "url", "URL"
	case "web_fetch":
		return "domain", "Web fetch domain"
	default:
		name, ok := allowlistToolNames[category]
		if !ok {
			name = category
		}
		return "tool", "Tool · " + name
	}
}

func (c *Coordinator) runAllowlist(s *Session, action, scope, kind, key, pattern string) {
	state := &s.shell.Tools
	if state.Busy {
		return
	}
	path, err := fxSettingsPath()
	if err != nil {
		state.AllowProblem = err.Error()
		s.invalidate()
		return
	}
	if action == "remove" || action == "reset" || action == "reset-all" {
		message := "Remove this persistent allow rule?"
		if action == "reset" {
			message = "Reset every persistent rule of this kind in the selected scope?"
		}
		if action == "reset-all" {
			if scope == "user" {
				message = "Reset every persistent user allow rule? This affects all workspaces."
			} else {
				message = "Reset every persistent allow rule for this workspace? User rules and other workspaces are not touched."
			}
		}
		result, dialogErr := mygo.Dialog.Message(mygo.MessageOptions{
			Parent: s.winSnapshot(), Type: mygo.MessageWarning, Title: "Change fx allow rules",
			Message: message, Buttons: []string{"Cancel", "Continue"}, DefaultButton: 0, CancelButton: 0,
		})
		if dialogErr != nil || result.Button != 1 {
			return
		}
	}
	state.Generation++
	generation := state.Generation
	state.Busy = true
	state.AllowProblem = ""
	s.invalidate()
	workspace := s.project
	go func() {
		var applyErr error
		if action == "load" {
			// no mutation
		} else {
			applyErr = allowlistApplyAt(path, workspace, action, scope, kind, key, pattern)
		}
		rules, loadErr := allowlistRulesAt(path, workspace)
		mygo.App.WhenReady(func() {
			if s.shell.Tools.Generation != generation || !s.shell.Tools.Open {
				return
			}
			state := &s.shell.Tools
			state.Busy = false
			state.AllowRules = rules
			if applyErr != nil {
				state.AllowProblem = applyErr.Error()
			} else if loadErr != nil {
				state.AllowProblem = loadErr.Error()
			}
			s.invalidate()
			if applyErr == nil && action != "load" {
				go s.refreshFxInfo()
			}
		})
	}()
}
