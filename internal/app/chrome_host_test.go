package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

func TestSessionTitle(t *testing.T) {
	tests := []struct {
		project string
		want    string
	}{
		{project: "/Users/me/code/myapp", want: "fx — myapp"},
		{project: "", want: "fx"},
		{project: ".", want: "fx"},
		{project: string(filepath.Separator), want: "fx"},
	}
	for _, tc := range tests {
		if got := sessionTitle(tc.project); got != tc.want {
			t.Fatalf("sessionTitle(%q) = %q want %q", tc.project, got, tc.want)
		}
	}
}

func TestCoordinatorRecentPaths(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	a := filepath.Join(t.TempDir(), "alpha")
	b := filepath.Join(t.TempDir(), "beta")
	c.projects.Touch(a)
	c.projects.Touch(b)
	c.projects.Touch(a)

	paths := c.recentPaths()
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %v", paths)
	}
	if paths[0] != a {
		t.Fatalf("expected %q first, got %v", a, paths)
	}
}

func TestSidebarDataProjectAndRecent(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.fxVersion = "fx v0.0.13"
	proj := filepath.Join(t.TempDir(), "workspace")
	c.projects.Touch(proj)

	d := c.sidebarData(proj, nil)
	if d.ProjectPath != proj || d.FxVersion != "fx v0.0.13" {
		t.Fatalf("project data: %+v", d)
	}
	if len(d.Recent) == 0 || d.Recent[0] != proj {
		t.Fatalf("recent: %v", d.Recent)
	}
	if d.Welcome {
		t.Fatal("welcome should be false")
	}
}

func TestSidebarDataWelcome(t *testing.T) {
	c := NewCoordinator()
	w := &welcomeModel{busy: true, status: "Downloading…"}
	d := c.sidebarData("", w)
	if !d.Welcome || !d.WelcomeBusy || d.WelcomeText != "Downloading…" {
		t.Fatalf("welcome flags: %+v", d)
	}
	if d.OnInstall == nil {
		t.Fatal("expected OnInstall callback")
	}
	w.busy = false
	w.status = ""
	d.OnInstall()
	if !w.busy || w.status != "Downloading…" {
		t.Fatalf("OnInstall should set busy state, got busy=%v status=%q", w.busy, w.status)
	}
}

func TestChromeActionsNonNil(t *testing.T) {
	c := NewCoordinator()
	a := c.chromeActions(nil)
	if a.OpenFolder == nil || a.OpenProject == nil || a.OpenInFileManager == nil ||
		a.OpenPreferences == nil || a.OpenDocs == nil || a.OpenConfig == nil ||
		a.RunDoctor == nil || a.CheckUpdates == nil {
		t.Fatal("chrome actions should wire all menu entries")
	}
}

func TestResolveFileManagerPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "Documents", "VC")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveFileManagerPath("~/Documents/VC")
	if err != nil || got != project {
		t.Fatalf("expand ~: got %q err %v, want %q", got, err, project)
	}
	if got, err := resolveFileManagerPath("~"); err != nil || got != home {
		t.Fatalf("expand bare ~: got %q err %v, want %q", got, err, home)
	}
	if _, err := resolveFileManagerPath(filepath.Join(home, "missing")); err == nil {
		t.Fatal("a missing folder was accepted")
	}
	file := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveFileManagerPath(file); err == nil {
		t.Fatal("a file was accepted as a folder")
	}
	if _, err := resolveFileManagerPath("  "); err == nil {
		t.Fatal("an empty path was accepted")
	}
}

// The palette's row table lives in package chrome; the handlers for those rows
// live in this package's runCommand. A row with no case is a click that runs
// nothing, and nothing else would notice — so the two tables are checked
// against each other (§A.6.3).
func TestEveryPaletteRowHasADispatcherCase(t *testing.T) {
	src, err := os.ReadFile("chrome_host.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, cmd := range chrome.Commands {
		// toggle-sidebar is a view toggle the chrome layer runs itself.
		if cmd.ID == "toggle-sidebar" {
			continue
		}
		if !strings.Contains(body, fmt.Sprintf("case %q:", cmd.ID)) {
			t.Fatalf("palette row %q has no handler in runCommand", cmd.ID)
		}
	}
}

func TestSessionSettingsPalettePreservesDraftAndResetsPicker(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	chat := newChatSession("/missing-local-stub", t.TempDir(), nil, nil)
	defer chat.Close()
	s := &Session{coord: c, project: t.TempDir(), chat: chat}
	s.chatUI.Draft = "keep this draft"
	s.chatUI.ConfigOpen, s.chatUI.ConfigQuery = "model", "old filter"
	c.runCommand(s, "session-settings")
	if !s.chatUI.ConfigListOpen || s.chatUI.ConfigOpen != "" || s.chatUI.ConfigQuery != "" || s.chatUI.Draft != "keep this draft" {
		t.Fatal("settings command did not open a clean overview or lost draft")
	}
	c.runCommand(s, "set-model")
	if s.chatUI.ConfigListOpen || s.chatUI.ConfigOpen != "model" {
		t.Fatal("model picker failed to replace overview")
	}
}

