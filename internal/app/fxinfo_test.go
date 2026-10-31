package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/fxbin"
	"fx-desktop/internal/testutil"
)

// Captured from fx v0.0.13 on macOS; the GUI must keep reading these keys.
const statusJSON = `{"kind":"status","model":"zenmux/deepseek/deepseek-v4.1-flash","model_origin":"settings","model_source":"magpie","provider_endpoint":"http://127.0.0.1:3425/v1","update_channel":"stable","build_channel":"stable","build_revision":"4d966e272cfc","auth":"configured provider","connected_providers":["magpie","Codex"],"auth_refreshable":false,"permission_mode":"auto","workspace":"/private/tmp","history_turns":0,"session_permission_grants":0,"agent_step_limit":0,"ultrafast_requested":false,"mcp":{"connection_check":"not_checked","servers":[],"configuration_issues":[],"inspection_error":null}}`

const sessionsJSON = `{"kind":"sessions","count":1,"sessions":[{"id":"ndjQRoFTN_l4","title":"Reply with the single word: ok","preview":null,"workspace_root":"/private/tmp/_fxs","origin_workspace_root":"/private/tmp/_fxs","created_at_ms":1792069556476,"updated_at_ms":1792069560542,"history_len":1,"conversation_language":"und-Latn"}]}`

const usageJSON = `{"kind":"usage","schema_version":1,"period":"30d","snapshot_time_ms":1792069501540,"window_start_ms":1789477501540,"coverage":{"status":"partial","started_at_ms":1791015582374,"full_window":false},"completeness":"incomplete","totals":{"total_tokens":128400,"input_tokens":90000,"output_tokens":38400,"cache_read_tokens":12,"cache_write_tokens":3,"reasoning_tokens":null,"request_count":7,"spend":1.8421},"models":[]}`

const permissionsJSON = `{"kind":"permissions","mode":"auto","grant_count":0,"grant_scope":"session","runtime_grants_available":false,"rules_scope":"persistent_config","rules":[],"grants":[]}`

func TestFxStatusParses(t *testing.T) {
	var s FxStatus
	if err := json.Unmarshal([]byte(statusJSON), &s); err != nil {
		t.Fatal(err)
	}
	if s.Model != "zenmux/deepseek/deepseek-v4.1-flash" {
		t.Fatalf("model: %q", s.Model)
	}
	if s.PermissionMode != "auto" {
		t.Fatalf("permission_mode: %q", s.PermissionMode)
	}
	if len(s.ConnectedProviders) != 2 {
		t.Fatalf("connected_providers: %v", s.ConnectedProviders)
	}
	if s.MCP.ConnectionCheck != "not_checked" {
		t.Fatalf("mcp: %+v", s.MCP)
	}
}

// Captured from fx 0.0.14 with an empty HOME: the signed-out shape. auth_help
// is fx's own sentence about what to do, which is why the GUI shows it
// verbatim rather than writing a sentence of its own (§12.1).
const statusNoAuthJSON = `{"kind":"status","model":"spacexai/grok-4.7","model_origin":"default","effort":"auto","update_channel":"stable","build_channel":"stable","build_revision":"1a070af32cdf","auth":"missing","auth_refreshable":false,"auth_help":"fx needs access to Vercel AI Gateway. Run fx login to sign in, fx setup to use an API key, or set AI_GATEWAY_API_KEY.","permission_mode":"auto","mode":"auto","workspace":"/private/tmp","history_turns":0,"session_permission_grants":0,"agent_step_limit":0,"ultrafast_requested":false,"mcp":{"connection_check":"not_checked","servers":[],"configuration_issues":[],"inspection_error":null}}`

