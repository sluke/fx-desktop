package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
