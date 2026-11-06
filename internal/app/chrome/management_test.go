package chrome

import (
	"fmt"
	"math"
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

// The tools sheet has more pages than fit on one row, and the pages have
// very different heights. A single clipped row hides the last pages, and a
// content-sized sheet is centered by DialogBase, so every page switch moves
// the whole dialog up and down. Both are layout regressions: the tests
// measure boxes, not prose.
func TestWorkspaceToolsTabsVisibleAndSheetDoesNotJump(t *testing.T) {
	const windowW, windowH = 720, 560
	host, sh := testHost()
	host.Actions.ManageWorkspace = noop
	sh.Tools = ManagementState{Open: true, Page: "Account"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, host) }, windowW, windowH)
	ts.Frame()

	pages := []string{"Account", "MCP", "Folders", "Details", "Sessions", "Launch", "Models", "Rules", "Skills", "Slack", "GitHub"}
	body, ok := ts.Find("Workspace tool page")
	if !ok {
		t.Fatal("tools page viewport is unnamed, so nothing can measure it")
	}
	first, ok := ts.Find("Tools: Account")
	if !ok {
		t.Fatal("first tab is missing from the frame")
	}
	rows := map[float32]bool{}
	for _, page := range pages {
		tab, ok := ts.Find("Tools: " + page)
		if !ok {
			t.Fatalf("tab %q is missing from the frame", page)
		}
		rows[tab.Y] = true
		// A clipped tab still has a box, but only the visible sliver of
		// it: the last tab was 28 DIPs wide instead of about 50.
		if tab.W < 32 {
			t.Fatalf("tab %q is clipped to %v DIPs wide: %+v", page, tab.W, tab)
		}
		if tab.X < body.X-1 || tab.X+tab.W > body.X+body.W+1 || tab.Y < first.Y-1 || tab.Y+tab.H > body.Y+1 {
			t.Fatalf("tab %q is clipped: %+v outside the sheet body %+v", page, tab, body)
		}
	}
	if len(rows) < 2 {
		t.Fatalf("all %d tabs still share one row; the strip must wrap", len(pages))
	}

	for _, page := range pages {
		if err := ts.Click("Tools: " + page); err != nil {
			t.Fatalf("switch to %q: %v", page, err)
		}
		ts.Frame()
		got, ok := ts.Find("Workspace tool page")
		if !ok {
			t.Fatalf("tools page viewport missing on %q", page)
		}
		tabs, ok := ts.Find("Tools: Account")
		if !ok {
			t.Fatalf("tab strip missing on %q", page)
		}
		if got.Y != body.Y || got.H != body.H || tabs.Y != first.Y {
			t.Fatalf("sheet jumps on %q: body y/h=%v/%v, tabs y=%v; want %v/%v, %v",
				page, got.Y, got.H, tabs.Y, body.Y, body.H, first.Y)
		}
	}

	// A page switch also starts the new page at its top. Both Launch and
	// MCP are taller than the viewport, so the container cannot clamp the
	// old offset away and hide a missing reset.
	if err := ts.Click("Tools: Launch"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	ts.Wheel(120, body.Y+20, 0, 800)
	ts.Advance(2 * time.Second)
	ts.Frame()
	if sh.Tools.BodyScroll.Y == 0 {
		t.Fatal("expected the wheel to scroll the tools body")
	}
	if err := ts.Click("Tools: MCP"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if sh.Tools.BodyScroll.Y != 0 {
		t.Fatalf("page switch kept scroll y=%v, want 0", sh.Tools.BodyScroll.Y)
	}
}

// Each tab row used to split its own width with Grow(1): six pages above and
// five below made the lower row's columns wider, so the tabs did not line up.
// The strip is one column grid, and the shorter row leaves its last cell
// empty rather than stretching five tabs across six columns.
func TestWorkspaceToolsTabsShareColumnGrid(t *testing.T) {
	// Below 608 the sheet itself narrows, so the grid must follow the
	// panel width rather than assume the 560 DIP maximum.
	for _, windowW := range []int{520, 640, 720, 1280} {
		t.Run(fmt.Sprintf("window=%d", windowW), func(t *testing.T) {
			host, sh := testHost()
			host.Actions.ManageWorkspace = noop
			sh.Tools = ManagementState{Open: true, Page: "Account"}
			ts := ui.NewTester(func(c *ui.Context) { Frame(c, host) }, windowW, 560)
			ts.Frame()
			assertToolTabGrid(t, ts)
		})
	}
}

func assertToolTabGrid(t *testing.T, ts *ui.Tester) {
	t.Helper()
	top := []string{"Account", "MCP", "Folders", "Details", "Sessions", "Launch"}
	bottom := []string{"Models", "Rules", "Skills", "Slack", "GitHub"}
	find := func(page string) ui.Rect {
		t.Helper()
		box, ok := ts.Find("Tools: " + page)
		if !ok {
			t.Fatalf("tab %q is missing from the frame", page)
		}
		return box
	}
	same := func(a, b float32) bool { return math.Abs(float64(a-b)) <= 0.5 }

	for i, page := range bottom {
		upper, lower := find(top[i]), find(page)
		if !same(upper.X, lower.X) || !same(upper.W, lower.W) {
			t.Fatalf("column %d is misaligned: %q %+v vs %q %+v", i, top[i], upper, page, lower)
		}
	}
	for _, row := range [][]string{top, bottom} {
		base := find(row[0])
		for _, page := range row[1:] {
			box := find(page)
			if box.Y != base.Y || box.H != base.H || !same(box.W, base.W) {
				t.Fatalf("tab %q does not share its row's geometry: %+v vs %+v", page, box, base)
			}
		}
	}
	if last := find(bottom[len(bottom)-1]); last.X+last.W >= find(top[len(top)-1]).X+find(top[len(top)-1]).W-1 {
		t.Fatalf("the shorter row stretches over the empty trailing column: %+v", last)
	}
}

// The sheet's labels follow the design's structural caption (labelText: 10px
// monospaced, weight 500): hand-rolled 9px labels next to 12px inputs and 11px
// controls made every page read as five unrelated sizes. The tab label is the
// reference 10px line already on screen; a caption that shares its size shares
// its height.
func TestWorkspaceToolsLabelsFollowTheTypeScale(t *testing.T) {
	h, sh := testHost()
	h.Appearance = "light"
	h.Actions.ManageWorkspace = noop
	sh.Tools = ManagementState{Open: true, Page: "Launch"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 800, 1100)
	ts.SetDark(false)
	ts.Frame()

	tab, ok := ts.Find("Launch")
	if !ok {
		t.Fatal("the Launch tab is missing")
	}
	for _, label := range []string{
		"OVERRIDES",         // section caption
		"Provider override", // field caption
		"Reasoning effort",  // inline group caption
		"Fast mode",         // radio group caption
	} {
		got, ok := ts.Find(label)
		if !ok {
			t.Fatalf("caption %q is missing", label)
		}
		if diff := got.H - tab.H; diff > 0.5 || diff < -0.5 {
			t.Fatalf("caption %q is %.2f high, want the 10px caption's %.2f", label, got.H, tab.H)
		}
	}
	if _, ok := ts.Find("Applies to new sessions"); !ok {
		t.Fatal("the deferred-setting badge is missing")
	}
	// Controls keep the 11px control size: button and radio share a height.
	btn, ok := ts.Find("Start new conversation")
	if !ok {
		t.Fatal("the launch button is missing")
	}
	radio, ok := ts.Find("Default")
	if !ok {
		t.Fatal("the fast-mode radio is missing")
	}
	if diff := btn.H - radio.H; diff > 0.5 || diff < -0.5 {
		t.Fatalf("button %.2f and radio %.2f disagree on the control size", btn.H, radio.H)
	}
}

// Row buttons on the list pages are the same 11px control at compact padding,
// not a third, smaller size.
func TestWorkspaceToolsRowButtonsFollowTheTypeScale(t *testing.T) {
	h, sh := testHost()
	h.Appearance = "light"
	h.Actions.ManageWorkspace = noop
	sh.Tools = ManagementState{
		Open:       true,
		Page:       "MCP",
		MCPServers: []MCPServerInfo{{Name: "demo", Transport: "stdio", Status: "ok"}},
	}
	ts := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Children(func() {
			Frame(c, h)
			// The reference is the compact 11px row button the pages use.
			ui.Button(c, "ref-row").FontSize(11).Padding(3, 6)
		})
	}, 800, 1100)
	ts.SetDark(false)
	ts.Frame()

	ref, ok := ts.Find("ref-row")
	if !ok {
		t.Fatal("the reference button is missing")
	}
	got, ok := ts.Find("Check demo")
	if !ok {
		t.Fatal("the row button is missing")
	}
	if diff := got.H - ref.H; diff > 0.5 || diff < -0.5 {
		t.Fatalf("row button is %.2f high, want the 11px row control's %.2f", got.H, ref.H)
	}
}
