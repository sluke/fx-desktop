package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// consoleTestHost is the drawer on its own: it needs no rail, no conversation
// and no terminal to be read, which is the point of keeping it a surface of
// its own.
func consoleTestHost(d ConsoleData) (Host, *ShellState) {
	sh := DefaultShell()
	sh.ConsoleOpen = true
	if len(d.Runs) > 0 {
		sh.ConsoleRun = d.Runs[0].ID
	}
	h := Host{
		Console: d,
		Actions: Actions{
			ToggleConsole:   noop,
			SelectConsole:   func(uint64) {},
			CloseConsoleRun: func(uint64) {},
		},
	}
	return h, &sh
}

func consoleTester(h *Host, sh *ShellState) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		consoleDrawer(c, *h, sh, palette(c.Theme()))
	}, 900, consoleHeight)
}

// A console is a log, not a window: every run it has ever held is a tab, and a
// run that has ended says so with the code it ended on rather than vanishing.
func TestConsoleDrawerListsRunsWithTheirExitCodes(t *testing.T) {
	h, sh := consoleTestHost(ConsoleData{
		Runs: []ConsoleRun{
			{ID: 1, Title: "fx doctor", Command: "fx doctor", Exited: true, Exit: 0},
			{ID: 2, Title: "fx login", Command: "fx login"},
		},
		Body: func(c *ui.Context) { ui.Text(c, "CONSOLE-BODY") },
	})
	sh.ConsoleRun = 2
	ts := consoleTester(&h, sh)
	mustHave(t, ts,
		"fx doctor", "exited 0", // a finished run and its code
		"fx login", "running", // one still going
		"CONSOLE-BODY",                        // the active run's terminal
		"running · click the console to type", // and what a click would do
	)
}

// Clicking a tab shows that run: the drawer is a log with one terminal in it,
// not every terminal at once.
func TestConsoleDrawerSelectsARunOnClick(t *testing.T) {
	h, sh := consoleTestHost(ConsoleData{Runs: []ConsoleRun{
		{ID: 7, Title: "fx doctor"},
		{ID: 9, Title: "fx credits", Exited: true},
	}})
	sh.ConsoleRun = 7
	var picked uint64
	h.Actions.SelectConsole = func(id uint64) { picked = id }
	ts := consoleTester(&h, sh)
	if err := ts.Click("fx credits"); err != nil {
		t.Fatal(err)
	}
	if picked != 9 {
		t.Fatalf("picked = %d, want the clicked run 9", picked)
	}
}

// Closing one tab closes one run: the others, and the drawer, stay.
func TestConsoleDrawerClosesOneRun(t *testing.T) {
	h, sh := consoleTestHost(ConsoleData{Runs: []ConsoleRun{
		{ID: 3, Title: "fx doctor", Exited: true},
		{ID: 4, Title: "fx pr", Exited: true},
	}})
	var closed []uint64
	selected := []uint64{}
	h.Actions.CloseConsoleRun = func(id uint64) { closed = append(closed, id) }
	h.Actions.SelectConsole = func(id uint64) { selected = append(selected, id) }
	ts := consoleTester(&h, sh)
	if err := ts.Click("Close fx pr"); err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || closed[0] != 4 {
		t.Fatalf("closed = %v, want just run 4", closed)
	}
	// The ✕ closes; it must not also be read as "show me this run", or
	// closing an unselected tab would move the drawer onto a run that is
	// about to be gone.
	if len(selected) != 0 {
		t.Fatalf("closing a run also selected it: %v", selected)
	}
}

// An empty drawer says what belongs in it rather than showing a blank pane,
// and the ✕ hides the whole panel.
func TestConsoleDrawerSpeaksWhenEmptyAndCanBeHidden(t *testing.T) {
	h, sh := consoleTestHost(ConsoleData{})
	hidden := 0
	h.Actions.ToggleConsole = func() { hidden++ }
	ts := consoleTester(&h, sh)
	mustHave(t, ts, "No runs yet")
	// A run with no terminal behind it (the app never shows this, but the
	// projection can be the only thing a window has) says so too.
	h.Console = ConsoleData{Runs: []ConsoleRun{{ID: 1, Title: "fx doctor"}}}
	sh.ConsoleRun = 1
	ts.Frame()
	mustHave(t, ts, "This run has no output to show")
	if err := ts.Click("Hide console"); err != nil {
		t.Fatal(err)
	}
	if hidden != 1 {
		t.Fatalf("hidden = %d, want 1", hidden)
	}
}

