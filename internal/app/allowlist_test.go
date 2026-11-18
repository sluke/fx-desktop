package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
)

func TestAllowlistMutatesLocalAndUserRulesWithoutLosingSettings(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	workspace := filepath.Join(dir, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	initial := map[string]any{
		"providers": map[string]any{"magpie": map[string]any{"base_url": "http://127.0.0.1:3425/v1"}},
		"workspaces": map[string]any{
			workspace: map[string]any{
				"permission": map[string]any{"read": map[string]any{"*": "allow"}},
				"other":      "keep me",
			},
			"/other": map[string]any{"permission": map[string]any{"bash": map[string]any{"ls": "allow"}}},
		},
	}
	data, _ := json.MarshalIndent(initial, "", "  ")
	if err := os.WriteFile(settings, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct {
		action, scope, kind, tool, pattern string
	}{
		{"add", "local", "command", "", "git status"},
		{"add", "local", "url", "", "https://example.test/path"},
		{"add", "local", "domain", "", "example.test"},
		{"add", "user", "tool", "read", ""},
		{"remove", "local", "tool", "read", "*"},
	} {
		if err := allowlistApplyAt(settings, workspace, step.action, step.scope, step.kind, step.tool, step.pattern); err != nil {
			t.Fatalf("%+v: %v", step, err)
		}
	}
	rules, err := allowlistRulesAt(settings, workspace)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, rule := range rules {
		seen[rule.Scope+"/"+rule.Kind+"/"+rule.Pattern] = true
	}
	for _, want := range []string{
		"local/command/git status",
		"local/url/https://example.test/path",
		"local/domain/example.test",
		"user/tool/*",
	} {
		if !seen[want] {
			t.Fatalf("missing rule %q in %+v", want, rules)
		}
	}
	if seen["local/tool/*"] {
		t.Fatal("removed local read rule is still present")
	}

	if err := allowlistApplyAt(settings, workspace, "reset-all", "user", "tool", "", ""); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	raw, _ := os.ReadFile(settings)
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := root["permission"]; ok {
		t.Fatal("reset-all left the user permission object behind")
	}
	var workspaces map[string]map[string]json.RawMessage
	if err := json.Unmarshal(root["workspaces"], &workspaces); err != nil {
		t.Fatal(err)
	}
	if string(workspaces[workspace]["other"]) != `"keep me"` {
		t.Fatalf("unknown workspace setting was lost: %s", workspaces[workspace]["other"])
	}
	if _, ok := root["providers"]; !ok {
		t.Fatal("unknown top-level settings were lost")
	}
}

func TestAllowlistMapsFileAndSkillNamesOntoCheckedKeys(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"write_file", "edit"},
		{"edit_file", "edit"},
		{"write", "edit"},
		{"install_skill", "skill"},
		{"read", "read"},
	} {
		got, err := allowlistCategory("tool", tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("%s -> %s, %v; want %s", tc.in, got, err, tc.want)
		}
	}
}

func TestPresentCreditsKeepsZeroAndNamesNull(t *testing.T) {
	got, err := presentCredits(`{"balance":null,"remaining":0}`, nil)
	if err != nil || !strings.Contains(got, "balance: 未提供") || !strings.Contains(got, "remaining: 0") || !strings.Contains(got, "Source: fx credits") {
		t.Fatalf("credits=%q err=%v", got, err)
	}
	pass, err := presentCredits(`{"token_pass":{"remaining":null,"resets_at":"2026-12-01"}}`, nil)
	if err != nil || !strings.Contains(pass, "remaining: 未提供") || strings.Contains(pass, "remaining: 0") {
		t.Fatalf("token pass=%q err=%v", pass, err)
	}
}

func TestFX17SettingsUsesFXHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FX_HOME", root)
	got, err := fxSettingsPath()
	want := filepath.Join(root, "settings.json")
	if err != nil || got != want {
		t.Fatalf("fxSettingsPath() = %q, %v; want %q", got, err, want)
	}
}

func TestFX17SettingsRejectsRelativeFXHome(t *testing.T) {
	t.Setenv("FX_HOME", "relative/fx")
	if _, err := fxSettingsPath(); err == nil {
		t.Fatal("relative FX_HOME was accepted")
	}
}

func TestAllowlistRejectsMalformedRules(t *testing.T) {
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind, tool, pattern string
	}{
		{"tool", "root", ""},
		{"command", "", ""},
		{"url", "", " "},
		{"domain", "", "bad domain"},
	} {
		if err := allowlistApplyAt(settings, t.TempDir(), "add", "local", tc.kind, tc.tool, tc.pattern); err == nil {
			t.Fatalf("accepted malformed rule: %+v", tc)
		}
	}
}

