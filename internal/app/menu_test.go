package app

import (
	"testing"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

// The shell prints ⌘K / ⌘B / ⌘. / ⌘] in the command table and the menu.
// Those hints are only honest if a menu really binds them, so assert the
// bindings rather than trusting the labels.
func TestViewMenuBindsAdvertisedAccelerators(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	var got []string
	for _, it := range c.viewMenuItems() {
		if it.Accelerator != "" {
			got = append(got, it.ID+"="+it.Accelerator)
		}
	}
	want := []string{
		"toggle-sidebar=CmdOrCtrl+B",
		"command-palette=CmdOrCtrl+K",
		"session-interrupt=CmdOrCtrl+.",
		"focus-attention=CmdOrCtrl+]",
		"review-changes=CmdOrCtrl+R",
		// The console's tabs and the status strip print ⌘J, and the drawer
		// is the only place a window's runs are listed: the binding is what
		// makes hiding it safe.
		"toggle-console=CmdOrCtrl+J",
		// The palette advertises "⇧⌘V" for Paste Image, so the menu has to
		// bind it: a printed shortcut with nothing behind it is a lie.
		"paste-image=CmdOrCtrl+Shift+V",
	}
	if len(got) != len(want) {
		t.Fatalf("accelerators = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("accelerator %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// ⌘B and ⌘K act on the window the shortcut came from, falling back to the
// frontmost session when no window is identified.
func TestViewShortcutsTargetFocusedWindow(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &Session{coord: c, shell: chrome.DefaultShell()}
	c.sessions = []*Session{s}

	c.toggleSidebar(nilForTest())
	if s.shell.SidebarOpen {
		t.Fatal("toggle should collapse the rail")
	}
	c.toggleSidebar(nilForTest())
	if !s.shell.SidebarOpen {
		t.Fatal("toggle should reopen the rail")
	}
	c.openPalette(nilForTest())
	if !s.shell.PaletteOpen {
		t.Fatal("openPalette should open the palette")
	}
	// ...and from a clean slate: a stale search must not survive, whether the
	// palette is opened again by key or by the magnifier (chrome.OpenPalette).
	s.shell.PaletteQuery = "compact"
	s.shell.PaletteHighlight = 4
	s.shell.PaletteOpen = false
	c.openPalette(nilForTest())
	if s.shell.PaletteQuery != "" || s.shell.PaletteHighlight != -1 {
		t.Fatalf("page reopened on query %q highlight %d, want a clean palette",
			s.shell.PaletteQuery, s.shell.PaletteHighlight)
	}
}

// ⌘] must be safe when nothing wants the user and when no window is known.
func TestFocusAttentionIsSafeWithoutTargets(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.focusAttentionFocused(nilForTest())
	c.focusNextAttention(nil)
}

// With no window identified the shortcut falls back to the frontmost
// session instead of doing nothing — that is the case a test exercises,
// and the case a menu accelerator hits if the window is already gone.
func nilForTest() *mygo.Window { return nil }

// ⌘. must reach a session even when the accelerator carries no window
// (menu clicks report the window, but the fallback is what keeps the hint
// honest after the window is gone). A windowless session is skipped, the
// same rule shellFor applies: there is nothing to act on and nothing to
// repaint, and real sessions always carry a window.
func TestSessionForWindowSkipsWindowlessSessions(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &Session{coord: c, shell: chrome.DefaultShell()}
	c.addSession(s)
	if got := c.sessionForWindow(nil); got != nil {
		t.Fatalf("sessionForWindow(nil) = %v, want nil for a windowless session", got)
	}
	c.interruptFocused(nilForTest()) // must not panic with no chat and no terminal
}
