package chrome

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// testHost is a fully populated shell: the rail has two sessions, the
// workspace has one change and one saved conversation, and every agent stat
// is filled, so a render test can assert on all of it at once.
// noop stands in for a wired action: the rail guards on nil, so a host
// without actions legitimately renders without its footer rows.
func noop() {}

func testHost() (Host, *ShellState) {
	sh := DefaultShell()
	h := Host{
		Title:       "fx — proj",
		Subtitle:    "~/Projects/proj",
		StatusLeft:  []string{"fx v0.0.13"},
		StatusHi:    "● 1 modified",
		StatusRight: []string{"/p/proj"},
		SessionBar:  SessionBar{Name: "proj", Branch: "main", Count: 2, Running: true},
		Actions: Actions{
			OpenFolder:      noop,
			OpenProject:     func(string) {},
			OpenPreferences: noop,
			OpenDocs:        noop,
			OpenConfig:      noop,
			RunDoctor:       noop,
			CheckUpdates:    noop,
			NewSession:      noop,
			Commit:          noop,
			RunCommand:      func(string) {},
			ResumeSession:   func(string) {},
			FocusSession:    func(string) {},
			Ask:             func(string) {},
			ChooseConfig:    func(string) {},
			SetAppearance:   func(string) {},
			StageAll:        noop,
			ToggleReview:    noop,
			CloseReview:     noop,
			SelectChange:    func(string) {},
			RefreshChanges:  noop,
			ToggleConsole:   noop,
			SelectConsole:   func(uint64) {},
			CloseConsoleRun: func(uint64) {},
		},
		Data: SidebarData{
			ProjectPath: "/p/proj",
			FxVersion:   "fx v0.0.13",
			Changes:     []Change{{Path: "a.go", Kind: "M", Adds: 4, Dels: 1}},
			Saved:       []SavedSession{{ID: "abc123", Title: "Rename the panel", When: "2m ago", Turns: 3}},
			// A host with changes is a host whose tree git answered for.
			Workspace: WorkspaceRepo,
		},
		Sessions: []SessionSummary{
			{ID: "/p/proj", Workspace: "/p/proj", Title: "proj", Status: AttentionWorking, Branch: "main"},
			{ID: "/p/other", Workspace: "/p/other", Title: "other", Status: AttentionNeedsApproval},
		},
		Active: "/p/proj",
		Agent: Agent{
			Status:     "Running",
			Model:      "zenmux/deepseek/deepseek-v4.1-flash",
			Permission: "auto",
			Tokens:     "128.4k",
			Spend:      "$1.84",
			Elapsed:    "04:12",
		},
		Shell: &sh,
		Main:  func(c *ui.Context) { ui.Text(c, "TERMINALVIEW") },
	}
	return h, &sh
}

func mustHave(t *testing.T, ts *ui.Tester, want ...string) {
	t.Helper()
	for _, w := range want {
		if !ts.HasText(w) {
			t.Errorf("frame is missing %q\nhave: %v", w, ts.Texts())
		}
	}
}

// mustNotHaveToken matches a whole text token, not a substring: "04" must
// not match the elapsed time "04:12".
func mustNotHaveToken(t *testing.T, ts *ui.Tester, unwanted ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, x := range ts.Texts() {
		have[x] = true
	}
	for _, w := range unwanted {
		if have[w] {
			t.Errorf("frame should not show the token %q\nhave: %v", w, ts.Texts())
		}
	}
}

func mustNotHave(t *testing.T, ts *ui.Tester, unwanted ...string) {
	t.Helper()
	for _, w := range unwanted {
		if ts.HasText(w) {
			t.Errorf("frame should not show %q\nhave: %v", w, ts.Texts())
		}
	}
}

func TestFrameRendersEveryRegion(t *testing.T) {
	h, _ := testHost()
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts,
		"Sessions",    // rail section
		"New Session", // rail header action
		"proj",        // active session row
		"Changes",     // working-tree section
		"1 changed",   // the drawer entry (the file list lives in the drawer)
		"History",     // saved conversations
		"Rename the panel",
		"TERMINALVIEW", // main content
		"Model", "Tokens", "Spend", "Elapsed",
		"zenmux/deepseek/deepseek-v4.1-flash",
		"fx v0.0.13", // status bar
		"agent live", // session bar
	)
}