// A row's Remove carries the rule exactly as fx stores it, so it removes that
// rule and nothing that merely resembles it: a longer command with the same
// prefix, the same key in the other scope, and the same key under another
// category all stay (r10 §1).
func TestAllowlistRowRemoveDeletesExactlyTheListedRule(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	workspace := filepath.Join(dir, "workspace")
	for _, step := range []struct{ scope, kind, pattern string }{
		{"local", "command", "git status"},
		{"local", "command", "git status --short"},
		{"user", "command", "git status"},
		{"local", "url", "git status"},
	} {
		if err := allowlistApplyAt(settings, workspace, "add", step.scope, step.kind, "", step.pattern); err != nil {
			t.Fatalf("%+v: %v", step, err)
		}
	}
	rules, err := allowlistRulesAt(settings, workspace)
	if err != nil {
		t.Fatal(err)
	}
	var target *chrome.AllowlistRule
	for i := range rules {
		if r := rules[i]; r.Scope == "local" && r.Kind == "command" && r.Pattern == "git status" {
			target = &rules[i]
		}
	}
	if target == nil {
		t.Fatalf("the rule to remove is not listed: %+v", rules)
	}
	action, scope, category, key, pattern := target.RemoveRequest()
	if action != chrome.AllowlistRemoveRule || category == "" || key != "git status" || pattern != "" {
		t.Fatalf("RemoveRequest = %q,%q,%q,%q,%q", action, scope, category, key, pattern)
	}
	if err := allowlistApplyAt(settings, workspace, action, scope, category, key, pattern); err != nil {
		t.Fatal(err)
	}
	after, err := allowlistRulesAt(settings, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(rules)-1 {
		t.Fatalf("removed %d rules, want 1: %+v", len(rules)-len(after), after)
	}
	left := map[string]bool{}
	for _, r := range after {
		left[r.Scope+"/"+r.Kind+"/"+r.Pattern] = true
	}
	if left["local/command/git status"] {
		t.Fatal("the listed rule is still present")
	}
	for _, want := range []string{"local/command/git status --short", "user/command/git status", "local/url/git status"} {
		if !left[want] {
			t.Fatalf("a neighbouring rule was removed: missing %q in %+v", want, after)
		}
	}
	// A row whose category is unknown cannot be named, and says so instead of
	// guessing one.
	if err := allowlistApplyAt(settings, workspace, chrome.AllowlistRemoveRule, "local", "", "git status --short", ""); err == nil {
		t.Fatal("a rule with no category was accepted")
	}
}

// A settings.json that links to a file that does not exist is the user's
// link, not an empty document: adding a rule reports it and changes nothing,
// instead of replacing the link with a regular file (r10). The neighbouring
// cases keep working: a path with nothing at it is created, a link that
// resolves is written through, the file's own mode survives, and the lock
// stays beside the path fx names.
func TestAllowlistKeepsADanglingSettingsLink(t *testing.T) {
	workspace := t.TempDir()
	add := func(path string) error {
		return allowlistApplyAt(path, workspace, "add", "user", "command", "", "fixture")
	}

	t.Run("dangling link is reported and kept", func(t *testing.T) {
		dir := t.TempDir()
		link := filepath.Join(dir, "settings.json")
		if err := os.Symlink("future.json", link); err != nil {
			t.Skip("symbolic links are not available:", err)
		}
		err := add(link)
		if err == nil || !strings.Contains(err.Error(), "future.json") {
			t.Fatalf("add over a dangling link = %v, want an error naming the target", err)
		}
		if target, lerr := os.Readlink(link); lerr != nil || target != "future.json" {
			t.Fatalf("the link was changed: %q, %v", target, lerr)
		}
		if _, serr := os.Stat(filepath.Join(dir, "future.json")); !os.IsNotExist(serr) {
			t.Fatalf("the missing target was created: %v", serr)
		}
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".settings-") {
				t.Fatalf("a temporary file was left behind: %s", entry.Name())
			}
		}
		// Removing from it is still "nothing to remove", and still touches
		// nothing; listing it is still an empty list.
		if err := allowlistApplyAt(link, workspace, "remove", "user", "command", "", "fixture"); err != nil {
			t.Fatalf("remove on a dangling link = %v", err)
		}
		if rules, err := allowlistRulesAt(link, workspace); err != nil || len(rules) != 0 {
			t.Fatalf("rules on a dangling link = %v, %v", rules, err)
		}
		if target, _ := os.Readlink(link); target != "future.json" {
			t.Fatal("a later call replaced the link")
		}
		// Once the target exists the same request succeeds through the link.
		if err := os.WriteFile(filepath.Join(dir, "future.json"), []byte("{}\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := add(link); err != nil {
			t.Fatal(err)
		}
		if target, _ := os.Readlink(link); target != "future.json" {
			t.Fatal("writing through the link replaced it")
		}
		if st, err := os.Stat(filepath.Join(dir, "future.json")); err != nil || st.Mode().Perm() != 0o640 {
			t.Fatalf("the linked file's mode changed: %v, %v", st, err)
		}
		if rules, err := allowlistRulesAt(link, workspace); err != nil || len(rules) != 1 {
			t.Fatalf("rules through the link = %v, %v", rules, err)
		}
		if _, err := os.Stat(settingsLockPath(link)); err != nil {
			t.Fatalf("the lock is not beside the link: %v", err)
		}
	})

	t.Run("a path with nothing at it is still created", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "settings.json")
		if err := add(path); err != nil {
			t.Fatal(err)
		}
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
			t.Fatalf("new settings file = %v, %v", st, err)
		}
		if rules, err := allowlistRulesAt(path, workspace); err != nil || len(rules) != 1 {
			t.Fatalf("rules = %v, %v", rules, err)
		}
	})
}
