package app

import (
	"testing"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

func consoleSession() *ProjectWindow {
	s := &ProjectWindow{shell: chrome.DefaultShell()}
	s.console.window = s
	return s
}

// A run that starts must be visible: starting one is the whole affordance for
// finding the console at all, and the newest run is the one the user means.
func TestAttachConsoleRunShowsTheDrawerAndSelectsIt(t *testing.T) {
	s := consoleSession()
	first := s.console.attach(&consoleRun{title: "fx doctor"})
	second := s.console.attach(&consoleRun{title: "fx credits"})
	if !s.shell.ConsoleOpen {
		t.Fatal("starting a run should open the drawer")
	}
	if s.shell.ConsoleRun != second.id {
		t.Fatalf("the new run should be on screen, got %d", s.shell.ConsoleRun)
	}
	if first.id == second.id {
		t.Fatal("runs need ids of their own")
	}
	if got := s.console.active(); got != second {
		t.Fatalf("activeConsole = %v, want the newest run", got)
	}
}

// The projection is data only: the terminal stays in the app, and a run that
// has no terminal behind it — which a test fixture is, and which the app never
// shows — has no body to draw rather than a panic waiting to happen.
func TestSessionConsoleProjectsEveryRunInOrder(t *testing.T) {
	testutil.IsolateHome(t)
	coord := NewCoordinator()
	s := consoleSession()
	s.console.attach(&consoleRun{title: "fx doctor", command: "fx doctor", exited: true, exit: 2})
	s.console.attach(&consoleRun{title: "fx pr", command: "fx pr"})

	d := coord.sessionConsole(s)
	if len(d.Runs) != 2 {
		t.Fatalf("runs = %+v", d.Runs)
	}
	if d.Runs[0].Title != "fx doctor" || d.Runs[0].Command != "fx doctor" || !d.Runs[0].Exited || d.Runs[0].Exit != 2 {
		t.Fatalf("first run = %+v", d.Runs[0])
	}
	if d.Runs[1].Title != "fx pr" || d.Runs[1].Exited {
		t.Fatalf("second run = %+v", d.Runs[1])
	}
	if d.Runs[0].ID == d.Runs[1].ID {
		t.Fatal("the projection must keep the ids the actions take")
	}
	if d.Body != nil {
		t.Fatal("a run with no terminal has no body to draw")
	}
}

// The exit code is the point of keeping a tab after the process: it has to be
// recorded, and a run whose tab was closed has nothing left to update.
func TestFinishConsoleRunRecordsTheExitCode(t *testing.T) {
	testutil.IsolateHome(t)
	coord := NewCoordinator()
	s := consoleSession()
	run := s.console.attach(&consoleRun{title: "fx doctor"})
	var told int
	run.done = func(code int) { told = code }

	coord.finishConsoleRun(s, run, 2)
	if !run.exited || run.exit != 2 {
		t.Fatalf("run = %+v, want exited with 2", run)
	}
	if told != 2 {
		t.Fatalf("done was told %d, want 2", told)
	}

	closed := s.console.attach(&consoleRun{title: "fx login"})
	closed.removed = true
	called := false
	closed.done = func(int) { called = true }
	coord.finishConsoleRun(s, closed, 0)
	if closed.exited || called {
		t.Fatal("a closed run must not be updated when its process reports back")
	}
}

// Hiding the drawer keeps the runs: they are what the drawer is for, and ⌘J
// has to be a view toggle rather than a delete key.
func TestToggleConsoleKeepsTheRuns(t *testing.T) {
	testutil.IsolateHome(t)
	coord := NewCoordinator()
	s := consoleSession()
	s.console.attach(&consoleRun{title: "fx doctor"})

	coord.toggleConsole(s)
	if s.shell.ConsoleOpen {
		t.Fatal("toggle-console should hide the drawer")
	}
	if len(s.console.runs) != 1 {
		t.Fatalf("runs = %d, want the run to survive hiding", len(s.console.runs))
	}
	coord.toggleConsole(s)
	if !s.shell.ConsoleOpen {
		t.Fatal("toggle-console should show the drawer again")
	}
}

// Closing a tab ends one run and hands the drawer to a neighbour; closing the
// last one closes the drawer rather than leaving an empty panel behind.
func TestCloseConsoleRunFallsToANeighbourThenCloses(t *testing.T) {
	testutil.IsolateHome(t)
	coord := NewCoordinator()
	s := consoleSession()
	r1 := s.console.attach(&consoleRun{title: "one"})
	r2 := s.console.attach(&consoleRun{title: "two"})
	r3 := s.console.attach(&consoleRun{title: "three"})

	coord.closeConsoleRun(s, r2.id)
	if !r2.removed {
		t.Fatal("a closed run must be marked so its exit is ignored")
	}
	if s.shell.ConsoleRun != r3.id {
		t.Fatalf("selection = %d, want the run that took its place", s.shell.ConsoleRun)
	}
	if len(s.console.runs) != 2 || s.console.runs[0] != r1 || s.console.runs[1] != r3 {
		t.Fatalf("runs = %+v", s.console.runs)
	}

	coord.closeConsoleRun(s, r3.id)
	if s.shell.ConsoleRun != r1.id {
		t.Fatalf("selection = %d, want the run before it", s.shell.ConsoleRun)
	}

	coord.closeConsoleRun(s, r1.id)
	if len(s.console.runs) != 0 || s.shell.ConsoleOpen {
		t.Fatalf("an emptied drawer should close itself: runs=%d open=%v", len(s.console.runs), s.shell.ConsoleOpen)
	}
}

// Clicking a tab must show the drawer: a selected run behind a hidden panel is
// a click that did nothing.
func TestSelectConsoleShowsTheDrawer(t *testing.T) {
	testutil.IsolateHome(t)
	coord := NewCoordinator()
	s := consoleSession()
	r1 := s.console.attach(&consoleRun{title: "one"})
	s.console.attach(&consoleRun{title: "two"})
	coord.toggleConsole(s)

	coord.selectConsole(s, r1.id)
	if s.shell.ConsoleRun != r1.id || !s.shell.ConsoleOpen {
		t.Fatalf("selection = %d open=%v", s.shell.ConsoleRun, s.shell.ConsoleOpen)
	}
}

// The drawer belongs to a window with a workspace. A command window's whole
// surface is already a terminal, so it is offered no way to open one — while
// signing in still works there, in a window of its own.
func TestSessionActionsWireTheConsoleOnlyForAProject(t *testing.T) {
	testutil.IsolateHome(t)
	coord := NewCoordinator()
	project := coord.sessionActions(&ProjectWindow{project: "/p", shell: chrome.DefaultShell()})
	if project.ToggleConsole == nil || project.SelectConsole == nil || project.CloseConsoleRun == nil {
		t.Fatalf("a project window needs the console: %+v", project)
	}
	if project.SignIn == nil {
		t.Fatal("a project window signs in through the console")
	}

	command := coord.sessionActions(&ProjectWindow{shell: chrome.DefaultShell()})
	if command.ToggleConsole != nil || command.SelectConsole != nil || command.CloseConsoleRun != nil {
		t.Fatalf("a command window has no drawer to offer: %+v", command)
	}
	if command.SignIn == nil {
		t.Fatal("sign-in still works without a project, in a window of its own")
	}
}

// The console keeps a bounded number of tabs, but space is never a reason to
// close a run that is still working: only finished runs are trimmed, oldest
// first (§4.10.5).
func TestConsoleTabsStayBoundedWithoutKillingRunningRuns(t *testing.T) {
	s := consoleSession()
	for i := 0; i < 12; i++ {
		s.console.attach(&consoleRun{title: "run"})
	}
	if len(s.console.runs) != 12 {
		t.Fatalf("dropped a running run: %d tabs", len(s.console.runs))
	}
	for _, run := range s.console.runs {
		run.exited = true
	}
	newest := s.console.attach(&consoleRun{title: "fresh"})
	if len(s.console.runs) != consoleRunLimit {
		t.Fatalf("drawer holds %d runs; want %d", len(s.console.runs), consoleRunLimit)
	}
	kept := make(map[uint64]bool, len(s.console.runs))
	for _, run := range s.console.runs {
		kept[run.id] = true
	}
	if !kept[newest.id] {
		t.Fatal("the run that just started was trimmed")
	}
	if kept[1] {
		t.Fatal("the oldest finished run was kept")
	}
	if s.shell.ConsoleRun != newest.id {
		t.Fatalf("active run = %d; want %d", s.shell.ConsoleRun, newest.id)
	}
}