// The rail is the product's spine: a session that wants the user floats up
// and says why, in one word, on its own row.
func TestRailLabelsEveryAttentionState(t *testing.T) {
	h, _ := testHost()
	var labels []string
	for _, a := range []Attention{
		AttentionNeedsApproval, AttentionErrored, AttentionWorking,
		AttentionDoneUnreviewed, AttentionExited, AttentionIdle,
	} {
		if label := attentionLabel(a); label != "" {
			labels = append(labels, label)
		}
	}
	h.Sessions = nil
	for i, a := range []Attention{
		AttentionNeedsApproval, AttentionErrored, AttentionWorking,
		AttentionDoneUnreviewed, AttentionExited,
	} {
		h.Sessions = append(h.Sessions, SessionSummary{
			ID:        itoa(i),
			Workspace: "/p/" + itoa(i),
			Title:     "s" + itoa(i),
			Status:    a,
			Changes:   2,
		})
	}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, labels...)
	// Idle is the unremarkable state: no word, no alarm.
	if attentionLabel(AttentionIdle) != "" {
		t.Fatal("idle should not be labelled")
	}
}

// The palette advertises only keys a menu really binds, and it no longer
// offers the deleted Explorer/Search panels.
func TestPaletteDropsDeletedPanels(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustNotHave(t, ts, "Show Explorer", "Show Search", "Show Changes", "Show Settings")
	mustHave(t, ts, "Toggle Sidebar", "Focus next session needing you")
}

// Quiet by default: ask and auto are the unremarkable permission modes,
// so the strip omits the stat entirely rather than stating the obvious.
func TestPermissionIsQuietWhenDefault(t *testing.T) {
	for _, mode := range []string{"ask", "auto"} {
		h, _ := testHost()
		h.Agent.Permission = mode
		ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
		mustNotHave(t, ts, "Permission", mode)
	}
}

// full-access turns fx's checks off: that is worth a permanent, accented
// mention even though the default modes are silent.
func TestPermissionShowsWhenRelaxed(t *testing.T) {
	h, _ := testHost()
	h.Agent.Permission = "full-access"
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Permission", "full-access")
}

func TestFrameRendersInDark(t *testing.T) {
	h, _ := testHost()
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.SetDark(true)
	ts.Frame()
	mustHave(t, ts, "Sessions", "Model", "TERMINALVIEW", "fx v0.0.13")
}

// A console/command window opens with the rail collapsed; nothing should be
// drawn in its place.
func TestClosedSidebarHidesRail(t *testing.T) {
	h, sh := testHost()
	sh.SidebarOpen = false
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "TERMINALVIEW", "Model")
	mustNotHave(t, ts, "Sessions", "Changes")
}

func TestFrameHidesEmptyAgentStats(t *testing.T) {
	h, _ := testHost()
	h.Agent = Agent{Status: "Running"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Running", "TERMINALVIEW")
	mustNotHave(t, ts, "Model", "Permission", "Tokens", "Spend")
}

// Context usage only appears once fx has reported a window size, and it is
// hidden when nothing has been confirmed: the strip never guesses.
func TestFrameShowsContextUsageOnlyWhenKnown(t *testing.T) {
	h, _ := testHost()
	h.Agent.ContextUsed = 0
	h.Agent.ContextTotal = 0
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1440, 800)
	mustNotHave(t, ts, "Context 0 / 0")

	h.Agent.ContextUsed = 128000
	h.Agent.ContextTotal = 200000
	ts.Frame()
	mustHave(t, ts, "Context 128.0k / 200.0k")
}

// The context caption and the Tokens block beside it are one unit system
// (§4.8.4 W2): a raw integer next to "128.4k" reads as a different quantity.
func TestContextCaptionUsesTheSameUnitsAsTokens(t *testing.T) {
	cases := []struct {
		used, total int
		want        string
	}{
		{0, 0, "Context 0 / 0"},
		{820, 4000, "Context 820 / 4.0k"},
		{128000, 200000, "Context 128.0k / 200.0k"},
		{105244, 131072, "Context 105.2k / 131.1k"},
		{2400000, 3000000, "Context 2.4M / 3.0M"},
	}
	for _, c := range cases {
		got := contextText(Agent{ContextUsed: c.used, ContextTotal: c.total, Tokens: "128.4k"})
		if got != c.want {
			t.Errorf("contextText(%d, %d) = %q, want %q", c.used, c.total, got, c.want)
		}
	}

	// And it is what the strip prints: no raw count survives beside Tokens.
	h, _ := testHost()
	h.Agent.ContextUsed, h.Agent.ContextTotal = 105244, 131072
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1440, 800)
	mustHave(t, ts, "Context 105.2k / 131.1k", "128.4k")
	mustNotHaveToken(t, ts, "105244", "131072")
}

