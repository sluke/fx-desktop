package chrome

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// reviewTestHost is the drawer on its own: it needs no rail and no terminal to
// be read, which is the point of keeping it a separate surface.
func reviewTestHost(d ReviewData) (Host, *ShellState) {
	sh := DefaultShell()
	sh.ReviewOpen = true
	h := Host{
		Review: d,
		Actions: Actions{
			Commit:         noop,
			StageAll:       noop,
			ToggleReview:   noop,
			CloseReview:    noop,
			SelectChange:   func(string) {},
			RefreshChanges: noop,
		},
	}
	return h, &sh
}

// reviewTester renders the drawer through a pointer to the host so a test can
// change what it is showing and redraw, the way the app loop does.
func reviewTester(h *Host, sh *ShellState) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		reviewDrawer(c, *h, sh, palette(c.Theme()), reviewWide)
	}, 460, 720)
}

func TestReviewDrawerShowsTheTallyFilesAndDiff(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{
		Open:      true,
		Workspace: WorkspaceRepo,
		Branch:    "main",
		Changes:   []Change{{Path: "internal/app/a.go", Kind: "M", Adds: 8, Dels: 4}, {Path: "b.go", Kind: "A", Adds: 4}},
		Selected:  "internal/app/a.go",
		Diff: []DiffLine{
			{Kind: DiffContext, Text: "@@ -1,3 +1,4 @@"},
			{Kind: DiffContext, Text: " func main() {"},
			{Kind: DiffDel, Text: "-old()"},
			{Kind: DiffAdd, Text: "+new()"},
		},
	})
	ts := reviewTester(&h, sh)
	mustHave(t, ts,
		"2 changed · +12 −4 · main", // header
		"internal/app/a.go",         // the file list
		"b.go",
		"@@ -1,3 +1,4 @@", // and the selected file's diff
		"func main() {",
		"-old()",
		"+new()",
	)
}

// Clicking a path opens that path: the drawer is an index plus one document,
// not a stack of every diff at once.
// A working tree can hold thousands of paths. The index must build only the
// rows the drawer shows: building them all cost 120ms a frame at 5000
// changes (§4.10.1).
func TestReviewFileIndexBuildsOnlyWhatIsOnScreen(t *testing.T) {
	changes := make([]Change, 5000)
	for i := range changes {
		changes[i] = Change{Path: fmt.Sprintf("internal/app/zzz-%04d.go", i), Kind: "M", Adds: 1, Dels: 1}
	}
	h, sh := reviewTestHost(ReviewData{
		Open:      true,
		Workspace: WorkspaceRepo,
		Branch:    "main",
		Changes:   changes,
		Selected:  changes[0].Path,
		Diff:      []DiffLine{{Kind: DiffAdd, Text: "+x"}},
	})
	selected := ""
	h.Actions.SelectChange = func(path string) { selected = path }
	ts := reviewTester(&h, sh)
	ts.Frame()

	if _, ok := ts.Find("internal/app/zzz-0000.go"); !ok {
		t.Fatal("the first changed file is not on screen")
	}
	if _, ok := ts.Find("internal/app/zzz-4999.go"); ok {
		t.Fatal("the file index built all 5000 rows, not only the visible ones")
	}
	if err := ts.Click("internal/app/zzz-0002.go"); err != nil {
		t.Fatalf("clicking a visible file failed: %v", err)
	}
	if selected != "internal/app/zzz-0002.go" {
		t.Fatalf("clicking the second file selected %q", selected)
	}
}

func TestReviewDrawerSelectsAFileOnClick(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{
		Open:      true,
		Workspace: WorkspaceRepo,
		Changes:   []Change{{Path: "a.go", Kind: "M"}, {Path: "b.go", Kind: "M"}},
		Selected:  "a.go",
	})
	var picked string
	h.Actions.SelectChange = func(path string) { picked = path }
	ts := reviewTester(&h, sh)
	if err := ts.Click("b.go"); err != nil {
		t.Fatal(err)
	}
	if picked != "b.go" {
		t.Fatalf("picked = %q, want b.go", picked)
	}
}

