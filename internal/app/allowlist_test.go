package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