// A clean repository has no changed paths, but the read did finish. The rail
// must not keep claiming it is still loading.
func TestChangesCleanWorkspaceIsNotLoading(t *testing.T) {
	h, _ := testHost()
	h.Data.Changes = nil
	h.Data.Workspace = WorkspaceRepo
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Working tree clean")
	mustNotHave(t, ts, "Reading workspace…")
}

func TestChangesWorkspaceErrorIsExplicit(t *testing.T) {
	h, _ := testHost()
	h.Data.Changes = nil
	h.Data.Workspace = WorkspaceFailed
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Could not read the workspace")
	mustNotHave(t, ts, "Reading workspace…")
}

// A workspace git never saw is not a broken one. The rail says which it is,
// and says it in its own words: "Could not read the workspace" would send the
// user looking for a fault that is not there.
func TestChangesNoRepositoryIsNotAnError(t *testing.T) {
	h, _ := testHost()
	h.Data.Changes = nil
	h.Data.Workspace = WorkspaceNotRepo
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Not a git repository")
	mustNotHave(t, ts, "Could not read the workspace", "Reading workspace…")
}

// Split can be dragged, but the app keeps the rail within useful bounds.
func TestFrameClampsSidebarWidth(t *testing.T) {
	h, sh := testHost()
	sh.SidebarWidth = 999
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	if sh.SidebarWidth != float32(sidebarMaxWide) {
		t.Fatalf("sidebar width = %v, want %v", sh.SidebarWidth, sidebarMaxWide)
	}

	sh.SidebarWidth = 20
	ts = ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	if sh.SidebarWidth != float32(sidebarMinWide) {
		t.Fatalf("sidebar width = %v, want %v", sh.SidebarWidth, sidebarMinWide)
	}
}

// A long saved-session title must not wrap the right-hand turn count into a
// two-line row.
func TestHistoryMetaStaysOnOneLine(t *testing.T) {
	h, _ := testHost()
	h.Data.Saved = []SavedSession{{
		ID:    "long",
		Title: strings.Repeat("A very long saved session title ", 4),
		When:  "2m ago",
		Turns: 12,
	}}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	r, ok := ts.Find("12 turns")
	if !ok {
		t.Fatalf("turn count not found\nhave: %v", ts.Texts())
	}
	if r.H > 18 {
		t.Fatalf("turn count wrapped onto multiple lines: %+v", r)
	}
}

func TestFrameRendersEmptyChangesAndSessions(t *testing.T) {
	h, _ := testHost()
	h.Data.Changes = nil
	h.Data.Workspace = WorkspaceRepo
	h.Data.Saved = nil
	h.Sessions = nil
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Working tree clean", "No sessions yet")
}

func TestFrameRendersWelcome(t *testing.T) {
	h, _ := testHost()
	h.Data = SidebarData{
		Welcome:     true,
		WelcomeText: "Downloading…",
		OnInstall:   func() {},
	}
	h.Agent = Agent{}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Install fx", "Download fx")
}

func TestFrameRendersCommandPaletteWithBothGroups(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Project & agent", "View", "Quick ask…", "Choose model…", "Toggle Sidebar")
}

// The palette must not advertise what the window cannot run: without a
// workspace there is no model to choose and nothing to ask about.
func TestFramePaletteHidesUnavailableCommands(t *testing.T) {
	h, sh := testHost()
	h.Actions.Ask = nil
	h.Actions.ChooseConfig = nil
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Toggle Sidebar")
	mustNotHave(t, ts, "Quick ask…", "Choose model…")
}

// fx commands that act on a repository need one. A window without a
// workspace must not offer them: run in the app's own directory they would
// touch a repository nobody opened.
func TestFramePaletteHidesWorkspaceCommandsWithoutProject(t *testing.T) {
	h, sh := testHost()
	h.Data.ProjectPath = ""
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustNotHave(t, ts, "Continue last session", "fx: Draft a pull request", "fx: Draft an issue")
	// Diagnostics are global: they stay available everywhere.
	mustHave(t, ts, "fx: Run doctor", "Toggle Sidebar")
}

