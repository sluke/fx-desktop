package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/fxcli"
	"fx-desktop/internal/fxlive"
)

func TestManagementCommandsAreExplicitArgv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a b")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		action  string
		state   chrome.ManagementState
		want    []string
		confirm bool
	}{
		{"login", chrome.ManagementState{Provider: "codex"}, []string{"login", "codex"}, false},
		{"logout", chrome.ManagementState{Provider: "grok"}, []string{"logout", "grok"}, true},
		{"setup", chrome.ManagementState{}, []string{"login", "vercel", "--api-key"}, false},
		{"teams", chrome.ManagementState{}, []string{"teams"}, false},
		{"mcp-add", chrome.ManagementState{Name: "local", Transport: "stdio", Command: "/a path/server", Arguments: "--name\na b\n$(touch nope)"}, []string{"mcp", "add", "local", "--", "/a path/server", "--name", "a b", "$(touch nope)"}, false},
		{"mcp-add", chrome.ManagementState{Name: "local", Transport: "stdio", Command: "server", MCPProject: true, Environment: "TOKEN=a=b\nEMPTY="}, []string{"mcp", "add", "--project", "--env", "TOKEN=a=b", "--env", "EMPTY=", "local", "--", "server"}, false},
		{"mcp-add", chrome.ManagementState{Name: "remote", Transport: "http", URL: "https://example.test/mcp"}, []string{"mcp", "add", "remote", "https://example.test/mcp"}, false},
		{"mcp-add", chrome.ManagementState{Name: "remote", Transport: "http", URL: "https://example.test/mcp", MCPProject: true, Headers: "Authorization=Bearer x\nX-Trace=1"}, []string{"mcp", "add", "--project", "--header", "Authorization=Bearer x", "--header", "X-Trace=1", "remote", "https://example.test/mcp"}, false},
		{"mcp-remove", chrome.ManagementState{Name: "local"}, []string{"mcp", "remove", "local"}, true},
		{"mcp-approve", chrome.ManagementState{Name: "local"}, []string{"mcp", "approve", "local"}, true},
		{"mcp-reject", chrome.ManagementState{Name: "local"}, []string{"mcp", "reject", "local"}, true},
		{"mcp-login", chrome.ManagementState{Name: "local"}, []string{"mcp", "login", "local"}, false},
		{"mcp-logout", chrome.ManagementState{Name: "local"}, []string{"mcp", "logout", "local"}, true},
		{"workspace-add", chrome.ManagementState{Directory: dir}, []string{"workspace", "add", dir}, false},
		{"workspace-remove", chrome.ManagementState{Directory: dir}, []string{"workspace", "remove", dir}, true},
		{"session-recover", chrome.ManagementState{SessionID: "thread-123"}, []string{"session", "recover", "--id", "thread-123"}, true},
		{"launch", chrome.ManagementState{Model: "provider/model", Ultra: "on", LogPath: "/tmp/acp log.jsonl"}, []string{"--model", "provider/model", "--ultrafast", "--log-file", "/tmp/acp log.jsonl"}, true},
	} {
		t.Run(tc.action, func(t *testing.T) {
			got, confirm, err := managementCommand(tc.action, tc.state)
			if err != nil || !reflect.DeepEqual(got, tc.want) || confirm != tc.confirm {
				t.Fatalf("got %v,%v,%v; want %v,%v", got, confirm, err, tc.want, tc.confirm)
			}
		})
	}
}

