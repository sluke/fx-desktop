package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
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
		{"setup", chrome.ManagementState{}, []string{"setup"}, false},
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
	if want := []string{"--model", "m", "--ultrafast", "--log-file", "/tmp/acp.jsonl"}; !reflect.DeepEqual(c.args, want) {
		t.Fatalf("acp args=%v, want %v", c.args, want)
	}
	if want := []string{"--no-additional-dirs"}; !reflect.DeepEqual(c.globalArgs, want) {
		t.Fatalf("global args=%v, want %v", c.globalArgs, want)
	}
	if c.initialProvider != "gateway" || c.initialEffort != "xhigh" || c.initialFast == nil || !*c.initialFast || !c.ultra {
		t.Fatalf("advanced launch state=%q,%q,%v,%v", c.initialProvider, c.initialEffort, c.initialFast, c.ultra)
	}
	if strings.Contains(strings.Join(c.args, " "), "--provider") {
		t.Fatalf("provider leaked into acp args: %v", c.args)
	}
}

func TestManagementReadsBoundOutputAndPreserveFailures(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fx")
	for _, tc := range []struct {
		script string
		want   string
		failed bool
	}{
		{"#!/bin/sh\nprintf '%s' '{\"count\":0,\"sessions\":[]}'\n", "\"sessions\": []", false},
		{"#!/bin/sh\necho 'usage unavailable' >&2\nexit 2\n", "usage unavailable", true},
		{"#!/bin/sh\nprintf '%070000d' 0\n", "Output truncated at 64 KiB", false},
	} {
		if err := os.WriteFile(file, []byte(tc.script), 0700); err != nil {
			t.Fatal(err)
		}
		text, err := managementRead(context.Background(), file, dir, "sessions", "--all", "--json")
		if (err != nil) != tc.failed || !strings.Contains(text, tc.want) || len(text) > 65600 {
			t.Fatalf("bounded read len=%d error=%v output=%q", len(text), err, text[:min(100, len(text))])
		}
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
	s := &Session{coord: &Coordinator{fxPath: path}, project: t.TempDir(), chat: previous}
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
