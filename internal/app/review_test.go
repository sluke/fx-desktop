package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

// waitFor polls until cond holds. The review drawer reads a diff off the UI
// thread, so its tests wait on the result rather than on a sleep of a guessed
// length.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// toggleReview opens the drawer, opens the first changed file, and records
// the look: that timestamp is what stops the rail raising the same changes
// again (§5).
func TestToggleReviewOpensSelectsAndAcknowledges(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell()}
	s.workspace.mu.Lock()
	s.workspace.git = &GitInfo{Branch: "main", Changes: []chrome.Change{
		{Path: "a.go", Kind: "M", Adds: 2, Dels: 1},
		{Path: "b.go", Kind: "A", Adds: 9},
	}}
	s.workspace.mu.Unlock()

	c.toggleReview(s)
	if !s.shell.ReviewOpen {
		t.Fatal("⌘R should open the drawer")
	}
	if s.shell.ReviewPath != "a.go" {
		t.Fatalf("selected = %q, want the first changed path", s.shell.ReviewPath)
	}
	s.workspace.mu.Lock()
	acked := !s.workspace.reviewedAt.IsZero()
	s.workspace.mu.Unlock()
	if !acked {
		t.Fatal("opening the drawer is the acknowledgement §5 asks for")
	}

	d := c.sessionReview(s)
	if !d.Open || d.Workspace != chrome.WorkspaceRepo || d.Branch != "main" || len(d.Changes) != 2 {
		t.Fatalf("review data = %+v", d)
	}

	c.toggleReview(s)
	if s.shell.ReviewOpen {
		t.Fatal("a second ⌘R should close the drawer")
	}
}

// Closing the drawer is not a decision about the changes, so it leaves the
// watermark where it was; reopening keeps the file that was open.
func TestCloseReviewKeepsTheSelectionAndWatermark(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell()}
	c.toggleReview(s)
	s.shell.ReviewPath = "b.go"
	s.workspace.mu.Lock()
	s.workspace.reviewedAt = time.Time{}
	s.workspace.mu.Unlock()

	c.closeReview(s)
	if s.shell.ReviewOpen {
		t.Fatal("closeReview should hide the drawer")
	}
	if s.shell.ReviewPath != "b.go" {
		t.Fatalf("closing lost the open file: %q", s.shell.ReviewPath)
	}
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	if !s.workspace.reviewedAt.IsZero() {
		t.Fatal("closing the drawer should not move the watermark")
	}
}

// The drawer reads the selected file's whole diff, including a file git has
// never seen: an untracked path is compared against /dev/null so the drawer
// shows what would be added.
func TestSelectChangeLoadsTheWholeDiff(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	writeFile(t, filepath.Join(dir, "a.go"), "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(dir, "a.go"), "one\ntwo\n")
	writeFile(t, filepath.Join(dir, "b.go"), "brand new\n")

	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: dir, shell: chrome.DefaultShell()}
	s.refreshGit()
	c.toggleReview(s)

	c.selectChange(s, "a.go")
	waitFor(t, "a.go's diff", func() bool {
		d := c.sessionReview(s)
		return d.Selected == "a.go" && !d.Busy && len(d.Diff) > 0
	})
	if d := c.sessionReview(s); !hasDiffLine(d.Diff, chrome.DiffAdd, "two") {
		t.Fatalf("a.go's diff is missing the added line: %+v", d.Diff)
	}

	c.selectChange(s, "b.go")
	waitFor(t, "b.go's diff", func() bool {
		d := c.sessionReview(s)
		return d.Selected == "b.go" && !d.Busy && len(d.Diff) > 0
	})
	d := c.sessionReview(s)
	if !hasDiffLine(d.Diff, chrome.DiffAdd, "brand new") {
		t.Fatalf("an untracked file should read as all additions: %+v", d.Diff)
	}
	for _, line := range d.Diff {
		if line.Kind != chrome.DiffAdd && line.Kind != chrome.DiffContext {
			t.Fatalf("an untracked file has nothing to delete: %+v", d.Diff)
		}
	}
}

// Two selections in flight must not let the first reply overwrite the
// second: the drawer shows the file the user picked last.
func TestSelectChangeDropsAStaleRead(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	writeFile(t, filepath.Join(dir, "a.go"), "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(dir, "a.go"), "one\ntwo\n")
	writeFile(t, filepath.Join(dir, "b.go"), "brand new\n")

	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: dir, shell: chrome.DefaultShell()}
	s.refreshGit()

	c.selectChange(s, "a.go")
	c.selectChange(s, "b.go")
	waitFor(t, "b.go's diff", func() bool {
		d := c.sessionReview(s)
		return d.Selected == "b.go" && !d.Busy && len(d.Diff) > 0
	})
	// Give a.go's slower reply time to land; the guard must drop it.
	time.Sleep(50 * time.Millisecond)
	d := c.sessionReview(s)
	if d.Selected != "b.go" {
		t.Fatalf("a stale read took over the drawer: %q", d.Selected)
	}
	if hasDiffLine(d.Diff, chrome.DiffAdd, "two") {
		t.Fatalf("a stale read's lines are on screen: %+v", d.Diff)
	}
}

