package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/projects"
	"fx-desktop/internal/sessionstats"
)

// A link names its folder with an absolute path. A relative one would be
// resolved against whatever directory the app started in.
func TestRegressionR9_LinkPathMustBeAbsolute(t *testing.T) {
	for _, raw := range []string{
		"fx-desktop://open?path=relative/dir",
		"fx-desktop://open?path=..%2F..%2Ftmp",
		"fx-desktop://open?path=.",
		"fx-desktop://?path=Users/Shared/x",
	} {
		if got, ok := ProjectFromURL(raw); ok {
			t.Errorf("ProjectFromURL(%q) = %q, want it refused", raw, got)
		}
		if got, ok := ProjectRequestFromArg(raw, "/work/wd"); ok {
			t.Errorf("ProjectRequestFromArg(%q) = %+v, want it refused", raw, got)
		}
	}
	request, ok := ProjectRequestFromArg("fx-desktop://open?path=%2Fdl%2Fproj", "/work/wd")
	if !ok || request.Path != "/dl/proj" || !request.FromLink {
		t.Errorf("a link argument = %+v, %v", request, ok)
	}
	request, ok = ProjectRequestFromArg("child", "/work/wd")
	if !ok || request.Path != filepath.Join("/work/wd", "child") || request.FromLink {
		t.Errorf("a path argument = %+v, %v", request, ok)
	}
}

func linkCoordinator(t *testing.T) (*Coordinator, *[]string, *[]string) {
	t.Helper()
	var asked, opened []string
	c := &Coordinator{projects: &projects.Store{}}
	c.confirmLink = func(path string) bool {
		asked = append(asked, path)
		return false
	}
	c.openProject = func(path string) { opened = append(opened, path) }
	return c, &asked, &opened
}

// A folder the user has never opened here is opened from a link only after
// they agree: opening it starts fx there and reads its Git state, which runs
// commands the folder's own configuration names.
func TestRegressionR9_LinkToUnknownFolderAsksFirst(t *testing.T) {
	unknown := filepath.Join(t.TempDir(), "downloaded-project")

	c, asked, opened := linkCoordinator(t)
	if c.OpenProjectFromLink(unknown) {
		t.Error("a declined link reported that it opened the folder")
	}
	if len(*asked) != 1 || (*asked)[0] != unknown {
		t.Errorf("asked about %v, want the linked folder once", *asked)
	}
	if len(*opened) != 0 {
		t.Fatalf("the folder was opened although the user declined: %v", *opened)
	}

	c.confirmLink = func(path string) bool { *asked = append(*asked, path); return true }
	if !c.OpenProjectFromLink(unknown) || len(*opened) != 1 || (*opened)[0] != unknown {
		t.Errorf("an accepted link: opened %v", *opened)
	}

	// The second-instance route (a link in argv) takes the same question.
	c, asked, opened = linkCoordinator(t)
	c.HandleSecondInstance([]string{"--flag", "fx-desktop://open?path=" + unknown}, "/work/wd")
	if len(*asked) != 1 || len(*opened) != 0 {
		t.Errorf("link in argv: asked %v, opened %v; want one question and nothing opened", *asked, *opened)
	}
	// A path typed after the command is the user's own request.
	c.HandleSecondInstance([]string{unknown}, "/work/wd")
	if len(*asked) != 1 || len(*opened) != 1 {
		t.Errorf("path in argv: asked %v, opened %v; want it opened without a question", *asked, *opened)
	}

	// A relative path never reaches the question.
	c, asked, opened = linkCoordinator(t)
	if c.OpenProjectFromLink("relative/dir") || len(*asked) != 0 || len(*opened) != 0 {
		t.Errorf("a relative link path: asked %v, opened %v", *asked, *opened)
	}
}

// A folder that is already open, was the last project, or is in the recent
// list was opened by the user before; a link to it needs no question.
func TestRegressionR9_LinkToKnownFolderOpensDirectly(t *testing.T) {
	base := t.TempDir()
	recent, last, open := filepath.Join(base, "recent"), filepath.Join(base, "last"), filepath.Join(base, "open")
	for _, dir := range []string{recent, last, open} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c, asked, opened := linkCoordinator(t)
	c.projects.Touch(recent)
	c.prefs.LastProject = last
	c.sessions = []*ProjectWindow{{project: open}}
	for _, path := range []string{recent, last, open, recent + string(filepath.Separator)} {
		if !c.OpenProjectFromLink(path) {
			t.Errorf("a link to %s was not opened", path)
		}
	}
	if len(*asked) != 0 {
		t.Errorf("asked about folders the user already opened: %v", *asked)
	}
	if len(*opened) != 4 {
		t.Errorf("opened %v, want all four requests", *opened)
	}

	if !linkedProjectKnown("/a", true, "", nil) || !linkedProjectKnown("/a", false, "/a/", nil) || !linkedProjectKnown("/a", false, "", []string{"/b", "/a"}) {
		t.Error("linkedProjectKnown missed a known folder")
	}
	if linkedProjectKnown("/a", false, "", nil) || linkedProjectKnown("/a", false, "/ab", []string{"/a/b", "/A"}) {
		t.Error("linkedProjectKnown accepted an unknown folder")
	}
}

func TestRegressionR9_LinkIsIgnoredWhileQuitting(t *testing.T) {
	c, asked, opened := linkCoordinator(t)
	c.stats = sessionstats.New(filepath.Join(t.TempDir(), "session-stats.json"))
	c.quitBudget = 200 * time.Millisecond
	done := make(chan struct{})
	c.beginQuit(nil, func() { close(done) })
	if c.OpenProjectFromLink(filepath.Join(t.TempDir(), "x")) || len(*asked) != 0 || len(*opened) != 0 {
		t.Errorf("a link during the final drain: asked %v, opened %v", *asked, *opened)
	}
	waitClosed(t, done, "the drain never finished")
}

// The app reads Git state in the background: when a window opens, every
// thirty seconds and when the drawer loads a file. Those reads must not
// rewrite the index. `git status` does by default, holding index.lock, and a
// command the agent runs at that moment fails on the lock.
func TestRegressionR9_BackgroundGitReadsLeaveTheIndexAlone(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.invalid")
	runGit(t, dir, "config", "user.name", "Test")
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "a.go")
	runGit(t, dir, "commit", "-q", "-m", "one")

	// Same content, new timestamp: the index's cached stat data is stale, so
	// a status that refreshes the index has something to write.
	later := time.Now().Add(90 * time.Second)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, ".git", "index")
	before, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, _ := os.ReadFile(index)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	info, err := gitInfo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Changes) != 0 {
		t.Fatalf("a touched but unchanged file was reported as a change: %+v", info.Changes)
	}
	if _, _, _, err := gitFileDiff(ctx, dir, "a.go"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(index)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, _ := os.ReadFile(index)
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) || string(afterBytes) != string(beforeBytes) {
		t.Errorf("a background read rewrote .git/index (same file: %v, mtime %v -> %v, %d -> %d bytes)",
			os.SameFile(before, after), before.ModTime().Format(time.RFC3339Nano), after.ModTime().Format(time.RFC3339Nano), len(beforeBytes), len(afterBytes))
	}
	if entries, _ := filepath.Glob(filepath.Join(dir, ".git", "*.lock")); len(entries) != 0 {
		t.Errorf("lock files left behind: %s", strings.Join(entries, ", "))
	}
}