func TestReviewDrawerClosesAndRefreshes(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{Open: true, Workspace: WorkspaceRepo, Changes: []Change{{Path: "a.go", Kind: "M"}}})
	closed, refreshed := 0, 0
	h.Actions.CloseReview = func() { closed++ }
	h.Actions.RefreshChanges = func() { refreshed++ }
	ts := reviewTester(&h, sh)
	if err := ts.Click("✕"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Click("Refresh"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 || refreshed != 1 {
		t.Fatalf("closed=%d refreshed=%d", closed, refreshed)
	}
}

// An empty drawer has four different things to say, and they are not the
// same thing: broken is not clean, clean is not still-reading, and a
// workspace git never saw is not broken.
func TestReviewDrawerSpeaksItsFourEmptyStates(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{Open: true})
	ts := reviewTester(&h, sh)
	mustHave(t, ts, "Reading workspace…")

	h.Review = ReviewData{Open: true, Workspace: WorkspaceRepo, Branch: "main"}
	ts.Frame()
	mustHave(t, ts, "Working tree clean", "Clean · main")
	mustNotHave(t, ts, "Not a git repository", "No file selected")

	h.Review = ReviewData{Open: true, Workspace: WorkspaceNotRepo}
	ts.Frame()
	mustHave(t, ts, "Not a git repository", "Not a git repository — nothing to review here")
	mustNotHave(t, ts, "Could not read the workspace", "Working tree clean", "Nothing to commit",
		"No file selected")

	h.Review = ReviewData{Open: true, Workspace: WorkspaceFailed}
	ts.Frame()
	mustHave(t, ts, "Could not read the workspace")
	mustNotHave(t, ts, "Working tree clean", "Not a git repository", "Nothing to commit",
		"No file selected")
}

func TestReviewDrawerMarksAnEmptyPaneWithoutInstructions(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{Open: true, Workspace: WorkspaceRepo, Changes: []Change{{Path: "a.go", Kind: "M"}}})
	ts := reviewTester(&h, sh)
	mustHave(t, ts, "No file selected")

	// A read in flight and a file with nothing to colour both have their own
	// words; neither may look like an empty pane.
	h.Review.Selected = "a.go"
	h.Review.Busy = true
	ts.Frame()
	mustHave(t, ts, "Reading diff…")

	h.Review.Busy = false
	h.Review.Note = "No text changes to show in this file"
	ts.Frame()
	mustHave(t, ts, "No text changes to show in this file")
}

// A long diff is capped at the same limit the timeline uses, and says how
// much it left out rather than trailing off.
func TestReviewDrawerCapsALongDiff(t *testing.T) {
	lines := make([]DiffLine, maxDiffRenderLines+25)
	for i := range lines {
		lines[i] = DiffLine{Kind: DiffAdd, Text: "+line"}
	}
	h, sh := reviewTestHost(ReviewData{
		Open: true, Workspace: WorkspaceRepo,
		Changes:  []Change{{Path: "big.go", Kind: "M"}},
		Selected: "big.go",
		Diff:     lines,
	})
	ts := reviewTester(&h, sh)
	mustHave(t, ts, "… 25 more lines")
}

// Both caps have to be able to say something: the read cap leaves behind
// lines the app never held, the render cap leaves lines it did.
func TestReviewDrawerReportsUnreadLines(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{
		Open: true, Workspace: WorkspaceRepo,
		Changes:  []Change{{Path: "huge.go", Kind: "M"}},
		Selected: "huge.go",
		Diff:     []DiffLine{{Kind: DiffAdd, Text: "+one"}},
		Omitted:  900,
	})
	ts := reviewTester(&h, sh)
	mustHave(t, ts, "… 900 more lines not read")
}

// Both working-tree writes stage everything, so both live next to the commit
// message they need — and neither is offered on a clean tree.
func TestReviewDrawerHidesTheCommitBoxOnACleanTree(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{Open: true, Workspace: WorkspaceRepo, Changes: []Change{{Path: "a.go", Kind: "M"}}})
	ts := reviewTester(&h, sh)
	mustHave(t, ts, "Commit", "Stage all")

	h.Review = ReviewData{Open: true, Workspace: WorkspaceRepo}
	ts.Frame()
	mustHave(t, ts, "Nothing to commit")
	mustNotHave(t, ts, "Stage all")
}