// The rail's green dot is the last two facts in one: the tree changed, and
// the change is newer than both the last commit and the last look.
func TestSummaryRaisesChangesNewerThanTheWatermark(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	now := time.Now()
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell()}
	s.workspace.mu.Lock()
	s.workspace.git = &GitInfo{
		Changes:    []chrome.Change{{Path: "a.go", Kind: "M"}},
		Modified:   map[string]time.Time{"a.go": now},
		LastCommit: now.Add(-time.Hour),
	}
	s.workspace.mu.Unlock()

	if got := s.summary().Status; got != chrome.AttentionDoneUnreviewed {
		t.Fatalf("status = %s, want done-unreviewed", got)
	}
	c.toggleReview(s)
	if got := s.summary().Status; got != chrome.AttentionIdle {
		t.Fatalf("opening the drawer should clear the review dot, got %s", got)
	}
	if s.summary().Changes != 1 {
		t.Fatal("the count stays: the change is still there, it is just no longer new")
	}
}

// A change older than the last commit has been through a review already — the
// commit itself — so it does not raise the rail (§12.0).
func TestSummaryLeavesOldChangesAlone(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	now := time.Now()
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell()}
	s.workspace.mu.Lock()
	s.workspace.git = &GitInfo{
		Changes:    []chrome.Change{{Path: "a.go", Kind: "M"}},
		Modified:   map[string]time.Time{"a.go": now.Add(-2 * time.Hour)},
		LastCommit: now.Add(-time.Hour),
	}
	s.workspace.mu.Unlock()
	if got := s.summary().Status; got != chrome.AttentionIdle {
		t.Fatalf("status = %s, want idle", got)
	}
}

func TestReviewDataReportsAReadFailure(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: "/nowhere", shell: chrome.DefaultShell()}
	s.workspace.mu.Lock()
	s.workspace.gitErr = os.ErrNotExist
	s.workspace.mu.Unlock()
	d := c.sessionReview(s)
	if d.Workspace != chrome.WorkspaceFailed {
		t.Fatalf("a failed read must say so: %+v", d)
	}
}

// A workspace that is not a repository is not a broken one. The two must
// reach the drawer as different answers, or a project without git reads as a
// fault the user is expected to fix.
func TestReviewDataSeparatesNoRepositoryFromFailure(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: "/nowhere", shell: chrome.DefaultShell()}
	s.workspace.mu.Lock()
	s.workspace.gitErr = errNoRepo
	s.workspace.mu.Unlock()
	d := c.sessionReview(s)
	if d.Workspace != chrome.WorkspaceNotRepo {
		t.Fatalf("a workspace without git must say so: %+v", d)
	}
}

// The drawer's selection survives a workspace reread, which is what keeps a
// commit from bouncing the user back to the first file.
func TestReviewKeepsTheSelectionAcrossAReread(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell()}
	c.toggleReview(s)
	c.selectChange(s, "keep/me.go")
	if got := c.sessionReview(s).Selected; got != "keep/me.go" {
		t.Fatalf("selected = %q", got)
	}
}

// A command window (fx doctor, fx pr …) has no workspace, so it gets no way
// into the review drawer at all — not even a status-strip button. A door that
// opens onto "Workspace unavailable" is an advertisement for something the
// window cannot do.
func TestCommandSessionOffersNoReviewDrawer(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &ProjectWindow{coord: c, title: "fx doctor", shell: chrome.DefaultShell()}
	a := c.sessionActions(s)
	if a.ToggleReview != nil || a.CloseReview != nil || a.RefreshChanges != nil || a.Commit != nil {
		t.Fatalf("a command window cannot review a tree: %+v", a)
	}

	project := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell()}
	p := c.sessionActions(project)
	if p.ToggleReview == nil || p.CloseReview == nil || p.RefreshChanges == nil || p.SelectChange == nil {
		t.Fatal("a project session can review its tree")
	}
}

// A project that is not a git repository has nothing the drawer could ever
// read, so the strip and the palette do not offer it: the rail's Changes line
// is what says why. An already-open drawer still closes, and Refresh still
// re-reads the tree in case git arrives.
func TestProjectWithoutGitOffersNoReviewEntryPoint(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell()}
	s.workspace.mu.Lock()
	s.workspace.gitErr = errNoRepo
	s.workspace.mu.Unlock()
	a := c.sessionActions(s)
	if a.ToggleReview != nil {
		t.Fatal("a workspace without git has no review drawer to open")
	}
	if a.CloseReview == nil || a.RefreshChanges == nil {
		t.Fatal("the drawer must still close, and Refresh still rereads the tree")
	}
	if d := c.sessionData(s); d.Workspace != chrome.WorkspaceNotRepo {
		t.Fatalf("rail state = %v, want %v", d.Workspace, chrome.WorkspaceNotRepo)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasDiffLine(lines []chrome.DiffLine, kind, text string) bool {
	for _, line := range lines {
		if line.Kind == kind && strings.Contains(line.Text, text) {
			return true
		}
	}
	return false
}