func TestFramePaletteFiltersByQuery(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	sh.PaletteQuery = "doctor"
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "fx: Run doctor")
	mustNotHave(t, ts, "fx: Draft a pull request")
}

func TestFrameRendersAskDialog(t *testing.T) {
	h, sh := testHost()
	sh.AskOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Quick ask", "Run", "Cancel")
}

func TestCenterPageRenders(t *testing.T) {
	ts := ui.NewTester(func(c *ui.Context) {
		CenterPage(c, "02 / PROJECT", "Open a project", "Pick a folder.", "Choose folder…", func() {})
	}, 1280, 800)
	mustHave(t, ts, "fx", "Open a project", "Pick a folder.", "Choose folder…")
}

// ---- interaction: clicks drive shell state and fire the wired actions ----

// Clicking another session raises its window; the active row does nothing.
func TestClickingRailRowFocusesSession(t *testing.T) {
	h, _ := testHost()
	var focused []string
	h.Actions.FocusSession = func(id string) { focused = append(focused, id) }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	if err := ts.Click("other"); err != nil {
		t.Fatalf("click session: %v", err)
	}
	if len(focused) != 1 || focused[0] != "/p/other" {
		t.Fatalf("focused = %v", focused)
	}
	if err := ts.Click("proj"); err != nil {
		t.Fatalf("click active session: %v", err)
	}
	if len(focused) != 1 {
		t.Fatalf("clicking the active session must not refocus: %v", focused)
	}
}

func TestClickingSavedSessionResumes(t *testing.T) {
	h, _ := testHost()
	var resumed string
	h.Actions.ResumeSession = func(id string) { resumed = id }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	if err := ts.Click("Rename the panel"); err != nil {
		t.Fatalf("click saved session: %v", err)
	}
	if resumed != "abc123" {
		t.Fatalf("resumed = %q", resumed)
	}
}

func TestClickingPaletteRowRunsIt(t *testing.T) {
	h, sh := testHost()
	var ran string
	h.Actions.RunCommand = func(id string) { ran = id }
	sh.PaletteOpen = true
	// Narrow the list first: rows below the scroll viewport cannot be
	// clicked, and doctor sits far down an unfiltered palette.
	sh.PaletteQuery = "doctor"
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	if err := ts.Click("fx: Run doctor"); err != nil {
		t.Fatalf("click palette row: %v", err)
	}
	if ran != "run-doctor" {
		t.Fatalf("ran = %q", ran)
	}
	if sh.PaletteOpen {
		t.Fatal("running a command should close the palette")
	}
}

// The working-tree writes live in the review drawer, so the click test opens
// it: the rail only points at the drawer.
func TestClickingCommitFiresAction(t *testing.T) {
	h, sh := testHost()
	commits := 0
	h.Actions.Commit = func() { commits++ }
	sh.CommitMsg = "refactor the shell"
	sh.ReviewOpen = true
	h.Review = ReviewData{Open: true, Workspace: WorkspaceRepo, Changes: h.Data.Changes, Selected: "a.go"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	if err := ts.Click("Commit"); err != nil {
		t.Fatalf("click Commit: %v", err)
	}
	if commits != 1 {
		t.Fatalf("commits = %d", commits)
	}
}

func TestClickingNewSessionFiresAction(t *testing.T) {
	h, _ := testHost()
	opens := 0
	h.Actions.NewSession = func() { opens++ }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	if err := ts.Click("New Session"); err != nil {
		t.Fatalf("click New Session: %v", err)
	}
	if opens != 1 {
		t.Fatalf("opens = %d", opens)
	}
}

func TestClickingInstallFiresOnInstall(t *testing.T) {
	h, _ := testHost()
	installs := 0
	h.Data = SidebarData{Welcome: true, OnInstall: func() { installs++ }}
	h.Agent = Agent{}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	if err := ts.Click("Download fx"); err != nil {
		t.Fatalf("click Download fx: %v", err)
	}
	if installs != 1 {
		t.Fatalf("installs = %d", installs)
	}
}

func TestPaletteCoversBothAppearances(t *testing.T) {
	light := palette(ui.LightTheme())
	dark := palette(ui.DarkTheme())
	for name, c := range map[string]ui.Color{
		"light.Window": light.Window, "light.Surface": light.Surface, "light.Ink": light.Ink,
		"light.Accent": light.Accent, "light.TermBG": light.TermBG,
		"dark.Window": dark.Window, "dark.Surface": dark.Surface, "dark.Ink": dark.Ink,
		"dark.Accent": dark.Accent, "dark.TermBG": dark.TermBG,
	} {
		if c.A == 0 {
			t.Errorf("%s is fully transparent", name)
		}
	}
	// Neutral surfaces deliberately replace the old teal/brown brand.
	if light.Window != ui.Hex("#FFFFFF") || light.Surface != ui.Hex("#F5F5F5") || dark.Window != ui.Hex("#090909") {
		t.Fatal("unexpected neutral work surfaces")
	}
	if light.TermBG != light.Window || dark.TermBG != dark.Window {
		t.Fatal("the terminal must share the working surface")
	}
}

// The picker and setup screens are a bare center page on paper — no rail,
// no sidebar.
func TestCenterOnlyStripsRailAndSidebar(t *testing.T) {
	h, _ := testHost()
	h.CenterOnly = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "TERMINALVIEW")
	mustNotHave(t, ts, "Sessions", "Changes", "New Session")
	// the title bar drops the sidebar toggle too — there is no sidebar
	mustNotHave(t, ts, "Toggle Sidebar")
}