func TestFxStatusParsesSignedOut(t *testing.T) {
	var s FxStatus
	if err := json.Unmarshal([]byte(statusNoAuthJSON), &s); err != nil {
		t.Fatal(err)
	}
	if s.Auth != "missing" {
		t.Fatalf("auth: %q", s.Auth)
	}
	if !strings.Contains(s.AuthHelp, "fx login") {
		t.Fatalf("auth_help must carry the way out: %q", s.AuthHelp)
	}
	if len(s.ConnectedProviders) != 0 {
		t.Fatalf("signed out with providers: %v", s.ConnectedProviders)
	}
}

// readFxAuth copies fx's own words across. It reads them from `fx status
// --json`, not by recognising a value, so the GUI never has to know what fx's
// auth states are called.
func TestReadFxAuthCopiesFxOwnWords(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fx")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat <<'JSON'\n"+statusNoAuthJSON+"\nJSON\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	auth := readFxAuth(script, dir)
	if auth.Auth != "missing" || !strings.Contains(auth.Help, "fx login") || len(auth.Providers) != 0 {
		t.Fatalf("auth: %+v", auth)
	}

	// A CLI that cannot answer reports nothing. Turning that into "signed
	// out" would be a claim about the user's credentials that the GUI has
	// no way to make.
	if got := readFxAuth(filepath.Join(dir, "missing-fx"), dir); got.Auth != "" || got.Help != "" {
		t.Fatalf("a broken CLI must not read as signed out: %+v", got)
	}
}

func TestFxUsageParses(t *testing.T) {
	var u FxUsage
	if err := json.Unmarshal([]byte(usageJSON), &u); err != nil {
		t.Fatal(err)
	}
	if u.Period != "30d" || u.Completeness != "incomplete" {
		t.Fatalf("period/completeness: %+v", u)
	}
	if u.Totals.TotalTokens != 128400 || u.Totals.RequestCount != 7 {
		t.Fatalf("totals: %+v", u.Totals)
	}
	if u.Totals.Spend < 1.84 || u.Totals.Spend > 1.85 {
		t.Fatalf("spend: %v", u.Totals.Spend)
	}
}

func TestFxSessionsParse(t *testing.T) {
	var s struct {
		Count    int         `json:"count"`
		Sessions []FxSession `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(sessionsJSON), &s); err != nil {
		t.Fatal(err)
	}
	if s.Count != 1 || len(s.Sessions) != 1 {
		t.Fatalf("count=%d sessions=%v", s.Count, s.Sessions)
	}
	got := s.Sessions[0]
	if got.ID != "ndjQRoFTN_l4" || got.Title != "Reply with the single word: ok" {
		t.Fatalf("session: %+v", got)
	}
	if got.HistoryLen != 1 || got.WorkspaceRoot != "/private/tmp/_fxs" {
		t.Fatalf("session: %+v", got)
	}
	if got.UpdatedAtMS != 1792069560542 {
		t.Fatalf("updated_at_ms: %d", got.UpdatedAtMS)
	}
}

func TestFxPermissionsParse(t *testing.T) {
	var p FxPermissions
	if err := json.Unmarshal([]byte(permissionsJSON), &p); err != nil {
		t.Fatal(err)
	}
	if p.Mode != "auto" || p.GrantCount != 0 {
		t.Fatalf("permissions: %+v", p)
	}
}

func TestCompactCount(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0"}, {42, "42"}, {999, "999"},
		{1284, "1.3k"}, {128400, "128.4k"}, {1_800_000, "1.8M"}, {-5, "0"},
	} {
		if got := compactCount(tc.in); got != tc.want {
			t.Fatalf("compactCount(%d) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestRelTime(t *testing.T) {
	now := time.UnixMilli(1792069560542)
	for _, tc := range []struct {
		ms   int64
		want string
	}{
		{0, ""},
		{now.UnixMilli(), "just now"},
		{now.Add(-5 * time.Minute).UnixMilli(), "5m ago"},
		{now.Add(-3 * time.Hour).UnixMilli(), "3h ago"},
		{now.Add(-2 * 24 * time.Hour).UnixMilli(), "2d ago"},
	} {
		if got := relTime(tc.ms, now); got != tc.want {
			t.Fatalf("relTime(%d) = %q want %q", tc.ms, got, tc.want)
		}
	}
}

func TestFxJSONWithoutPath(t *testing.T) {
	var s FxStatus
	if err := fxJSON(context.Background(), "", t.TempDir(), &s, "status"); err == nil {
		t.Fatal("expected an error without an fx path")
	}
}

// isolateWithFx keeps the real fx binary reachable after HOME isolation.
func isolateWithFx(t *testing.T) string {
	t.Helper()
	path, err := fxbin.ResolvePath()
	if err != nil {
		t.Skip("fx is not installed")
	}
	testutil.IsolateHome(t)
	dir := filepath.Dir(path)
	if p, err := exec.LookPath("git"); err == nil {
		dir += string(os.PathListSeparator) + filepath.Dir(p)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestFxUsageAndModelsFromBinary(t *testing.T) {
	fxPath := isolateWithFx(t)
	dir := t.TempDir()
	u, err := fxUsage(context.Background(), fxPath, dir)
	if err != nil {
		// fx 0.0.14 queries gateway account usage; unlike the v0.0.13
		// local-record API it requires sign-in. HOME is deliberately empty
		// here. Only this explicit unavailable state is an environment skip;
		// malformed JSON and other command failures must still fail.
		ctx, cancel := context.WithTimeout(context.Background(), fxTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, fxPath, "usage", "--json")
		cmd.Dir = dir
		out, _ := cmd.Output()
		var state struct {
			Kind   string `json:"kind"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(out, &state) == nil && state.Kind == "usage" &&
			state.Status == "unavailable" && state.Reason == "no_gateway_sign_in" {
			t.Skip("installed fx requires gateway sign-in; integration HOME is isolated")
		}
		t.Fatalf("fx usage: %v", err)
	}
	if u.Period == "" {
		t.Fatalf("expected a period: %+v", u)
	}
	if u.Totals.TotalTokens < 0 || u.Totals.Spend < 0 {
		t.Fatalf("negative totals: %+v", u.Totals)
	}
}

