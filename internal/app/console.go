package app

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/app/chrome"
)

// consoleRun is one command fx run shown in a window's console drawer (§4.7).
// It owns a terminal exactly as a command window does; what this step changed
// is where that terminal is shown — under the conversation that asked for the
// run, rather than in a window of its own.
//
// Every field is touched on the app loop only: the terminal's own goroutine
// reaches them by asking the app to run the change for it.
type consoleRun struct {
	id      uint64
	title   string
	command string
	term    *terminal.Terminal
	exited  bool
	exit    int
	// removed is set when the tab was closed. The process may still report
	// its end afterwards, and a closed run has nothing left to update.
	removed bool
	// done runs on the app loop when the process ends. A run that changes
	// fx's own state — fx login — uses it to re-read that state, so the
	// sign-in notice clears without the user doing anything.
	done func(code int)
}

// consoleRunLimit bounds the tabs a window keeps. A console is a log, not a
// workspace: eight runs are more than the drawer shows at once, and each
// terminal keeps its own scrollback (§4.10.5).
const consoleRunLimit = 8

// attachConsoleRun adds a run to the window's drawer, makes it the one on
// screen and shows the drawer: a run the user just started must be visible,
// and that is the whole affordance for finding the console at all. It is where
// a run is born, so the tests use it too — a PTY has no business in a test
// process, and the bookkeeping is what needs proving.
func (s *Session) attachConsoleRun(run *consoleRun) *consoleRun {
	s.consoleSeq++
	run.id = s.consoleSeq
	s.consoles = append(s.consoles, run)
	s.shell.ConsoleOpen = true
	s.shell.ConsoleRun = run.id
	s.trimConsoles()
	return run
}

// trimConsoles drops the oldest finished run while the drawer holds more than
// its limit. A run that is still working is never closed for space: when every
// tab is live the drawer keeps them all, because closing one would kill work
// the user started.
func (s *Session) trimConsoles() {
	for len(s.consoles) > consoleRunLimit {
		drop := -1
		for i, run := range s.consoles {
			if run.exited {
				drop = i
				break
			}
		}
		if drop < 0 {
			return
		}
		run := s.consoles[drop]
		run.removed = true
		if run.term != nil {
			run.term.Close()
		}
		s.consoles = append(s.consoles[:drop], s.consoles[drop+1:]...)
	}
}

// activeConsole is the run the drawer draws, or nil when the selection points
// at nothing (a closed tab, before the first run).
func (s *Session) activeConsole() *consoleRun {
	for _, run := range s.consoles {
		if run.id == s.shell.ConsoleRun {
			return run
		}
	}
	return nil
}

// closeConsoles ends every run this window holds. A run's process belongs to
// the window that showed it: closing the window closes the runs, as closing a
// command window always did.
func (s *Session) closeConsoles() {
	for _, run := range s.consoles {
		run.removed = true
		if run.term != nil {
			run.term.Close()
		}
	}
	s.consoles = nil
}

// consoleIn runs a command fx process in the window's console drawer.
//
// A window with no workspace — the picker, the start page, or the fx menu
// acting on nothing — has no drawer to put it in, so it opens the command
// window it always did. The drawer is an improvement where it fits, not a
// promise the app breaks where it does not (§4.7).
func (c *Coordinator) consoleIn(s *Session, title string, args ...string) {
	if s == nil || s.project == "" {
		c.openCommandSession(title, "", args...)
		return
	}
	if _, err := c.startConsoleRun(s, title, args...); err != nil {
		// fx itself could not start. Say so here rather than opening a
		// drawer with a dead terminal in it.
		mygo.Dialog.Error(title, err.Error())
		return
	}
	s.invalidate()
}

// startConsoleRun builds one run and its terminal, and hands the run to the
// drawer. The run is made before the terminal so the exit callback can name
// it: a process that reports its end immediately must still find its run.
func (c *Coordinator) startConsoleRun(s *Session, title string, args ...string) (*consoleRun, error) {
	cmd := append([]string{c.fxPathSnapshot()}, args...)
	run := &consoleRun{title: title, command: strings.Join(cmd, " ")}
	if len(args) > 0 && args[0] == "login" {
		// Signing in changes what fx reports about being signed in, and no
		// ACP message says so. Re-reading it here is what makes the
		// sign-in notice go away by itself.
		run.done = func(code int) {
			if code == 0 {
				go s.refreshFxInfo()
			}
		}
	}
	light, dark := terminalThemes()
	term, err := terminal.New(terminal.Options{
		Command:   cmd,
		Font:      terminal.Font{Family: c.prefs.FontFamily, Size: float32(c.prefs.FontSize)},
		Theme:     light,
		DarkTheme: dark,
		// `fx doctor`, `fx pr` and friends act on a repository, so the run
		// starts where the conversation is.
		Dir: s.project,
		// OnExit runs on the terminal's own goroutine: hand the fact to the
		// app loop, where the run and the window may be touched.
		OnExit: func(code int) {
			mygo.App.WhenReady(func() { c.finishConsoleRun(s, run, code) })
		},
	})
	if err != nil {
		return nil, err
	}
	run.term = term
	return s.attachConsoleRun(run), nil
}