// The palette is keyboard-first. The keys themselves come from the combobox
// widget; what belongs to the shell, and what this pins down, is that the
// arrows walk the same rows on screen and that Enter runs the one the
// highlight is on.
func TestPaletteArrowsAndEnterRunTheHighlightedCommand(t *testing.T) {
	h, sh := testHost()
	var ran []string
	h.Actions.RunCommand = func(id string) { ran = append(ran, id) }
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	rows := paletteGroupRows(h, "")
	if len(rows) == 0 || len(rows[0].Rows) < 2 {
		t.Fatalf("fixture has too few commands to navigate: %v", rows)
	}
	want := rows[0].Rows[1].ID
	// The first Down lands on the first row, the second moves to the next.
	ts.Key(0, ui.KeyDown)
	ts.Key(0, ui.KeyDown)
	ts.Key(0, ui.KeyEnter)
	if len(ran) != 1 || ran[0] != want {
		t.Fatalf("enter ran %v, want just %q", ran, want)
	}
	if sh.PaletteOpen {
		t.Fatal("running a command must close the palette")
	}
}

// Escape closes the palette rather than leaving a modal that only the mouse
// can dismiss.
func TestPaletteEscapeClosesIt(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	ts.Key(0, ui.KeyEscape)
	// Escape folds the command list first; the sheet follows it rather than
	// staying behind as an empty box, so one key press and one frame is
	// enough to be rid of it.
	ts.Frame()
	if sh.PaletteOpen {
		t.Fatal("escape must close the palette")
	}
}

// Typing narrows the rows and preselects the first one, so ⌘K + a few
// letters + Enter is the whole interaction.
func TestPaletteTypingPreselectsFirstMatch(t *testing.T) {
	h, sh := testHost()
	var ran []string
	h.Actions.RunCommand = func(id string) { ran = append(ran, id) }
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	ts.Type("doctor")
	ts.Frame()
	mustHave(t, ts, "fx: Run doctor")
	mustNotHave(t, ts, "Quick ask…")
	ts.Key(0, ui.KeyEnter)
	if len(ran) != 1 || ran[0] != "run-doctor" {
		t.Fatalf("typing then Enter ran %v", ran)
	}
}

