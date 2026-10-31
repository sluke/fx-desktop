package chrome

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestWorkspaceToolsPagesAndDispatch(t *testing.T) {
	for _, tc := range []struct{ page, button, action string }{{"Account", "Sign in", "login"}, {"MCP", "List servers", "mcp-list"}, {"Folders", "List directories", "workspace-list"}, {"Details", "Refresh details", "details"}, {"Sessions", "Browse history…", "sessions"}, {"Launch", "Start new conversation", "launch"}} {
		t.Run(tc.page, func(t *testing.T) {
			h, sh := testHost()
			h.Actions.ManageWorkspace = noop
			sh.Tools = ManagementState{Open: true, Page: tc.page, Transport: "stdio", Period: "month", Ultra: "default"}
			var got string
			h.Management = ManagementHost{Run: func(action string) { got = action }}
			ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
			panel, ok := ts.Find("Workspace tools")
			if !ok || panel.X < 0 || panel.Y < 0 || panel.X+panel.W > 720 || panel.Y+panel.H > 560 {
				t.Fatalf("tools panel outside window: %+v", panel)
			}
			if err := ts.Click(tc.button); err != nil {
				t.Fatal(err)
			}
			if got != tc.action {
				t.Fatalf("dispatch=%s, want %s", got, tc.action)
			}
			sh.Tools.Busy = true
			ts.Frame()
			got = ""
			_ = ts.Click(tc.button)
			if got != "" {
				t.Fatal("busy tools dispatched twice")
			}
		})
	}
}

func TestWorkspaceToolsHideWithoutProjectAndResumeOwningWorkspace(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustNotHave(t, ts, "Workspace tools…")
	h.Actions.ManageWorkspace = noop
	ts.Frame()
	mustHave(t, ts, "Workspace tools…")
	sh.PaletteOpen = false
	h.Actions.OpenHistory = noop
	sh.History = HistoryState{Open: true, Loaded: true, Exhausted: true, Entries: []HistorySession{{ID: "saved", Title: "Saved", Workspace: "/other/workspace"}}}
	var id, path string
	h.History.Resume = func(i, p string) { id, path = i, p }
	ts.Frame()
	if err := ts.Click("Resume history saved"); err != nil {
		t.Fatal(err)
	}
	if id != "saved" || path != "/other/workspace" {
		t.Fatalf("resume=(%s,%s)", id, path)
	}
}