func TestFxStatusFromBinary(t *testing.T) {
	// The isolated HOME makes this the signed-out case on a machine that is
	// signed in. A gateway key in the environment would answer for fx, so it
	// is cleared for the duration: the assertion is about fx's reporting,
	// not about the developer's credentials.
	t.Setenv("AI_GATEWAY_API_KEY", "")
	fxPath := isolateWithFx(t)
	dir := t.TempDir()
	st, err := fxStatus(context.Background(), fxPath, dir)
	if err != nil {
		t.Fatalf("fx status: %v", err)
	}
	if st.Model == "" {
		t.Fatal("expected a configured model")
	}
	if st.PermissionMode == "" {
		t.Fatal("expected a permission mode")
	}
	// fx always describes the credential in use, and when it is missing it
	// also says what to run. The GUI leans on that second field, so its
	// absence on the missing state would be a silent loss of guidance.
	if st.Auth == "" {
		t.Fatal("expected fx to describe the auth state")
	}
	if st.Auth == "missing" && st.AuthHelp == "" {
		t.Fatal("a missing credential must come with fx's own guidance")
	}
	// readFxAuth is the path the GUI actually uses; it must carry fx's
	// answer across, guidance included.
	if auth := readFxAuth(fxPath, dir); auth.Auth != st.Auth || auth.Help != st.AuthHelp {
		t.Fatalf("readFxAuth %+v does not match fx status %q/%q", auth, st.Auth, st.AuthHelp)
	}
	if perms, err := fxPermissions(context.Background(), fxPath, dir); err != nil {
		t.Fatalf("fx permissions: %v", err)
	} else if perms.Mode == "" {
		t.Fatal("expected a permission mode from fx permissions")
	}
	if sessions, err := fxSessions(context.Background(), fxPath, dir); err != nil {
		t.Fatalf("fx sessions: %v", err)
	} else if sessions == nil {
		t.Fatal("nil sessions slice should not happen")
	}
}