func TestManagementCommandsCoverTheNoninteractiveFxSurface(t *testing.T) {
	for _, tc := range []struct {
		action  string
		state   chrome.ManagementState
		want    []string
		confirm bool
	}{
		{"mcp-list", chrome.ManagementState{}, []string{"mcp", "list", "--json"}, false},
		{"mcp-show", chrome.ManagementState{Name: "local"}, []string{"mcp", "show", "local", "--json"}, false},
		{"mcp-add-slack", chrome.ManagementState{}, []string{"mcp", "add", "slack"}, true},
		{"mcp-add-slack", chrome.ManagementState{MCPNoLogin: true}, []string{"mcp", "add", "--no-login", "slack"}, true},
		{"config-list", chrome.ManagementState{}, []string{"config", "list", "--show-origin", "--json"}, false},
		{"config-set", chrome.ManagementState{ConfigKey: "fast_mode", ConfigValue: "true"}, []string{"config", "set", "fast_mode", "true"}, true},
		{"sessions-live", chrome.ManagementState{}, []string{"sessions", "--live", "--json"}, false},
		{"sessions-live", chrome.ManagementState{LiveAll: true}, []string{"sessions", "--live", "--all", "--json"}, false},
		{"session-tail", chrome.ManagementState{SessionID: "abc/agent", Tail: "5"}, []string{"session", "abc/agent", "--json", "--tail", "5"}, false},
		{"mcp-approve-all", chrome.ManagementState{}, []string{"mcp", "approve", "--all"}, true},
		{"provider-set", chrome.ManagementState{ProviderDefault: "gateway"}, []string{"provider", "gateway"}, true},
		{"upgrade-channel", chrome.ManagementState{Channel: "dev"}, []string{"upgrade", "--channel", "dev"}, true},
		{"doctor", chrome.ManagementState{}, []string{"doctor", "--json"}, false},
		{"credits", chrome.ManagementState{}, []string{"credits", "--json"}, false},
		{"slack-status", chrome.ManagementState{}, []string{"slack", "status", "--json"}, false},
		{"slack-install", chrome.ManagementState{}, []string{"slack", "install", "--json"}, true},
		{"slack-refresh", chrome.ManagementState{}, []string{"slack", "refresh", "--json"}, true},
		{"sessions-convert", chrome.ManagementState{}, []string{"sessions", "convert", "--json"}, true},
		{"launch", chrome.ManagementState{Model: "m", Effort: "high", Fast: "off", Ultra: "on", NoAdditionalDirs: true}, []string{"--model", "m", "--effort", "high", "--no-fast", "--ultrafast", "--no-additional-dirs"}, true},
	} {
		t.Run(tc.action, func(t *testing.T) {
			got, confirm, err := managementCommand(tc.action, tc.state)
			if err != nil || !reflect.DeepEqual(got, tc.want) || confirm != tc.confirm {
				t.Fatalf("got %v,%v,%v; want %v,%v", got, confirm, err, tc.want, tc.confirm)
			}
		})
	}
}

func TestGitHubCommandKeepsContextAsOneArgAndConfirmsPublishing(t *testing.T) {
	for _, tc := range []struct {
		state   chrome.GitHubState
		want    []string
		confirm bool
	}{
		{chrome.GitHubState{Kind: "pr", Auto: true, Context: "review this"}, []string{"pr", "--auto", "review this"}, false},
		{chrome.GitHubState{Kind: "issue", Create: true, Context: "-not-a-flag"}, []string{"issue", "--create", "--", "-not-a-flag"}, true},
	} {
		got, confirm, err := githubCommand(tc.state)
		if err != nil || !reflect.DeepEqual(got, tc.want) || confirm != tc.confirm {
			t.Fatalf("got %v,%v,%v; want %v,%v", got, confirm, err, tc.want, tc.confirm)
		}
	}
	if _, _, err := githubCommand(chrome.GitHubState{Kind: "commit"}); err == nil {
		t.Fatal("accepted an unsupported GitHub action")
	}
}

func TestChatAdvancedLaunchArgumentsSplitGlobalsFromAcpFlags(t *testing.T) {
	c := newChatSession("/stub", "/project", []string{
		"--sessions-v2", "--no-additional-dirs", "--provider", "gateway", "--model", "m", "--effort", "xhigh", "--fast", "--ultrafast", "--log-file", "/tmp/acp.jsonl",
	}, nil)
	defer c.Close()
	if want := []string{"--provider", "gateway", "--model", "m", "--effort", "xhigh", "--fast", "--ultrafast", "--log-file", "/tmp/acp.jsonl"}; !reflect.DeepEqual(c.args, want) {
		t.Fatalf("acp args=%v, want %v", c.args, want)
	}
	if want := []string{"--no-additional-dirs"}; !reflect.DeepEqual(c.globalArgs, want) {
		t.Fatalf("global args=%v, want %v", c.globalArgs, want)
	}
	if c.initialProvider != "gateway" || c.initialEffort != "xhigh" || c.initialFast == nil || !*c.initialFast || !c.ultra {
		t.Fatalf("advanced launch state=%q,%q,%v,%v", c.initialProvider, c.initialEffort, c.initialFast, c.ultra)
	}
	if !c.providerArgv || !c.effortArgv || !c.fastArgv {
		t.Fatalf("startup flags were not recorded as argv: provider=%v effort=%v fast=%v", c.providerArgv, c.effortArgv, c.fastArgv)
	}
}