// The commit row offers two git writes; both must be reachable at every
// drawer width. FillWidth is width:100% — an absolute claim on the row — so
// the Commit button used to take the whole row and push "Stage all" clean
// out of the window (rect W=0 at x=1286 in a 1280-wide window, §4.8.4 W1).
func TestReviewCommitRowKeepsEveryControlWhole(t *testing.T) {
	var base float32
	for i, w := range []int{1440, 1280, 1100, 900} {
		h, sh := testHost()
		sh.ReviewOpen = true
		h.Review = ReviewData{Open: true, Branch: "main", Workspace: WorkspaceRepo, Selected: "a.go",
			Changes: []Change{{Path: "a.go", Kind: "M", Adds: 1, Dels: 1}},
			Diff:    []DiffLine{{Kind: DiffContext, Text: "@@ -1,2 +1,3 @@"}}}
		h.Actions.Commit = noop
		h.Actions.StageAll = noop
		ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, w, 800)
		ts.Frame()
		ts.Frame()

		commit, okCommit := ts.Find("Commit")
		stage, okStage := ts.Find("Stage all")
		if !okCommit || !okStage {
			t.Fatalf("w=%d: the commit row is missing a control (Commit=%v, Stage all=%v)", w, okCommit, okStage)
		}
		for name, r := range map[string]ui.Rect{"Commit": commit, "Stage all": stage} {
			if r.W <= 0 || r.H <= 0 {
				t.Errorf("w=%d: %s is drawn with no size: %+v", w, name, r)
			}
			if r.X+r.W > float32(w)+0.5 {
				t.Errorf("w=%d: %s runs past the window edge: %+v", w, name, r)
			}
		}
		if commit.X+commit.W > stage.X {
			t.Errorf("w=%d: Commit overlaps Stage all (commit ends at %.1f, Stage all starts at %.1f)",
				w, commit.X+commit.W, stage.X)
		}
		if i == 0 {
			base = stage.W
			continue
		}
		// A control squeezed by a few points ellipsises its label; the label
		// here keeps the width it has with room to spare.
		if d := stage.W - base; d < -0.5 || d > 0.5 {
			t.Errorf("w=%d: Stage all lost its width (%.1f, want %.1f) — a squeezed control is not a control", w, stage.W, base)
		}
	}
}

// The drawer is a panel, not an overlay: closing it must remove it from the
// frame rather than leave it drawn behind something else.
func TestFrameOnlyDrawsTheDrawerWhenItIsOpen(t *testing.T) {
	h, sh := testHost()
	h.Review = ReviewData{Open: true, Workspace: WorkspaceRepo, Changes: h.Data.Changes, Selected: "a.go",
		Diff: []DiffLine{{Kind: DiffAdd, Text: "+brand new line"}}}
	sh.ReviewOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "+brand new line")

	sh.ReviewOpen = false
	ts.Frame()
	mustNotHave(t, ts, "+brand new line")
}

// The rail keeps the count and the way in; the file list is the drawer's.
func TestRailEntryOpensTheReviewDrawer(t *testing.T) {
	h, _ := testHost()
	opened := 0
	h.Actions.ToggleReview = func() { opened++ }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "1 changed · +4 −1")
	if err := ts.Click("1 changed · +4 −1"); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("opened = %d, want 1", opened)
	}
	// The file list itself must not be in the rail any more: the path only
	// appears in the drawer.
	mustNotHave(t, ts, "a.go")
}

func TestRailChangesEntryWithoutAWorkspaceRead(t *testing.T) {
	h, _ := testHost()
	h.Data.Changes = nil
	h.Data.Workspace = WorkspaceRepo
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Working tree clean")

	h.Data.Workspace = WorkspaceFailed
	ts.Frame()
	mustHave(t, ts, "Could not read the workspace")

	h.Data.Workspace = WorkspaceNotRepo
	ts.Frame()
	mustHave(t, ts, "Not a git repository")
	mustNotHave(t, ts, "Could not read the workspace")

	h.Data.Workspace = WorkspaceReading
	ts.Frame()
	mustHave(t, ts, "Reading workspace…")
}