// The palette is one panel (§4.6): the field, the list under it and the key
// hints at its foot, all of one width inside one border. Its list used to be
// a combobox's popup, which is a layer of its own: the list came out the
// width of the window, hung off the field's edges and ran past the window's
// bottom, and the panel itself shrank to a box around the field.
func TestPaletteIsOnePanel(t *testing.T) {
	const windowW, windowH = 1280, 800
	h, sh := testHost()
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, windowW, windowH)
	ts.Frame()

	panel, ok := ts.Find("Command palette")
	if !ok {
		t.Fatal("the palette panel is unnamed, so nothing can measure it")
	}
	if panel.W != float32(contentMaxWidth) {
		t.Fatalf("panel is %v wide, want %v", panel.W, contentMaxWidth)
	}
	if want := (float32(windowW) - panel.W) / 2; panel.X != want {
		t.Fatalf("panel sits at x=%v, want %v (centered)", panel.X, want)
	}
	// The field is the panel's top row and fills the panel's inside: any
	// widget beside it in that row would leave the list out of line with the
	// panel's own edges.
	const border, pad = 1, panelPad
	field, ok := ts.Find("Search commands")
	if !ok {
		t.Fatal("the palette field is unnamed, so nothing can measure it")
	}
	if field.X != panel.X+border || field.W != panel.W-2*border {
		t.Fatalf("field %+v does not fill panel %+v inside its border", field, panel)
	}
	// A row, and the key hints at the panel's foot: they end at the panel's
	// edge, not at the window's.
	inner := panel.X + panel.W - border
	for _, label := range []string{"Open Folder…", "↑↓ move · enter run · esc dismiss"} {
		r, ok := ts.Find(label)
		if !ok {
			t.Fatalf("nothing shows %q", label)
		}
		if r.X < panel.X || r.X+r.W > inner {
			t.Fatalf("%q at %+v is outside the panel %+v", label, r, panel)
		}
	}
	// The foot's right-hand text is flush with the panel's edge, which is
	// what a right-aligned row inside the panel means.
	count, ok := ts.Find(itoa(availableCommands(h)) + " commands")
	if !ok {
		t.Fatal("the palette foot does not say how many commands there are")
	}
	if got := count.X + count.W; got != inner-pad {
		t.Fatalf("the count ends at %v, want %v (the panel's edge)", got, inner-pad)
	}
}

// The panel gives way on a narrow window rather than hanging over its edges.
func TestPalettePanelFitsANarrowWindow(t *testing.T) {
	const windowW, windowH = 600, 600
	h, sh := testHost()
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, windowW, windowH)
	ts.Frame()

	panel, ok := ts.Find("Command palette")
	if !ok {
		t.Fatal("the palette panel is unnamed, so nothing can measure it")
	}
	if want := float32(windowW - 48); panel.W != want {
		t.Fatalf("panel is %v wide, want %v (the window less its margins)", panel.W, want)
	}
	if want := (float32(windowW) - panel.W) / 2; panel.X != want {
		t.Fatalf("panel sits at x=%v, want %v (centered)", panel.X, want)
	}
	r, ok := ts.Find("↑↓ move · enter run · esc dismiss")
	if !ok {
		t.Fatal("the palette foot is missing")
	}
	if r.X < panel.X || r.X+r.W > panel.X+panel.W {
		t.Fatalf("the key hints at %+v are outside the panel %+v", r, panel)
	}
}

// However big the window is, the sheet is a sheet: inside it, with its field
// above and its key hints below. A panel that outgrew the window would take
// the field off the top and the hints off the bottom, which is what a small
// window showed.
func TestPalettePanelFitsAnyWindow(t *testing.T) {
	for _, size := range [][2]int{{1600, 1000}, {1280, 800}, {1100, 700}, {900, 640}, {760, 560}, {640, 480}, {520, 420}, {460, 360}} {
		w, windowH := size[0], size[1]
		h, sh := testHost()
		OpenPalette(sh)
		ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, w, windowH)
		ts.Frame()

		panel, ok := ts.Find("Command palette")
		if !ok {
			t.Fatalf("%dx%d: no panel to measure", w, windowH)
		}
		if panel.X < 0 || panel.X+panel.W > float32(w)+0.5 ||
			panel.Y < 0 || panel.Y+panel.H > float32(windowH)+0.5 {
			t.Fatalf("%dx%d: panel %+v is not inside the window", w, windowH, panel)
		}
		field, ok := ts.Find("Search commands")
		if !ok {
			t.Fatalf("%dx%d: the field is not on screen", w, windowH)
		}
		if want := panel.W - 2; field.W != want {
			t.Fatalf("%dx%d: field is %v wide, want %v (the panel inside its border)", w, windowH, field.W, want)
		}
		foot, ok := ts.Find("↑↓ move · enter run · esc dismiss")
		if !ok {
			t.Fatalf("%dx%d: the key hints are not on screen", w, windowH)
		}
		if foot.Y+foot.H > panel.Y+panel.H+0.5 {
			t.Fatalf("%dx%d: the key hints at %+v spill past the panel %+v", w, windowH, foot, panel)
		}
	}
}