func TestWorkspaceToolsNewPagesRenderAndDispatch(t *testing.T) {
	h, sh := testHost()
	h.Actions.ManageWorkspace = noop
	var got string
	var allowAction, allowScope, allowKind, allowKey, allowPattern string
	var opened string
	h.Management = ManagementHost{
		Run:      func(action string) { got = action },
		OpenPath: func(path string) { opened = path },
		Allowlist: func(action, scope, kind, key, pattern string) {
			allowAction, allowScope, allowKind, allowKey, allowPattern = action, scope, kind, key, pattern
		},
	}
	sh.Tools = ManagementState{
		Open: true, Page: "Models", Project: "/p",
		Models:     []ModelInfo{{ID: "provider/model", Source: "gateway"}},
		Skills:     []SkillInfo{{Name: "alpha", Description: "Alpha skill", Source: "workspace .fx/skills", Path: "/p/.fx/skills/alpha"}},
		AllowScope: "local", AllowKind: "command", AllowPattern: "git status",
		AllowRules: []AllowlistRule{{Scope: "local", Kind: "command", Key: "git status", Label: "Command", Pattern: "git status"}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
	ts.Frame()
	if err := ts.Click("Use model provider/model"); err != nil {
		t.Fatal(err)
	}
	if sh.Tools.Model != "provider/model" || sh.Tools.Page != "Launch" {
		t.Fatalf("model selection=%q page=%q", sh.Tools.Model, sh.Tools.Page)
	}

	sh.Tools.Page = "MCP"
	ts.Frame()
	if err := ts.Click("Check server"); err != nil {
		t.Fatal(err)
	}
	if got != "mcp-show" {
		t.Fatalf("mcp check dispatch=%q", got)
	}
	if err := ts.Click("Approve all project servers"); err != nil {
		t.Fatal(err)
	}
	if got != "mcp-approve-all" {
		t.Fatalf("mcp approve-all dispatch=%q", got)
	}
	mustNotHave(t, ts, "Reset trust")

	sh.Tools.Page = "Rules"
	ts.Frame()
	if err := ts.Click("Add rule"); err != nil {
		t.Fatal(err)
	}
	if allowAction != "add" || allowScope != "local" || allowKind != "command" || allowPattern != "git status" {
		t.Fatalf("allow add=%q,%q,%q,%q", allowAction, allowScope, allowKind, allowPattern)
	}
	if err := ts.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	if allowAction != "remove" || allowKey != "git status" {
		t.Fatalf("allow remove=%q key=%q", allowAction, allowKey)
	}
	if err := ts.Click("Reset scope"); err != nil {
		t.Fatal(err)
	}
	if allowAction != "reset-all" {
		t.Fatalf("allow reset=%q", allowAction)
	}

	sh.Tools.Page = "Skills"
	ts.Frame()
	if err := ts.Click("Refresh skills"); err != nil {
		t.Fatal(err)
	}
	if got != "skills-load" {
		t.Fatalf("skills dispatch=%q", got)
	}
	if err := ts.Click("Open"); err != nil {
		t.Fatal(err)
	}
	if opened != "/p/.fx/skills/alpha" {
		t.Fatalf("opened=%q", opened)
	}

	sh.Tools.Page = "Slack"
	ts.Frame()
	if err := ts.Click("Refresh status"); err != nil {
		t.Fatal(err)
	}
	if got != "slack-status" {
		t.Fatalf("slack dispatch=%q", got)
	}

	sh.Tools.Page = "GitHub"
	ts.Frame()
	if err := ts.Click("Run GitHub action"); err != nil {
		t.Fatal(err)
	}
	if got != "github-run" {
		t.Fatalf("github dispatch=%q", got)
	}

	sh.Tools.Page = "Details"
	ts.Frame()
	if err := ts.Click("Run doctor"); err != nil {
		t.Fatal(err)
	}
	if got != "doctor" {
		t.Fatalf("details dispatch=%q", got)
	}
}

func TestWorkspaceToolsRenderNativeMCPAndSessionViews(t *testing.T) {
	h, sh := testHost()
	h.Actions.ManageWorkspace = noop
	var got string
	h.Management = ManagementHost{Run: func(action string) { got = action }}
	sh.Tools = ManagementState{
		Open: true, Page: "MCP", Transport: "http",
		MCPServers: []MCPServerInfo{{Name: "local", Source: "global", File: "/h/.fx/mcp.json", Transport: "stdio", Status: "connected"}},
		MCPDetail: &MCPServerDetail{
			MCPServerInfo: MCPServerInfo{Name: "local", Transport: "stdio", Status: "connected"},
			Version:       "1.2", Command: "server", Tools: []MCPToolInfo{{Name: "read", Description: "Read a file"}},
		},
	}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 560)
	ts.Frame()
	mustHave(t, ts, "Configured servers")
	mustHave(t, ts, "Checked server")
	mustHave(t, ts, "Read a file")
	// The server list sits below the sheet's scroll viewport; scroll to it
	// before clicking, as a person would.
	ts.Wheel(300, 400, 0, 800)
	ts.Advance(2 * time.Second)
	ts.Frame()
	if err := ts.Click("Check local"); err != nil {
		t.Fatal(err)
	}
	if got != "mcp-show" || sh.Tools.Name != "local" {
		t.Fatalf("row check dispatch=%q name=%q", got, sh.Tools.Name)
	}

	sh.Tools.Page = "Sessions"
	sh.Tools.SessionView = &SessionDetail{
		ID: "thread-1", Created: "2026-10-31 10:00 CST", Language: "en",
		HistoryLen: 2,
		Updated:    "2026-10-31 10:01 CST",
		Turns:      []SessionTurn{{Kind: "user", User: "hi", Images: 1}, {Kind: "assistant", Assistant: "hello", Tools: []string{"read"}}},
	}
	ts.Frame()
	mustHave(t, ts, "Session detail")
	mustHave(t, ts, "You: hi")
	mustHave(t, ts, "Tools: read")
	if err := ts.Click("Inspect session"); err != nil {
		t.Fatal(err)
	}
	if got != "session-inspect" {
		t.Fatalf("inspect dispatch=%q", got)
	}
}
