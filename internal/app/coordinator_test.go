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

// One folder reached under two spellings is one project window. Compared as
// strings, the second spelling opened a second window and a second fx on the
// same workspace.
func TestRegressionR9_SessionForMatchesTheSameFolderUnderAnotherSpelling(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "Proj")
	other := filepath.Join(base, "other")
	for _, dir := range []string{real, other} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A window records its folder's identity when it opens.
	opened := func(project string) *ProjectWindow {
		info, err := os.Stat(project)
		if err != nil {
			t.Fatal(err)
		}
		return &ProjectWindow{project: project, projectInfo: info}
	}
	window := opened(real)
	c := &Coordinator{sessions: []*ProjectWindow{window, opened(other)}}
	if got := c.sessionFor(real); got != window {
		t.Fatal("the exact path no longer finds its window")
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip("symbolic links are not available:", err)
	}
	if got := c.sessionFor(alias); got != window {
		t.Errorf("a symbolic link to an open project did not find its window: %v", got)
	}
	if got := c.sessionFor(filepath.Join(base, "missing")); got != nil {
		t.Errorf("a folder that does not exist matched a window: %v", got.project)
	}
	if got := c.sessionFor(filepath.Join(base, "other")); got == nil || got.project != other {
		t.Errorf("a different folder matched the wrong window: %v", got)
	}
	lower := filepath.Join(base, "proj")
	if a, errA := os.Stat(real); errA == nil {
		if b, errB := os.Stat(lower); errB == nil && os.SameFile(a, b) {
			if got := c.sessionFor(lower); got != window {
				t.Errorf("another letter case of an open project did not find its window")
			}
		}
	}

	// A window whose identity is unknown is matched by its path alone, and
	// its folder is not read again: the lookup must not depend on a volume
	// that may have stopped answering since the window opened.
	plain := &ProjectWindow{project: real}
	c = &Coordinator{sessions: []*ProjectWindow{plain}}
	if got := c.sessionFor(real); got != plain {
		t.Error("a window without a recorded identity is no longer found by its path")
	}
	if got := c.sessionFor(alias); got != nil {
		t.Error("a window without a recorded identity was matched through the filesystem")
	}
}

// A damaged fx found first on PATH used to hide a working managed install:
// the app declared fx broken, and a successful repair changed nothing. The
// version query now records the path and resolution goes on to the managed
// binary.
func TestRegressionR9_DamagedFxOnPathFallsBackToManagedInstall(t *testing.T) {
	home := testutil.IsolateHome(t)
	pathDir := filepath.Join(t.TempDir(), "pathbin")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	onPath := filepath.Join(pathDir, "fx")
	if err := os.WriteFile(onPath, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	managed := filepath.Join(home, ".local", "bin", "fx")
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\necho 0.0.17\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := NewCoordinator()
	c.refreshFx()
	if got := c.fxPathSnapshot(); got != onPath {
		t.Fatalf("first resolution = %q, want the PATH binary %q", got, onPath)
	}
	waitForFx(t, func() bool { return c.fxUnrunnableSnapshot() == onPath })
	if c.fxBrokenSnapshot() {
		t.Fatal("a working managed install exists; fx must not be reported broken")
	}
	// The retry is posted to the app loop. A test has no loop, so the step
	// it would take is taken here.
	c.refreshFx()
	if got := c.fxPathSnapshot(); got != managed {
		t.Fatalf("after the retry fxPath = %q, want the managed install %q", got, managed)
	}
	waitForFx(t, func() bool { return c.fxVersionSnapshot() == "fx v0.0.17" })
	if c.fxBrokenSnapshot() {
		t.Error("the managed install answered, but fx is reported broken")
	}
	if got := c.fxUnrunnableList(); len(got) != 1 || got[0] != onPath {
		t.Errorf("unrunnable paths = %v, want only the PATH binary", got)
	}
}

// With every candidate damaged the app ends in the broken state, names the
// last binary it tried, and does not hand the retry back and forth.
func TestRegressionR9_TwoDamagedFxBinariesEndBroken(t *testing.T) {
	home := testutil.IsolateHome(t)
	pathDir := filepath.Join(t.TempDir(), "pathbin")
	managed := filepath.Join(home, ".local", "bin", "fx")
	onPath := filepath.Join(pathDir, "fx")
	for _, file := range []string{onPath, managed} {
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", pathDir)

	c := NewCoordinator()
	c.refreshFx()
	waitForFx(t, func() bool { return c.fxUnrunnableSnapshot() == onPath })
	c.refreshFx() // the posted retry
	if got := c.fxPathSnapshot(); got != managed {
		t.Fatalf("the retry resolved %q, want the managed install %q", got, managed)
	}
	waitForFx(t, func() bool { return c.fxBrokenSnapshot() })
	if got := c.fxPathSnapshot(); got != "" {
		t.Errorf("fxPath = %q after every candidate failed", got)
	}
	if got := c.fxUnrunnableSnapshot(); got != managed {
		t.Errorf("the setup screen would name %q, want the last binary tried %q", got, managed)
	}
	if got := c.fxUnrunnableList(); len(got) != 2 {
		t.Errorf("unrunnable paths = %v, want both", got)
	}

	// A repair replaces the managed binary; what was learned about the old
	// file no longer holds, and the app resolves it again.
	if err := os.WriteFile(managed, []byte("#!/bin/sh\necho 0.0.17\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	c.forgetUnrunnable(managed)
	c.refreshFx()
	if got := c.fxPathSnapshot(); got != managed {
		t.Fatalf("after the repair fxPath = %q, want %q", got, managed)
	}
	waitForFx(t, func() bool { return c.fxVersionSnapshot() == "fx v0.0.17" })
	if c.fxBrokenSnapshot() {
		t.Error("the repaired install answered, but fx is still reported broken")
	}
}