// The drawer gives way before the conversation does: on a narrow window it
// shrinks, and it never squeezes the work below mainMinWide.
func TestClampReviewWidthLeavesTheConversationRoom(t *testing.T) {
	if got := clampReviewWidth(1600); got != reviewWide {
		t.Fatalf("a wide window gets the full drawer: %v", got)
	}
	if got := clampReviewWidth(mainMinWide + 300); got != 300 {
		t.Fatalf("a narrow window shrinks the drawer: %v", got)
	}
	if got := clampReviewWidth(400); got != sidebarMinWide {
		t.Fatalf("an unusable window keeps the drawer's floor: %v", got)
	}
	if got := clampReviewWidth(0); got != reviewWide {
		t.Fatalf("an unmeasured window gets the default: %v", got)
	}
}

// The clamp is only half the promise: the drawer must also honour the width
// it is handed. It once used Fill(), which sets a width of 100%, so the panel
// swallowed the row and left the conversation a sliver — the composer's own
// controls overlapped in what was left.
func TestReviewDrawerHonoursItsWidthBesideTheRail(t *testing.T) {
	h, sh := testHost()
	sh.SidebarOpen = true
	sh.SidebarWidth = sidebarMinWide
	sh.ReviewOpen = true
	h.Review = ReviewData{Open: true, Workspace: WorkspaceRepo, Changes: h.Data.Changes, Selected: "a.go"}
	const window = 900
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, window, 700)

	// The file list's first row is the drawer's own content, so it lands
	// just inside the drawer's left edge — wherever the layout put that.
	row, ok := ts.Find("a.go")
	if !ok {
		t.Fatalf("the drawer did not draw\nhave: %v", ts.Texts())
	}
	want := clampReviewWidth(window - sh.SidebarWidth)
	drawerLeft := float32(window) - want
	if row.X < drawerLeft {
		t.Fatalf("drawer starts at %v but its own row is at %v: the conversation was swallowed by it", drawerLeft, row.X)
	}
	if row.X > drawerLeft+60 {
		t.Fatalf("drawer row at %v, want just inside %v", row.X, drawerLeft)
	}
	// Its right-hand controls stay inside the window: the drawer ends where
	// the window does rather than running under the rail's edge.
	if control, ok := ts.Find("Refresh"); !ok || control.X > float32(window)-panelPad {
		t.Fatalf("drawer control at %v, want inside the window's right edge %v", control.X, window)
	}
	// And the two panes still leave the conversation the width the clamp
	// reserves for it.
	if conversation := drawerLeft - sh.SidebarWidth; conversation < float32(mainMinWide)-1 {
		t.Fatalf("conversation is left %v wide, want at least %v", conversation, mainMinWide)
	}
}

// The rail and the drawer share one window, so the rail's own maximum gives
// way to the drawer: the conversation keeps the minimum both clamps promise
// it even with both panes wide open.
func TestFrameGivesWayToTheDrawerWhenBothPanesAreOpen(t *testing.T) {
	h, sh := testHost()
	sh.SidebarOpen = true
	sh.ReviewOpen = true
	sh.SidebarWidth = sidebarMaxWide
	h.Review = ReviewData{Open: true, Workspace: WorkspaceRepo, Changes: h.Data.Changes, Selected: "a.go"}
	const window = 1100
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, window, 800)
	ts.Frame()

	drawer := clampReviewWidth(window - float32(sidebarMinWide))
	conversation := float32(window) - sh.SidebarWidth - drawer
	if conversation < float32(mainMinWide)-1 {
		t.Fatalf("rail %v + drawer %v leave the conversation %v wide, want at least %v",
			sh.SidebarWidth, drawer, conversation, mainMinWide)
	}
	if sh.SidebarWidth < float32(sidebarMinWide) {
		t.Fatalf("the rail shrank past its own minimum: %v", sh.SidebarWidth)
	}
}

