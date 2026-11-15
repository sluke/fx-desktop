// Package fxprofile lists agent profiles fx can see. It reads profile.json
// and a short prompt preview. It does not write profiles or pass --profile
// to ask or acp; that flag is only for an interactive fx.
package fxprofile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	maxProfileBytes = 64 << 10
	maxPromptBytes  = 4 << 10
)

// Profile is one readable agent profile.
type Profile struct {
	Name, Source, Path, Description, Permission, Model, Effort, PromptMode string
	Tools                                                                  []string
	Ignored                                                                bool
	RepoPermissionIgnored                                                  bool
	Problem, PromptPreview                                                 string
}

// Scan returns built-in profiles, then profiles under the fx state root and
// the workspace. A folder named main is listed as ignored because fx ignores
// it. The same name can appear from more than one source.
func Scan(stateRoot, workspace string) []Profile {
	out := []Profile{
		{Name: "main", Source: "built-in", Description: "fx itself, with every tool"},
		{Name: "agent", Source: "built-in", Description: "A copy of the agent that starts it"},
	}
	if stateRoot != "" {
		out = append(out, readDir(filepath.Join(stateRoot, "agents"), "user", false)...)
	}
	if workspace != "" {
		out = append(out, readDir(filepath.Join(workspace, ".fx", "agents"), "workspace", true)...)
	}
	return out
}

func readDir(dir, source string, workspace bool) []Profile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Profile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		profile := Profile{Name: name, Source: source, Path: filepath.Join(dir, name)}
		if !validName(name) {
			profile.Problem = "profile name is not one fx will load"
		}
		if name == "main" {
			profile.Ignored = true
			profile.Problem = "fx ignores a main folder"
		}
		readProfile(&profile, workspace)
		out = append(out, profile)
	}
	return out
}

func readProfile(profile *Profile, workspace bool) {
	path := filepath.Join(profile.Path, "profile.json")
	if workspace {
		if problem := workspaceFile(profile.Path, path); problem != "" {
			profile.Problem = problem
			return
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if profile.Problem == "" {
			profile.Problem = "profile.json is missing"
		}
		return
	}
	if len(data) > maxProfileBytes {
		profile.Problem = "profile.json is larger than fx accepts"
		return
	}
	var doc struct {
		Description    string   `json:"description"`
		Tools          []string `json:"tools"`
		PermissionMode string   `json:"permission_mode"`
		Model          string   `json:"model"`
		Effort         string   `json:"effort"`
		Prompt         string   `json:"prompt"`
	}
	if json.Unmarshal(data, &doc) != nil {
		profile.Problem = "profile.json is not readable"
		return
	}
	profile.Description = strings.TrimSpace(doc.Description)
	profile.Tools = doc.Tools
	profile.Model = strings.TrimSpace(doc.Model)
	profile.Effort = strings.TrimSpace(doc.Effort)
	profile.PromptMode = strings.TrimSpace(doc.Prompt)
	if profile.Description == "" && profile.Problem == "" {
		profile.Problem = "a profile without a description does not load"
	}
	if doc.PermissionMode != "" {
		if workspace {
			profile.RepoPermissionIgnored = true
		} else if doc.PermissionMode == "ask" || doc.PermissionMode == "auto" || doc.PermissionMode == "full-access" {
			profile.Permission = doc.PermissionMode
		} else if profile.Problem == "" {
			profile.Problem = "permission mode is not one fx accepts"
		}
	}
	promptPath := filepath.Join(profile.Path, "prompt.md")
	if workspace {
		if problem := workspaceFile(profile.Path, promptPath); problem != "" && fileExists(promptPath) {
			profile.Problem = problem
			return
		}
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return
	}
	text := string(prompt)
	if len(text) > maxPromptBytes {
		text = text[:maxPromptBytes]
	}
	profile.PromptPreview = strings.TrimSpace(text)
}

func workspaceFile(dir, path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return ""
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "a workspace profile file must stay inside the repository"
	}
	if !info.Mode().IsRegular() {
		return "a workspace profile file must be a regular file"
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "a workspace profile file must stay inside the repository"
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