// The hint is the one thing the drawer has to say about focus: an embedded
// terminal takes the keyboard on a click, so the running run says so and a
// finished one explains what a click is still good for.
func TestConsoleHintSaysWhatAClickWouldDo(t *testing.T) {
	d := ConsoleData{Runs: []ConsoleRun{{ID: 1}, {ID: 2, Exited: true}}}
	sh := &ShellState{ConsoleRun: 1}
	if got := consoleHint(d, sh); got != "running · click the console to type" {
		t.Fatalf("running hint = %q", got)
	}
	sh.ConsoleRun = 2
	if got := consoleHint(d, sh); got != "click the console to scroll it" {
		t.Fatalf("exited hint = %q", got)
	}
	sh.ConsoleRun = 99
	if got := consoleHint(d, sh); got != "" {
		t.Fatalf("no selection should say nothing, got %q", got)
	}
}

// The drawer is a panel in the window, not an overlay: closing it must remove
// it from the frame rather than leave it drawn behind something else.
func TestFrameOnlyDrawsTheConsoleWhenItIsOpen(t *testing.T) {
	h, sh := testHost()
	h.Console = ConsoleData{
		Runs: []ConsoleRun{{ID: 1, Title: "fx doctor", Exited: true}},
		Body: func(c *ui.Context) { ui.Text(c, "CONSOLE-BODY") },
	}
	sh.ConsoleOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "CONSOLE-BODY")

	sh.ConsoleOpen = false
	ts.Frame()
	mustNotHave(t, ts, "CONSOLE-BODY")
	mustHave(t, ts, "Console") // the way back stays in the status strip
}

// A window with no drawer — a command window, whose whole surface is already a
// terminal — must not advertise one, and must not draw one even if some stale
// state says it is open.
func TestCommandWindowHasNoConsoleDrawer(t *testing.T) {
	h, sh := testHost()
	h.Actions.ToggleConsole = nil
	h.Actions.SelectConsole = nil
	h.Actions.CloseConsoleRun = nil
	h.Console = ConsoleData{Runs: []ConsoleRun{{ID: 1, Title: "fx doctor"}},
		Body: func(c *ui.Context) { ui.Text(c, "CONSOLE-BODY") }}
	sh.ConsoleOpen = true
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustNotHave(t, ts, "CONSOLE-BODY")
	if !sh.ConsoleOpen {
		t.Fatal("the frame must not mutate state it cannot honour")
	}
}

// A run cannot be lost by hiding the drawer: the status strip keeps the way
// back to it and is the only place the runs are listed from outside.
func TestConsoleStatusEntryAppearsWithRunsAndOpensTheDrawer(t *testing.T) {
	h, _ := testHost()
	opened := 0
	h.Actions.ToggleConsole = func() { opened++ }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustNotHave(t, ts, "Console") // nothing to show, nothing to say

	h.Console = ConsoleData{Runs: []ConsoleRun{{ID: 1, Title: "fx doctor", Exited: true}}}
	ts.Frame()
	mustHave(t, ts, "Console")
	if err := ts.Click("Console"); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("opened = %d, want 1", opened)
	}
}

// The palette offers the drawer where there is one to open, and the row is
// what actually flips it.
func TestPaletteOffersToggleConsole(t *testing.T) {
	var cmd Command
	for _, candidate := range Commands {
		if candidate.ID == "toggle-console" {
			cmd = candidate
		}
	}
	if cmd.ID == "" {
		t.Fatal("the palette has no toggle-console row")
	}
	if cmd.Hint != "⌘J" {
		t.Fatalf("hint = %q, want the accelerator the menu binds", cmd.Hint)
	}
	if commandAvailable(Host{}, cmd) {
		t.Fatal("toggle-console is offered with nothing behind it")
	}
	if !commandAvailable(Host{Actions: Actions{ToggleConsole: noop}}, cmd) {
		t.Fatal("toggle-console is hidden where it would work")
	}

	sh := DefaultShell()
	toggled := 0
	h := Host{Shell: &sh, Actions: Actions{ToggleConsole: func() { toggled++ }}}
	runCommand(h, &sh, cmd)
	if toggled != 1 {
		t.Fatalf("the palette row did not touch the drawer: %d", toggled)
	}
}