// The palette offers the drawer where there is one to open, and the row is
// what actually opens it.
func TestPaletteOffersReviewChanges(t *testing.T) {
	var cmd Command
	for _, candidate := range Commands {
		if candidate.ID == "review-changes" {
			cmd = candidate
		}
	}
	if cmd.ID == "" {
		t.Fatal("the palette has no review-changes row")
	}
	if cmd.Hint != "⌘R" {
		t.Fatalf("hint = %q, want the accelerator the menu binds", cmd.Hint)
	}
	if commandAvailable(Host{}, cmd) {
		t.Fatal("review-changes is offered with nothing behind it")
	}
	if !commandAvailable(Host{Actions: Actions{ToggleReview: noop}}, cmd) {
		t.Fatal("review-changes is hidden where it would work")
	}

	sh := DefaultShell()
	opened := 0
	h := Host{Shell: &sh, Actions: Actions{ToggleReview: func() { opened++ }}}
	runCommand(h, &sh, cmd)
	if opened != 1 {
		t.Fatalf("the palette row did not open the drawer: %d", opened)
	}
}

// The commit row keeps the app's type scale. Left at the widget defaults its
// input and buttons render several pixels taller than everything around them
// and the footer reads as a different, louder panel (§4.9: the layout carries
// the hierarchy, not one oversized control).
func TestReviewCommitRowUsesTheAppTypeScale(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{
		Open:      true,
		Workspace: WorkspaceRepo,
		Branch:    "main",
		Changes:   []Change{{Path: "a.go", Kind: "M", Adds: 1}},
		Selected:  "a.go",
	})
	ts := ui.NewTester(func(c *ui.Context) {
		ApplyTheme(c, "light")
		ui.Row(c).Children(func() {
			reviewDrawer(c, h, sh, palette(c.Theme()), reviewWide)
			// The reference controls are the app's working sizes: an
			// 11px button (as every tools-page button) and a 12px
			// input (as every form field).
			ui.Column(c).Children(func() {
				ui.Button(c, "ref-button").FontSize(11).Padding(6, 9)
				ui.TextInput(c, new(string)).Label("ref-input").FontSize(12).FillWidth()
			})
		})
	}, 700, 720)
	ts.SetDark(false)
	ts.Frame()

	refBtn, ok := ts.Find("ref-button")
	if !ok {
		t.Fatal("the reference button is missing")
	}
	for _, label := range []string{"Commit", "Stage all"} {
		got, ok := ts.Find(label)
		if !ok {
			t.Fatalf("%q is missing", label)
		}
		if diff := got.H - refBtn.H; diff > 0.5 || diff < -0.5 {
			t.Fatalf("%q is %.2f high, want the app's %.2f button height", label, got.H, refBtn.H)
		}
	}
	input, ok := ts.Find("Commit message")
	if !ok {
		t.Fatal("the commit message input is missing")
	}
	refIn, ok := ts.Find("ref-input")
	if !ok {
		t.Fatal("the reference input is missing")
	}
	if diff := input.H - refIn.H; diff > 0.5 || diff < -0.5 {
		t.Fatalf("the commit input is %.2f high, want the app's %.2f field height", input.H, refIn.H)
	}
}

// A commit with an empty message must not dispatch. The button is disabled
// at the source and OnClick re-checks the live message, so a stale frame
// cannot submit either. The app layer's error dialog stays as a second
// guard, but the UI no longer offers a write it cannot perform.
func TestReviewCommitRequiresAMessage(t *testing.T) {
	h, sh := reviewTestHost(ReviewData{Open: true, Workspace: WorkspaceRepo,
		Changes: []Change{{Path: "a.go", Kind: "M"}}})
	commits := 0
	h.Actions.Commit = func() { commits++ }
	ts := reviewTester(&h, sh)
	ts.Frame()

	// Click may report the disabled control or stay silent; either way the
	// write must not have happened.
	_ = ts.Click("Commit")
	if commits != 0 {
		t.Fatalf("empty commit message dispatched %d commit(s)", commits)
	}

	sh.CommitMsg = "tighten the design system"
	ts.Frame()
	if err := ts.Click("Commit"); err != nil {
		t.Fatalf("click enabled Commit: %v", err)
	}
	if commits != 1 {
		t.Fatalf("a message should dispatch exactly one commit, got %d", commits)
	}
}