// finishConsoleRun records a run's end. The tab stays: the output and the exit
// code are the point of a console, and a window that vanished on exit could
// never be read twice.
func (c *Coordinator) finishConsoleRun(s *Session, run *consoleRun, code int) {
	if run.removed {
		return
	}
	run.exited = true
	run.exit = code
	if run.done != nil {
		run.done(code)
	}
	s.invalidate()
}

// sessionConsole projects the drawer's state for chrome. The terminal itself
// stays here: the projection carries a closure that draws it, never a handle.
func (c *Coordinator) sessionConsole(s *Session) chrome.ConsoleData {
	d := chrome.ConsoleData{Runs: make([]chrome.ConsoleRun, 0, len(s.consoles))}
	for _, run := range s.consoles {
		d.Runs = append(d.Runs, chrome.ConsoleRun{
			ID:      run.id,
			Title:   run.title,
			Command: run.command,
			Exited:  run.exited,
			Exit:    run.exit,
		})
	}
	if run := s.activeConsole(); run != nil && run.term != nil {
		// The key follows the run, so switching tabs builds a new element
		// rather than re-binding the one that is there: two terminals are
		// two views, each with state of its own.
		d.Body = func(ctx *ui.Context) {
			ctx.Key(run.id)
			terminal.View(ctx, run.term).Fill()
		}
	}
	return d
}

// toggleConsole shows or hides the drawer. Hiding it keeps the runs: the tabs
// and their output are what the drawer is for, and ⌘J is how the conversation
// gets the room back.
func (c *Coordinator) toggleConsole(s *Session) {
	s.shell.ConsoleOpen = !s.shell.ConsoleOpen
	s.invalidate()
}

// selectConsole makes a run the one on screen, and shows the drawer: a click
// on a tab cannot leave the panel hidden behind it.
func (c *Coordinator) selectConsole(s *Session, id uint64) {
	s.shell.ConsoleRun = id
	s.shell.ConsoleOpen = true
	s.invalidate()
}

// closeConsoleRun ends one run and drops its tab, killing a process that is
// still going: closing the tab is the same act as closing the command window
// the run used to have.
func (c *Coordinator) closeConsoleRun(s *Session, id uint64) {
	for i, run := range s.consoles {
		if run.id != id {
			continue
		}
		run.removed = true
		if run.term != nil {
			run.term.Close()
		}
		s.consoles = append(s.consoles[:i], s.consoles[i+1:]...)
		if s.shell.ConsoleRun == id {
			// Fall to the run that took its place, or to the one before it.
			s.shell.ConsoleRun = 0
			switch {
			case i < len(s.consoles):
				s.shell.ConsoleRun = s.consoles[i].id
			case i > 0:
				s.shell.ConsoleRun = s.consoles[i-1].id
			}
		}
		if len(s.consoles) == 0 {
			s.shell.ConsoleOpen = false
		}
		s.invalidate()
		return
	}
}

// runDoctorFocused runs fx doctor where the focused window can show it: in the
// console drawer of a project window, or in a command window of its own when
// there is no project window to hang a drawer on.
func (c *Coordinator) runDoctorFocused(w *mygo.Window) {
	if s := c.sessionForWindow(w); s != nil && s.project != "" {
		c.consoleIn(s, "fx doctor", "doctor")
		return
	}
	c.openCommandSession("fx doctor", "", "doctor")
}

// toggleConsoleFocused flips the drawer on the focused project window. A
// command window has no drawer, so it does nothing there rather than opening
// one it cannot fill.
func (c *Coordinator) toggleConsoleFocused(w *mygo.Window) {
	if s := c.sessionForWindow(w); s != nil && s.project != "" {
		c.toggleConsole(s)
	}
}