func TestManagementReadsBoundOutputAndKeepStreamsSeparate(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	for _, tc := range []struct {
		name    string
		script  string
		wantOut string
		wantErr string
	}{
		{"json", "#!/bin/sh\nprintf '%s' '{\"count\":0,\"sessions\":[]}'\n", "\"sessions\": []", ""},
		{"stderr-only", "#!/bin/sh\necho 'usage unavailable' >&2\nexit 2\n", "", "usage unavailable"},
		{"truncated", "#!/bin/sh\nprintf '%070000d' 0\n", "", "exceeded 64 KiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(file, []byte(tc.script), 0700); err != nil {
				t.Fatal(err)
			}
			text, err := managementRead(context.Background(), file, dir, "sessions", "--all", "--json")
			if !strings.Contains(text, tc.wantOut) || len(text) > 65600 {
				t.Fatalf("bounded read len=%d output=%q", len(text), text[:min(100, len(text))])
			}
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error=%v, want %q", err, tc.wantErr)
			}
			if tc.name == "stderr-only" && strings.Contains(text, "usage unavailable") {
				t.Fatalf("stderr was copied into stdout: %q", text)
			}
		})
	}
}

// A truncated stdout is not a smaller JSON document: the caller gets the
// bounded prefix and an explicit cap error, and no marker is appended to the
// payload (A4).
func TestManagementStreamsReportTruncationAndIdentity(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	script := "#!/bin/sh\nprintf 'answer partial'\nprintf 'diagnostic' >&2\n"
	if err := os.WriteFile(file, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	streams := managementStreams(context.Background(), file, dir, 64*1024, "status", "--json")
	if streams.stdout != "answer partial" || streams.stderr != "diagnostic" || streams.err != nil {
		t.Fatalf("streams=%+v", streams)
	}
	if streams.stdoutTruncated || streams.stderrTruncated {
		t.Fatalf("small output reported truncated: %+v", streams)
	}

	big := "#!/bin/sh\nprintf '%040000d' 0 >&2\n"
	if err := os.WriteFile(file, []byte(big), 0700); err != nil {
		t.Fatal(err)
	}
	streams = managementStreams(context.Background(), file, dir, 64, "status", "--json")
	if !streams.stderrTruncated || len(streams.stderr) != 32*1024 {
		t.Fatalf("stderr truncation not observable: truncated=%v len=%d", streams.stderrTruncated, len(streams.stderr))
	}
}

// A long Ask publishes bounded stderr progress instead of waiting for exit.
func TestManagementStreamsPublishProgress(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	script := "#!/bin/sh\necho 'contacting model' >&2\necho 'done' >&2\nprintf '{}'\n"
	if err := os.WriteFile(file, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	var lines []string
	streams := managementStreamsProgress(context.Background(), file, dir, 64*1024, func(line string) {
		lines = append(lines, line)
	}, "ask", "--json", "hi")
	if streams.err != nil || streams.stdout != "{}" {
		t.Fatalf("streams=%+v", streams)
	}
	joined := strings.Join(lines, "\n")
	if len(lines) == 0 || strings.TrimSpace(joined) == "" {
		t.Fatalf("no progress published: %v", lines)
	}
}

func TestDoctorExitOneKeepsChecks(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	script := "#!/bin/sh\necho 'noise' >&2\nprintf '%s' '{\"ok_count\":1,\"warn_count\":0,\"fail_count\":1,\"checks\":[{\"name\":\"auth\",\"status\":\"fail\"}]}'\nexit 1\n"
	if err := os.WriteFile(file, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	text, err := managementRead(context.Background(), file, dir, "doctor", "--json")
	doctor, ok := decodeDoctor(text, err)
	if !ok || doctor.FailCount != 1 || len(doctor.Checks) != 1 || strings.Contains(text, "noise") {
		t.Fatalf("doctor text=%q err=%v ok=%v report=%+v", text, err, ok, doctor)
	}
}

// JSON that decodes is not a doctor report. Exit 2 is a usage error, and an
// error object without checks is not a report even on exit 1 (A4).
func TestDoctorUsageAndChecklessJSONAreNotReports(t *testing.T) {
	usage := exec.Command("/bin/sh", "-c", "exit 2").Run()
	if _, ok := decodeDoctor(`{"error":"unknown flag"}`, usage); ok {
		t.Fatal("decodeDoctor accepted a usage error as an empty report")
	}
	exitOne := exec.Command("/bin/sh", "-c", "exit 1").Run()
	if _, ok := decodeDoctor(`{"error":"unknown flag"}`, exitOne); ok {
		t.Fatal("decodeDoctor accepted a checkless object on exit 1")
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"error\":\"unknown flag\"}'\nexit 2\n"
	if err := os.WriteFile(file, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var report FxDoctor
	if err := fxcli.Run(context.Background(), file, dir, &report, "doctor"); err == nil {
		t.Fatal("fxcli.Run hid a doctor usage error")
	}
}

func TestAskAuthFailureObjectKeepsPartialAnswer(t *testing.T) {
	raw := `{"output":"partial answer","final_output":"","exit_code":1,"auth_failure":{"source":"fx login","reason":"expired","http_status":401},"recovery":{"state":"need_user","need":"reconnect","attempt":1}}`
	var result fxAskResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("documented auth_failure object rejected the result: %v", err)
	}
	output, meta, problem := askPresentation(result, true, raw, "", nil)
	if output != "partial answer" {
		t.Fatalf("partial answer lost: %q", output)
	}
	if !strings.Contains(meta, "partial output") {
		t.Fatalf("partial output not marked: %q", meta)
	}
	for _, want := range []string{"fx login", "expired", "HTTP 401"} {
		if !strings.Contains(problem, want) {
			t.Fatalf("problem=%q missing %q", problem, want)
		}
	}
}

// A string where the object belongs is protocol drift: say so, and still show
// the answer that arrived. It must not be printed as the assistant answer.
func TestAskAuthFailureTypeDriftIsAProtocolError(t *testing.T) {
	raw := `{"output":"partial answer","auth_failure":"expired"}`
	var result fxAskResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("type drift discarded the whole result: %v", err)
	}
	output, _, problem := askPresentation(result, true, raw, "", nil)
	if output != "partial answer" {
		t.Fatalf("partial answer lost: %q", output)
	}
	if !strings.Contains(problem, "auth_failure is not an object") {
		t.Fatalf("problem=%q", problem)
	}
}

func TestAskRecoveryNeedIsShown(t *testing.T) {
	result := fxAskResult{Recovery: &fxAskRecovery{State: "need_user", Need: "reconnect the provider"}}
	_, _, problem := askPresentation(result, true, "", "", nil)
	if !strings.Contains(problem, "reconnect the provider") {
		t.Fatalf("recovery.need not shown: %q", problem)
	}
}

func TestManagementRejectsInvalidFormsAndUnsupportedOperations(t *testing.T) {
	for _, tc := range []struct {
		action string
		state  chrome.ManagementState
	}{
		{"logout", chrome.ManagementState{Provider: "--help"}},
		{"mcp-remove", chrome.ManagementState{Name: "--all"}},
		{"mcp-add", chrome.ManagementState{Name: "remote", Transport: "http", URL: "file:///tmp/x"}},
		{"mcp-add", chrome.ManagementState{Name: "remote", Transport: "http", URL: "https://example.test/mcp", Headers: "missing-equals"}},
		{"mcp-add", chrome.ManagementState{Name: "local", Transport: "stdio", Command: "server", Environment: "=empty-key"}},
		{"workspace-add", chrome.ManagementState{Directory: "relative"}},
		{"launch", chrome.ManagementState{LogPath: "relative.log"}},
		{"launch", chrome.ManagementState{Fast: "sometimes"}},
		{"launch", chrome.ManagementState{Ultra: "maybe"}},
		{"upgrade-channel", chrome.ManagementState{Channel: "nightly"}},
		{"provider-set", chrome.ManagementState{}},
		{"config-set", chrome.ManagementState{ConfigKey: "permission", ConfigValue: "allow"}},
		{"session-tail", chrome.ManagementState{SessionID: "../keep", Tail: "1"}},
		{"mcp-reload", chrome.ManagementState{}}, {"tui", chrome.ManagementState{}},
	} {
		if _, _, err := managementCommand(tc.action, tc.state); err == nil {
			t.Errorf("accepted invalid action/form: %s %+v", tc.action, tc.state)
		}
	}
}

func TestChatRetainsSupportedAdvancedLaunchArguments(t *testing.T) {
	c := newChatSession("/stub", "/project", []string{"--ultrafast", "--log-file", "/tmp/acp.jsonl", "--model", "m"}, nil)
	defer c.Close()
	want := []string{"--ultrafast", "--log-file", "/tmp/acp.jsonl", "--model", "m"}
	if !reflect.DeepEqual(c.args, want) {
		t.Fatalf("ACP launch args=%v, want %v", c.args, want)
	}
}

func TestAdvancedLaunchKeepsCurrentModelWithoutOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-local-stub")
	previous := newChatSession(path, t.TempDir(), []string{"--model", "current/model"}, nil)
	s := &ProjectWindow{coord: &Coordinator{fxPath: path}, project: t.TempDir(), chat: previous}
	s.replaceConversation([]string{"--ultrafast"})
	defer s.chat.Close()
	if s.chat.model != "current/model" {
		t.Fatalf("advanced launch reset current model to %q", s.chat.model)
	}
}

func TestLaunchGlobalArgsKeepContextAndAdditionalDirectoriesBeforeAcp(t *testing.T) {
	extra := t.TempDir()
	got, _, err := managementCommand("launch", chrome.ManagementState{
		ProviderOverride: "gateway", Model: "m", ContextLimits: "project_instructions_total_bytes=1000\nskill_file_bytes=off",
		AddDirs: extra + "\n", Project: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--context-limit", "project_instructions_total_bytes=1000",
		"--context-limit", "skill_file_bytes=off",
		"--add-dir", extra, "--provider", "gateway", "--model", "m",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launch args=%v, want %v", got, want)
	}
	for _, tc := range []chrome.ManagementState{
		{ContextLimits: "project_instructions_total_bytes=lots"},
		{ContextLimits: "not-a-name=1"},
		{AddDirs: "relative/path"},
	} {
		if _, _, err := managementCommand("launch", tc); err == nil {
			t.Fatalf("accepted invalid global launch args: %+v", tc)
		}
	}
}

func TestAskResultMetaSummarizesMachineFields(t *testing.T) {
	in, out, read, write := int64(1200), int64(34), int64(5400), int64(0)
	result := fxAskResult{
		Model:            "gateway/gpt",
		ResolvedProvider: "gateway",
		SessionID:        "thread-1",
		Steps:            2,
		ToolCalls:        []json.RawMessage{json.RawMessage(`{}`)},
		Usage:            &fxAskUsage{InputTokens: &in, OutputTokens: &out, CacheReadTokens: &read, CacheWriteTokens: &write},
	}
	want := "model gateway/gpt · provider gateway · 2 step(s) · 1 tool call(s) · in 1.2k, out 34, cache read 5.4k, cache write 0 · session thread-1"
	if got := askResultMeta(result); got != want {
		t.Fatalf("meta=%q, want %q", got, want)
	}
	if got := askResultMeta(fxAskResult{Output: "only text"}); got != "" {
		t.Fatalf("plain result produced meta %q", got)
	}
	partial, meta, problem := askPresentation(fxAskResult{
		Output: "draft", Model: "m", AuthFailure: json.RawMessage(`{"source":"fx login","reason":"expired"}`),
		Recovery: &fxAskRecovery{State: "need_user", Message: "sign in"},
	}, true, "", "retrying", nil)
	if partial != "draft" || !strings.Contains(meta, "partial output") || !strings.Contains(problem, "fx login") || !strings.Contains(meta, "retrying") {
		t.Fatalf("ask presentation output=%q meta=%q problem=%q", partial, meta, problem)
	}
	final, _, problem := askPresentation(fxAskResult{Output: "draft", FinalOutput: "done"}, true, "", "", nil)
	if final != "done" || problem != "" {
		t.Fatalf("final presentation output=%q problem=%q", final, problem)
	}
	if got := askResultMeta(fxAskResult{Usage: &fxAskUsage{}}); got != "" {
		t.Fatalf("empty usage produced meta %q", got)
	}
}

func TestSessionDetailViewKeepsKindsAndTrimsLongTurns(t *testing.T) {
	long := strings.Repeat("x", 2500)
	detail := &FxSessionDetail{
		ID:                   "thread-1",
		CreatedAtMS:          1700000000000,
		UpdatedAtMS:          1700000100000,
		HistoryLen:           2,
		ConversationLanguage: "zh-Hans",
		History: []FxSessionTurn{
			{Kind: "user", User: struct {
				Text   string            `json:"text"`
				Images []json.RawMessage `json:"images"`
			}{Text: "hi", Images: []json.RawMessage{json.RawMessage(`{}`)}}},
			{Kind: "assistant", Assistant: long, CompletedToolNames: []string{"read"}},
		},
	}
	view := sessionDetailView(detail)
	if view == nil || view.ID != "thread-1" || view.Language != "zh-Hans" || view.HistoryLen != 2 {
		t.Fatalf("view=%+v", view)
	}
	if view.Created == "" || view.Updated == "" {
		t.Fatalf("timestamps not rendered: %+v", view)
	}
	if len(view.Turns) != 2 || view.Turns[0].Kind != "user" || view.Turns[0].Images != 1 || view.Turns[0].User != "hi" {
		t.Fatalf("turns=%+v", view.Turns)
	}
	if !strings.HasPrefix(view.Turns[1].Assistant, strings.Repeat("x", 2000)) || len(view.Turns[1].Assistant) > 2040 || len(view.Turns[1].Tools) != 1 {
		t.Fatalf("long turn not trimmed: len=%d tools=%v", len(view.Turns[1].Assistant), view.Turns[1].Tools)
	}
	if sessionDetailView(nil) != nil {
		t.Fatal("nil detail produced a view")
	}
}

func TestDetailsSectionsSurfaceNewInspectionFields(t *testing.T) {
	fetched := int64(1700000000000)
	status := &FxStatus{
		ModelOrigin:        "cli flag",
		ProviderEndpoint:   "https://gateway.test/v1",
		ConnectedProviders: []string{"gateway", "anthropic"},
		AuthRefreshable:    true,
		BuildChannel:       "dev",
		BuildRevision:      "abc123",
		Modes:              []FxMode{{ID: "auto"}, {ID: "yolo"}, {ID: ""}},
		MCP: FxMCP{
			Servers:             []json.RawMessage{json.RawMessage(`{}`)},
			ConfigurationIssues: []string{"bad server"},
		},
	}
	permissions := &FxPermissions{
		Mode:                   "auto",
		GrantCount:             2,
		RuntimeGrantsAvailable: true,
		Grants:                 []json.RawMessage{json.RawMessage(`{"tool":"read"}`)},
	}
	usage := &FxUsage{FetchedAtMS: &fetched, Budget: json.RawMessage(`{"limit":1}`)}
	usage.Totals.ReasoningTokens = 700

	sections := detailsSections(status, permissions, usage, nil)
	if got := sectionFact(sections, "Status", "Model origin"); got != "cli flag" {
		t.Fatalf("model origin=%q", got)
	}
	if got := sectionFact(sections, "Status", "Permission modes"); got != "auto, yolo" {
		t.Fatalf("permission modes=%q", got)
	}
	if got := sectionFact(sections, "Status", "Auth refreshable"); got != "yes" {
		t.Fatalf("auth refreshable=%q", got)
	}
	if got := sectionFact(sections, "Status", "Build channel"); got != "dev" {
		t.Fatalf("build channel=%q", got)
	}
	if got := sectionText(sections, "MCP issues"); !strings.Contains(got, "bad server") {
		t.Fatalf("mcp issues=%q", got)
	}
	if got := sectionFact(sections, "Permissions", "Runtime grants available"); got != "yes" {
		t.Fatalf("runtime grants available=%q", got)
	}
	if got := sectionText(sections, "Runtime grants"); !strings.Contains(got, "interactive shell") {
		t.Fatalf("runtime grants note=%q", got)
	}
	if got := sectionFact(sections, "Usage · month", "Reasoning"); got != "700" {
		t.Fatalf("reasoning=%q", got)
	}
	if got := sectionFact(sections, "Usage · month", "Fetched"); got == "" {
		t.Fatal("fetched timestamp not rendered")
	}
	if got := sectionText(sections, "Usage detail"); !strings.Contains(got, "Budget:") {
		t.Fatalf("usage detail=%q", got)
	}
}

func sectionFact(sections []chrome.DetailSection, title, label string) string {
	for _, section := range sections {
		if section.Title != title {
			continue
		}
		for _, fact := range section.Facts {
			if fact.Label == label {
				return fact.Value
			}
		}
	}
	return ""
}

func sectionText(sections []chrome.DetailSection, title string) string {
	for _, section := range sections {
		if section.Title == title {
			return section.Text
		}
	}
	return ""
}

func TestChatKeepsGlobalContextLimitAndAddDir(t *testing.T) {
	c := newChatSession("/stub", "/project", []string{
		"--context-limit", "skill_file_bytes=off", "--add-dir", "/extra", "--model", "m",
	}, nil)
	defer c.Close()
	if want := []string{"--context-limit", "skill_file_bytes=off", "--add-dir", "/extra"}; !reflect.DeepEqual(c.globalArgs, want) {
		t.Fatalf("global args=%v, want %v", c.globalArgs, want)
	}
	if want := []string{"--model", "m"}; !reflect.DeepEqual(c.args, want) {
		t.Fatalf("acp args=%v, want %v", c.args, want)
	}
}

// The live readout must not turn a count fx omitted into a number it never
// sent, and it must say when entries were dropped (L4).
func TestLiveSummaryKeepsUnknownCountAndSkippedEntries(t *testing.T) {
	two := 2
	tests := []struct {
		name   string
		report fxlive.Report
		want   string
	}{
		{
			name:   "count omitted",
			report: fxlive.Report{Agents: []fxlive.Agent{{CLIRef: "a"}, {CLIRef: "b"}}},
			want:   "2 live session(s) listed · count not reported",
		},
		{
			name:   "count zero",
			report: fxlive.Report{Count: new(int)},
			want:   "0 live session(s)",
		},
		{
			name:   "count with skipped entries",
			report: fxlive.Report{Count: &two, Skipped: 2},
			want:   "2 live session(s) · 2 not shown",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := liveSummary(tt.report); got != tt.want {
				t.Fatalf("liveSummary = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLiveAgentsKeepDepthAndOrder(t *testing.T) {
	got := liveAgents([]fxlive.Agent{{
		CLIRef: "a",
		Agents: []fxlive.Agent{{
			CLIRef: "a/reviewer",
			Agents: []fxlive.Agent{{CLIRef: "a/reviewer/helper"}},
		}},
	}})
	if len(got) != 3 {
		t.Fatalf("rows=%+v", got)
	}
	want := []struct {
		ref   string
		depth int
	}{{"a", 0}, {"a/reviewer", 1}, {"a/reviewer/helper", 2}}
	for i, w := range want {
		if got[i].CLIRef != w.ref || got[i].Depth != w.depth {
			t.Fatalf("row %d = %+v, want %s at depth %d", i, got[i], w.ref, w.depth)
		}
	}
}
