package chrome

import (
	"github.com/egoist/mygo/ui"
)

// ConsoleRun is one fx command run (fx doctor, fx pr, fx login …) as the
// console drawer lists it (§4.7).
//
// A run outlives its process: the tab keeps the output and the exit code, so
// "what did doctor say" is still answerable an hour later. That is the whole
// difference between a console and a terminal window that closes on exit.
type ConsoleRun struct {
	// ID identifies the tab, so a click can select this run rather than
	// whichever one happens to be on screen.
	ID uint64
	// Title is what the tab is called: the command fx was asked to run.
	Title string
	// Command is the exact invocation, for the tab's tooltip.
	Command string
	// Exited is true once the process ended; Exit is its code then. A code
	// is not a guess: it is what the process reported.
	Exited bool
	Exit   int
}

// ConsoleData is the console drawer: the runs this window has started, and how
// to draw the one on screen.
type ConsoleData struct {
	// Runs are the runs in the order they started.
	Runs []ConsoleRun
	// Body draws the active run's terminal. The app owns the terminal and
	// chrome never reaches into it — and a test can hand in a stand-in.
	Body func(*ui.Context)
}

const (
	// consoleHeight is the drawer's height. A terminal needs rows to be
	// worth showing; this is about a dozen at the app's font size, and ⌘J
	// gives the room back to the conversation. The shell's divider drags
	// columns, not rows, so this is fixed rather than a grip that would not
	// move.
	consoleHeight    = 268
	consoleTabHeight = 32
)

// consoleDrawer is the run log: one tab per run over one terminal. It sits at
// the bottom of the window, below the work it belongs to, and shows only where
// a window has runs to show.
func consoleDrawer(c *ui.Context, h Host, sh *ShellState, p Palette) {
	d := h.Console
	ui.Column(c).Shrink(0).Height(consoleHeight).FillWidth().Background(p.Surface).
		BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).
		Children(func() {
			consoleTabs(c, h, sh, p)
			ui.Box(c).Grow(1).MinHeight(0).FillWidth().Background(p.TermBG).Children(func() {
				switch {
				case len(d.Runs) == 0:
					emptyState(c, p, IconTerminal, "No runs yet · fx doctor, fx pr and fx login land here")
				case d.Body != nil:
					d.Body(c)
				default:
					emptyState(c, p, IconTerminal, "This run has no output to show")
				}
			})
		})
}

func consoleTabs(c *ui.Context, h Host, sh *ShellState, p Palette) {
	d := h.Console
	ui.Row(c).Shrink(0).FillWidth().Height(consoleTabHeight).AlignItems(ui.Center).Gap(8).
		Padding(0, 8).Background(p.Surface).
		BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).
		Children(func() {
			// Runs scroll rather than shrink: a tab showing three letters of
			// a command name names nothing.
			ui.ScrollHorizontal(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				for _, run := range d.Runs {
					consoleTab(c, h, p, run, run.ID == sh.ConsoleRun)
				}
			})
			// A run's state is on its own tab (coloured, and named), so the
			// tab strip needs no sentence about what a click would do.
			if h.Actions.ToggleConsole != nil {
				ui.Button(c, "✕").Label("Hide console").Tooltip("Hide console (⌘J)").
					FontSize(11).BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).
					Padding(2, 6).OnClick(h.Actions.ToggleConsole)
			}
		})
}

func consoleTab(c *ui.Context, h Host, p Palette, run ConsoleRun, active bool) {
	bg := p.Surface
	if active {
		bg = p.SurfaceHi
	}
	c.Key(run.ID)
	tab := ui.ButtonBase(c).Label(run.Title).Tooltip(consoleRunTooltip(run)).
		Padding(4, 8).Radius(controlRadius).AlignItems(ui.Center).Gap(6).Shrink(0).Background(bg)
	if h.Actions.SelectConsole != nil {
		tab.OnClick(func() { h.Actions.SelectConsole(run.ID) })
	}
	tab.Children(func() {
		mono(ui.Text(c, run.Title)).FontSize(10).TextColor(p.InkStrong).SingleLine()
		mono(ui.Text(c, consoleRunState(run))).FontSize(9).TextColor(consoleRunColor(run, p)).Shrink(0)
		if h.Actions.CloseConsoleRun != nil {
			ui.Button(c, "✕").Label("Close "+run.Title).Tooltip("Close this run").
				FontSize(10).Padding(2, 4).BorderWidth(0).Background(bg).TextColor(p.InkMuted).
				OnClick(func() { h.Actions.CloseConsoleRun(run.ID) })
		}
	})
}

// consoleRunState is what the tab says about the run: what it is doing, or how
// it ended. The exit code is printed for a clean run too — "it worked" and "it
// worked, with code 0" are the same fact, and one of them is checkable.
func consoleRunState(run ConsoleRun) string {
	if !run.Exited {
		return "running"
	}
	return "exited " + itoa(run.Exit)
}

func consoleRunColor(run ConsoleRun, p Palette) ui.Color {
	switch {
	case !run.Exited:
		return p.InkMuted
	case run.Exit != 0:
		return p.Danger
	default:
		return p.OK
	}
}

func consoleRunTooltip(run ConsoleRun) string {
	if run.Command == "" {
		return run.Title + " · " + consoleRunState(run)
	}
	return run.Command + " · " + consoleRunState(run)
}

// consoleRunning reports whether any run is still going, which is the one
// thing about a hidden console worth keeping in the status strip: a run the
// user cannot see is still a run.
func consoleRunning(d ConsoleData) bool {
	for _, run := range d.Runs {
		if !run.Exited {
			return true
		}
	}
	return false
}