func TestSessionActionsWireFxIntegration(t *testing.T) {
	c := NewCoordinator()
	s := &Session{project: "/p/proj"}
	a := c.sessionActions(s)
	// Every fx-backed affordance must be wired, or the panel hides it.
	if a.NewSession == nil || a.Commit == nil || a.RunCommand == nil ||
		a.ResumeSession == nil || a.Ask == nil {
		t.Fatalf("session actions incomplete: %+v", a)
	}
	if a.StageAll == nil || a.SetAppearance == nil || a.FocusSession == nil {
		t.Fatalf("session actions incomplete: %+v", a)
	}
	// The right rail's file browser acts on this window's workspace.
	if a.SelectInspectorTab == nil || a.ToggleFileDir == nil || a.OpenFile == nil ||
		a.OpenFileInEditor == nil || a.RefreshFiles == nil {
		t.Fatalf("file browser actions incomplete: %+v", a)
	}
}

// A command window (fx pr, fx doctor …) has no workspace. It must not be
// handed actions that act on one, or the panels offer buttons that cannot
// work and the palette offers rows that open nothing.
func TestCommandSessionHidesWorkspaceActions(t *testing.T) {
	c := NewCoordinator()
	a := c.sessionActions(&Session{})
	if a.Commit != nil || a.StageAll != nil ||
		a.ResumeSession != nil || a.Ask != nil || a.ChooseConfig != nil {
		t.Fatalf("workspace actions must stay unwired without a project: %+v", a)
	}
	if a.SelectInspectorTab != nil || a.ToggleFileDir != nil || a.OpenFile != nil ||
		a.OpenFileInEditor != nil || a.RefreshFiles != nil {
		t.Fatalf("file browser must stay unwired without a workspace: %+v", a)
	}
	// What still works in every window stays wired.
	if a.NewSession == nil || a.RunCommand == nil || a.SetAppearance == nil {
		t.Fatalf("window actions should stay wired: %+v", a)
	}
	// Signing in needs no workspace, and a signed-out user has to be able
	// to reach it from wherever they are — including before a folder is
	// open, which is exactly the case this test covers.
	if a.SignIn == nil {
		t.Fatal("sign-in should stay wired without a project")
	}
	// The rail is the app's map, not a workspace affordance: raising another
	// session works from a command window too.
	if a.FocusSession == nil {
		t.Fatal("focus session should stay wired without a project")
	}
}

// The rail lists live sessions, and a session with no window yet is still a
// real session: it must appear, not vanish. focusSession is the pair of
// sessionSummaries — it raises by the same id the rail shows.
func TestSessionSummariesCoverEveryRegisteredSession(t *testing.T) {
	c := NewCoordinator()
	c.sessions = append(c.sessions, &Session{project: "/a"}, &Session{project: "/b"})
	sums := c.sessionSummaries()
	if len(sums) != 2 {
		t.Fatalf("expected 2 rail rows, got %+v", sums)
	}
	if sums[0].ID == "" || sums[1].ID == "" {
		t.Fatalf("rail ids must be stable: %+v", sums)
	}
	// A windowless session has no window to raise; focusing must be a no-op,
	// not a panic.
	c.focusSession(sums[0].ID)
	c.focusSession("no-such-session")
}

func TestSessionDataCarriesGitAndSessions(t *testing.T) {
	c := NewCoordinator()
	s := &Session{
		project: "/p/proj",
		git:     &GitInfo{Branch: "main", Changes: []chrome.Change{{Path: "a.go", Kind: "M"}}},
		saved:   []FxSession{{ID: "abc", Title: "Rename", UpdatedAtMS: time.Now().Add(-2 * time.Minute).UnixMilli(), HistoryLen: 3}},
	}
	d := c.sessionData(s)
	if len(d.Changes) != 1 {
		t.Fatalf("git data missing: %+v", d)
	}
	if len(d.Saved) != 1 || d.Saved[0].ID != "abc" || d.Saved[0].Turns != 3 {
		t.Fatalf("saved sessions: %+v", d.Saved)
	}
	s.chat = newChatSession("", s.project, nil, nil)
	defer s.chat.Close()
	s.chat.sessionID = "abc"
	if current := c.sessionData(s); !current.Saved[0].Active {
		t.Fatal("current ACP conversation must be selected in the sidebar")
	}
	if d.Saved[0].When != "2m ago" {
		t.Fatalf("when: %q", d.Saved[0].When)
	}
}

func TestRunCommandOpensDialogs(t *testing.T) {
	c := NewCoordinator()
	s := &Session{} // no workspace
	c.runCommand(s, "ask")
	if !s.shell.AskOpen {
		t.Fatal("ask should open the quick-ask dialog")
	}
	// A native session routes set-model to the conversation's own picker.
	s.chat = newChatSession("", s.project, nil, nil)
	defer s.chat.Close()
	c.runCommand(s, "set-model")
	if s.chatUI.ConfigOpen != "model" {
		t.Fatalf("set-model should open the model picker, got %q", s.chatUI.ConfigOpen)
	}
}

func TestFirstLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "git failed"},
		{"one line\n", "one line"},
		{"first\nsecond\n", "first"},
		{"  spaced  \n", "spaced"},
	} {
		if got := firstLine([]byte(tc.in)); got != tc.want {
			t.Fatalf("firstLine(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

// A command that printed nothing must name itself, not the last command
// that happened to share the helper: fx failures are not git failures.
func TestFirstLineWithFallback(t *testing.T) {
	if got := firstLineWith(nil, "fx sessions --clear failed"); got != "fx sessions --clear failed" {
		t.Fatalf("fallback = %q", got)
	}
	if got := firstLineWith([]byte("boom\nmore"), "unused"); got != "boom" {
		t.Fatalf("first line = %q", got)
	}
	if got := firstLineWith([]byte("   "), "fallback"); got != "fallback" {
		t.Fatalf("blank output should fall back, got %q", got)
	}
}
