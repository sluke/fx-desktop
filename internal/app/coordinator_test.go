package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fx-desktop/internal/testutil"
)

func TestNewCoordinatorLoadsOrDefaults(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	if c.projects == nil {
		t.Fatal("projects store must exist even when the file is missing")
	}
	if c.prefs.FontSize <= 0 {
		t.Fatalf("font size should default: %+v", c.prefs)
	}
	if c.fxPath != "" || len(c.sessions) != 0 {
		t.Fatalf("unexpected initial state: %+v", c)
	}
}

func TestRefreshFxFromOverride(t *testing.T) {
	home := testutil.IsolateHome(t)
	path := filepath.Join(home, "fx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 0.0.13\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_DESKTOP_FX_PATH", path)

	c := NewCoordinator()
	c.refreshFx()
	if got := c.fxPathSnapshot(); got != path {
		t.Fatalf("fxPath = %q want %q", got, path)
	}
	waitForFx(t, func() bool { return c.fxVersionSnapshot() == "fx v0.0.13" })
}

func waitForFx(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for fx query")
		case <-tick.C:
		}
	}
}

// The executable is enough to open a project. Metadata must not delay
// Launch/OpenProject, or hold the coordinator lock while the CLI waits.
func TestRefreshFxDoesNotWaitForVersion(t *testing.T) {
	home := testutil.IsolateHome(t)
	path := filepath.Join(home, "fx")
	gate, started := filepath.Join(home, "release"), filepath.Join(home, "started")
	t.Setenv("FX_TEST_GATE", gate)
	t.Setenv("FX_TEST_STARTED", started)
	script := "#!/bin/sh\nprintf started > \"$FX_TEST_STARTED\"\nwhile [ ! -f \"$FX_TEST_GATE\" ]; do /bin/sleep 0.01; done\necho 0.0.13\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_DESKTOP_FX_PATH", path)
	defer os.WriteFile(gate, nil, 0o600)
	c := NewCoordinator()
	returned := make(chan struct{})
	go func() { c.refreshFx(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("refreshFx blocked on the version query")
	}
	waitForFx(t, func() bool { _, err := os.Stat(started); return err == nil })
	if c.fxPathSnapshot() != path || c.fxVersionSnapshot() != "" {
		t.Fatal("executable must be available before its version")
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFx(t, func() bool { return c.fxVersionSnapshot() == "fx v0.0.13" })
}

func TestRefreshFxVersionRejectsSupersededQuery(t *testing.T) {
	home := testutil.IsolateHome(t)
	path := filepath.Join(home, "fx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := NewCoordinator()
	c.fxPath, c.fxVersion, c.fxGeneration = path, "fx vnew", 2
	if c.refreshFxVersion(path, 1) {
		t.Fatal("superseded query was published")
	}
	if got := c.fxVersionSnapshot(); got != "fx vnew" {
		t.Fatalf("stale query overwrote the new version: %q", got)
	}
}

func TestRefreshFxWithBadOverride(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("FX_DESKTOP_FX_PATH", filepath.Join(t.TempDir(), "nope"))

	c := NewCoordinator()
	c.refreshFx()
	if c.fxPath != "" || c.fxVersion != "" {
		t.Fatalf("a missing override should clear both: %q %q", c.fxPath, c.fxVersion)
	}
}

// A truncated or otherwise unusable fx used to look installed: the file
// existed, so the setup screen stayed hidden and every session then failed
// against a binary the OS would not even start. Such a binary has to be
// treated as not installed so the user is offered a fresh download.
func TestRefreshFxTreatsUnrunnableBinaryAsNotInstalled(t *testing.T) {
	home := testutil.IsolateHome(t)
	path := filepath.Join(home, "fx")
	// Present and executable, but it fails when run.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_DESKTOP_FX_PATH", path)

	c := NewCoordinator()
	c.refreshFx()
	waitForFx(t, func() bool { return c.fxBrokenSnapshot() })
	if got := c.fxPathSnapshot(); got != "" {
		t.Fatalf("an unrunnable fx must not stay resolvable: %q", got)
	}
	if got := c.fxVersionSnapshot(); got != "" {
		t.Fatalf("an unrunnable fx must not publish a version: %q", got)
	}
}

// A working binary must clear the broken flag, so a repaired install is not
// still treated as broken.
func TestRefreshFxClearsBrokenFlagWhenBinaryRuns(t *testing.T) {
	home := testutil.IsolateHome(t)
	path := filepath.Join(home, "fx")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 0.0.13\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_DESKTOP_FX_PATH", path)

	c := NewCoordinator()
	c.fxBroken = true
	c.refreshFx()
	waitForFx(t, func() bool { return c.fxVersionSnapshot() == "fx v0.0.13" })
	if c.fxBrokenSnapshot() {
		t.Fatal("a working fx must clear the broken flag")
	}
}

// These paths would normally open a window or a dialog; with no windows and
// no arguments they must simply do nothing rather than panic.
func TestHandleSecondInstanceWithoutTargetsIsSafe(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.HandleSecondInstance(nil, t.TempDir())
	c.HandleSecondInstance([]string{"", "--flag"}, t.TempDir())
}

func TestRemoveSession(t *testing.T) {
	c := NewCoordinator()
	a, b := &ProjectWindow{project: "/a"}, &ProjectWindow{project: "/b"}
	c.sessions = []*ProjectWindow{a, b}
	c.removeSession(a)
	if len(c.sessions) != 1 || c.sessions[0] != b {
		t.Fatalf("sessions: %v", c.sessions)
	}
	c.removeSession(&ProjectWindow{}) // unknown session: no-op
	if len(c.sessions) != 1 {
		t.Fatalf("sessions: %v", c.sessions)
	}
}

func TestFxVersionLabel(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"0.0.13", "fx v0.0.13"},
		{"v0.0.13", "fx v0.0.13"},
		{"fx v0.0.13", "fx v0.0.13"},
		{"fx 0.0.13", "fx 0.0.13"},
		{"  0.1.0  ", "fx v0.1.0"},
		{"", ""},
	} {
		if got := fxVersionLabel(tc.in); got != tc.want {
			t.Errorf("fxVersionLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Two fx processes sharing one workspace would both rewrite the same
// tree, so reopening a project must focus the window that owns it.
func TestSessionForFindsOwningSession(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	a := &ProjectWindow{project: "/p/one"}
	b := &ProjectWindow{project: "/p/two"}
	c.addSession(a)
	c.addSession(b)
	if got := c.sessionFor("/p/two"); got != b {
		t.Fatalf("sessionFor(/p/two) = %v", got)
	}
	if got := c.sessionFor("/p/none"); got != nil {
		t.Fatalf("sessionFor(/p/none) = %v, want nil", got)
	}
	if c.sessionCount() != 2 {
		t.Fatalf("sessionCount = %d", c.sessionCount())
	}
	c.removeSession(a)
	if c.sessionCount() != 1 {
		t.Fatalf("after remove: %d", c.sessionCount())
	}
}

// A window can close while another caller walks the session list. The
// snapshot must be a copy, so a concurrent remove cannot reassign the
// slice the walker is iterating.
func TestSessionsSnapshotIsACopy(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	a := &ProjectWindow{project: "/p/one"}
	b := &ProjectWindow{project: "/p/two"}
	c.addSession(a)
	c.addSession(b)

	snap := c.sessionsSnapshot()
	if len(snap) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(snap))
	}
	// Mutating what was handed out must not reach the coordinator.
	snap[0] = nil
	c.removeSession(a)
	if got := c.sessionsSnapshot(); len(got) != 1 || got[0] != b {
		t.Fatalf("coordinator list = %v, want [%v]", got, b)
	}
	if c.sessionCount() != 1 {
		t.Fatalf("sessionCount = %d, want 1", c.sessionCount())
	}
}
