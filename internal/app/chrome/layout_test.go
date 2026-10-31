package chrome

import (
	"path/filepath"
	"testing"
)

func TestProjectSubtitleUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "Projects", "fx-desktop")
	got := ProjectSubtitle(path)
	want := "~/Projects/fx-desktop"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestProjectSubtitleOutsideHomeUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got := ProjectSubtitle("/var/tmp/work")
	if got != "/var/tmp/work" {
		t.Fatalf("got %q", got)
	}
}

func TestProjectSubtitleEmpty(t *testing.T) {
	if ProjectSubtitle("") != "" {
		t.Fatal("empty path")
	}
}

func TestDefaultShell(t *testing.T) {
	s := DefaultShell()
	if !s.SidebarOpen || s.SidebarWidth != float32(sidebarDefaultWide) {
		t.Fatalf("unexpected default shell: %+v", s)
	}
	if s.PaletteOpen || s.PaletteQuery != "" || s.CommitMsg != "" {
		t.Fatalf("transient state should start clean: %+v", s)
	}
}

func TestClampSidebarWidth(t *testing.T) {
	cases := []struct {
		name   string
		width  float32
		window float32
		want   float32
	}{
		{"default stays", float32(sidebarDefaultWide), 1280, float32(sidebarDefaultWide)},
		{"too narrow", 20, 1280, float32(sidebarMinWide)},
		{"too wide", 999, 1280, float32(sidebarMaxWide)},
		{"narrow window keeps workspace", 400, 720, 720 - mainMinWide - 1},
		{"unknown window uses default bounds", 999, 0, float32(sidebarMaxWide)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampSidebarWidth(tc.width, tc.window); got != tc.want {
				t.Fatalf("clampSidebarWidth(%v, %v) = %v, want %v", tc.width, tc.window, got, tc.want)
			}
		})
	}
}

func TestCommandsHaveUniqueIDsAndLabels(t *testing.T) {
	seenID := map[string]bool{}
	seenLabel := map[string]bool{}
	for _, cmd := range Commands {
		if cmd.ID == "" || cmd.Label == "" {
			t.Fatalf("command missing id or label: %+v", cmd)
		}
		if seenID[cmd.ID] {
			t.Fatalf("duplicate command id %q", cmd.ID)
		}
		if seenLabel[cmd.Label] {
			t.Fatalf("duplicate command label %q", cmd.Label)
		}
		seenID[cmd.ID], seenLabel[cmd.Label] = true, true
	}
	if len(Commands) < 10 {
		t.Fatalf("expected a useful palette, got %d commands", len(Commands))
	}
}

// The palette slices its groups at projectAgentCommands. A wrong constant
// would silently drop commands, so assert the boundary is real and that both
// groups are non-empty.
func TestCommandTableSplitsIntoTwoGroups(t *testing.T) {
	if projectAgentCommands <= 0 || projectAgentCommands >= len(Commands) {
		t.Fatalf("projectAgentCommands = %d out of range for %d commands", projectAgentCommands, len(Commands))
	}
	if Commands[0].ID != "open-folder" {
		t.Fatalf("first project command = %q", Commands[0].ID)
	}
	if Commands[projectAgentCommands].ID != "toggle-sidebar" {
		t.Fatalf("first view command = %q", Commands[projectAgentCommands].ID)
	}
}

// Every palette row must reach an action: a row that dispatches to nothing is
// a click that does nothing. This is the shape the removed slash-command group
// had (§A.6.3) — it could only ever appear in a window whose terminal runs a
// one-shot fx command, where typing /undo into stdin does nothing at all.
func TestEveryPaletteRowDispatches(t *testing.T) {
	for _, cmd := range Commands {
		sh := DefaultShell()
		sh.PaletteOpen = true
		ran, dispatched := "", false
		h := Host{Shell: &sh, Actions: Actions{RunCommand: func(id string) { dispatched, ran = true, id }}}
		runCommand(h, &sh, cmd)
		if sh.PaletteOpen {
			t.Fatalf("%s: running a command must close the palette", cmd.ID)
		}
		if cmd.ID == "toggle-sidebar" {
			continue // handled locally, before the dispatcher
		}
		if !dispatched || ran != cmd.ID {
			t.Fatalf("command %q dispatched to %q (dispatched=%v)", cmd.ID, ran, dispatched)
		}
	}
}

func TestRunCommandDispatchesByID(t *testing.T) {
	sh := DefaultShell()
	sh.PaletteOpen = true
	got := ""
	h := Host{Shell: &sh, Actions: Actions{RunCommand: func(id string) { got = id }}}
	runCommand(h, &sh, Command{ID: "run-doctor"})
	if got != "run-doctor" {
		t.Fatalf("expected dispatch by id, got %q", got)
	}
	if sh.PaletteOpen {
		t.Fatal("dispatch should close the palette")
	}
	called := false
	h.Actions = Actions{OpenPreferences: func() { called = true }}
	runCommand(h, &sh, Command{ID: "preferences"})
	if !called {
		t.Fatal("picker/setup palette must dispatch shared preferences without a session")
	}

}

func TestToggleSidebarCommandFlipsState(t *testing.T) {
	sh := DefaultShell()
	h := Host{Shell: &sh}
	runCommand(h, &sh, Command{ID: "toggle-sidebar"})
	if sh.SidebarOpen {
		t.Fatal("toggle-sidebar should collapse the rail")
	}
	runCommand(h, &sh, Command{ID: "toggle-sidebar"})
	if !sh.SidebarOpen {
		t.Fatal("toggle-sidebar should reopen the rail")
	}
}

func TestAgentZeroValueHidesBar(t *testing.T) {
	var a Agent
	if a.Status != "" || a.Model != "" {
		t.Fatal("zero agent should hide the strip")
	}
}

func TestItoa(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "0"}, {7, "7"}, {42, "42"}, {1284, "1284"},
	} {
		if got := itoa(tc.in); got != tc.want {
			t.Fatalf("itoa(%d) = %q want %q", tc.in, got, tc.want)
		}
	}
}