// A heading travels with its rows. Dropping a group a query emptied used to
// shift every group after it up one place, so the group that was left took
// the name of the one that went: a search for a View command showed it under
// "Project & agent".
func TestPaletteHeadingTravelsWithItsRows(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	// "toggle" matches two rows of the second group and nothing in the first.
	sh.PaletteQuery = "toggle"
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "View", "Toggle Sidebar", "Toggle Console")
	mustNotHaveToken(t, ts, "Project & agent")
}

// The arrows keep the row they are on in view: the list scrolls inside the
// panel rather than the highlight walking off the bottom of it.
func TestPaletteArrowsKeepTheHighlightedRowInView(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()

	groups := paletteGroupRows(h, "")
	rows := paletteCount(groups)
	if rows < 3 {
		t.Fatalf("the fixture has too few commands to scroll: %d", rows)
	}
	// Down to the last row, which is well past the fold.
	for i := 0; i < rows; i++ {
		ts.Key(0, ui.KeyDown)
	}
	if sh.PaletteHighlight != rows-1 {
		t.Fatalf("highlight = %d, want the last row, %d", sh.PaletteHighlight, rows-1)
	}
	last, ok := paletteHighlighted(groups, rows-1)
	if !ok {
		t.Fatal("the last row does not resolve")
	}
	row, ok := ts.Find(last.Label)
	if !ok {
		t.Fatalf("nothing shows %q", last.Label)
	}
	if row.W == 0 {
		t.Fatalf("the highlighted row %q was not laid out: is the list scrolling it into view?", last.Label)
	}
	foot, ok := ts.Find("↑↓ move · enter run · esc dismiss")
	if !ok {
		t.Fatal("the palette foot is missing")
	}
	if row.Y+row.H > foot.Y {
		t.Fatalf("the highlighted row %q at %+v is behind the foot at y=%v", last.Label, row, foot.Y)
	}
}

// Entering a row with the pointer puts the highlight on it, so the row that
// lights up and the row Enter runs are the same one.
func TestPalettePointerMovesTheHighlight(t *testing.T) {
	h, sh := testHost()
	var ran []string
	h.Actions.RunCommand = func(id string) { ran = append(ran, id) }
	sh.PaletteOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()

	groups := paletteGroupRows(h, "")
	if len(groups) == 0 || len(groups[0].Rows) < 3 {
		t.Fatalf("the fixture has too few commands to hover: %v", groups)
	}
	third := groups[0].Rows[2]
	r, ok := ts.Find(third.Label)
	if !ok {
		t.Fatalf("nothing shows %q", third.Label)
	}
	ts.Move(r.X+r.W/2, r.Y+r.H/2)
	ts.Frame()
	if sh.PaletteHighlight != 2 {
		t.Fatalf("hovering the third row left the highlight at %d", sh.PaletteHighlight)
	}
	ts.Key(0, ui.KeyEnter)
	if len(ran) != 1 || ran[0] != third.ID {
		t.Fatalf("Enter ran %v, want just %q", ran, third.ID)
	}
}

// Both ways in start from the same clean slate. The title bar's magnifier used
// to only set the flag, so after one search the next click reopened onto that
// search instead of onto every command.
func TestPaletteOpensCleanFromEitherEntryPoint(t *testing.T) {
	h, sh := testHost()
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()

	// Search something, run nothing, and put the palette away.
	OpenPalette(sh)
	sh.PaletteQuery = "doctor"
	ts.Frame()
	mustHave(t, ts, "fx: Run doctor")
	sh.PaletteOpen = false
	ts.Frame()

	// The magnifier opens it again: every command, nothing preselected.
	if err := ts.Click("Commands"); err != nil {
		t.Fatal(err)
	}
	if sh.PaletteQuery != "" {
		t.Fatalf("the palette reopened onto %q, want a clean slate", sh.PaletteQuery)
	}
	if sh.PaletteHighlight != -1 {
		t.Fatalf("the palette reopened with row %d highlighted, want none", sh.PaletteHighlight)
	}
	// The unfiltered list is back: with "doctor" in the query the first group
	// was gone, so its heading coming back is the clean slate showing.
	mustHave(t, ts, "Project & agent", "View", "Open Folder…")
}
