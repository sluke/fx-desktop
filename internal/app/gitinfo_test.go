package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

func TestGitBranch(t *testing.T) {
	tests := []struct{ in, want string }{
		{"main", "main"},
		{"No commits yet on main", "main"},
		{"Initial commit on develop", "develop"},
		{"main...origin/main", "main"},
		{"main...origin/main [ahead 1]", "main"},
		{"HEAD (no branch)", "HEAD"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := gitBranch(tc.in); got != tc.want {
			t.Fatalf("gitBranch(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestGitMark(t *testing.T) {
	tests := []struct{ xy, want string }{
		{" M", "M"},
		{"M ", "M"},
		{"A ", "A"},
		{"??", "A"},
		{" D", "D"},
		{"R ", "M"},
	}
	for _, tc := range tests {
		if got := gitMark(tc.xy); got != tc.want {
			t.Fatalf("gitMark(%q) = %q want %q", tc.xy, got, tc.want)
		}
	}
}

func TestPlusOrZero(t *testing.T) {
	if plusOrZero("-") != 0 {
		t.Fatal("- should be 0")
	}
	if plusOrZero("12") != 12 {
		t.Fatal("12 should be 12")
	}
	if plusOrZero("1x") != 0 {
		t.Fatal("malformed should be 0")
	}
}

func TestFormatElapsed(t *testing.T) {
	tests := []struct {
		d    string
		want string
	}{
		{"0s", "00:00"},
		{"5s", "00:05"},
		{"1m4s", "01:04"},
		{"10m0s", "10:00"},
		{"1h2m3s", "1:02:03"},
	}
	for _, tc := range tests {
		d, err := time.ParseDuration(tc.d)
		if err != nil {
			t.Fatal(err)
		}
		if got := formatElapsed(d); got != tc.want {
			t.Fatalf("formatElapsed(%s) = %q want %q", tc.d, got, tc.want)
		}
	}
}

// isolateWithGit isolates HOME like every other test, then puts the real
// git binary back on PATH, which IsolateHome deliberately empties.
func isolateWithGit(t *testing.T) {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	testutil.IsolateHome(t)
	t.Setenv("PATH", filepath.Dir(p)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitInfoOnRepository(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()

	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := gitInfo(context.Background(), dir)
	if err != nil {
		t.Fatalf("gitInfo: %v", err)
	}
	if info == nil {
		t.Fatal("expected git info inside a repository")
	}
	if info.Branch == "" {
		t.Fatal("expected a branch name")
	}
	if len(info.Changes) != 2 {
		t.Fatalf("expected 2 changes, got %v", info.Changes)
	}
	marked := map[string]string{}
	for _, ch := range info.Changes {
		marked[ch.Path] = ch.Kind
	}
	if marked["a.go"] != "M" {
		t.Fatalf("a.go should be modified, got %q", marked["a.go"])
	}
	if marked["b.go"] != "A" {
		t.Fatalf("b.go should be added, got %q", marked["b.go"])
	}
}

func TestGitNumstatCountsChangedLines(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("l1\nl2\nl3\nl4\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	// l3 becomes l9 (-1 +1) and l5, l6 are new (+2): 3 added, 1 deleted.
	write("l1\nl2\nl9\nl4\nl5\nl6\n")

	info, err := gitInfo(context.Background(), dir)
	if err != nil {
		t.Fatalf("gitInfo: %v", err)
	}
	if info == nil {
		t.Fatal("expected git info")
	}
	if len(info.Changes) != 1 {
		t.Fatalf("changes: %+v", info.Changes)
	}
	if info.Changes[0].Adds != 3 || info.Changes[0].Dels != 1 {
		t.Fatalf("numstat = +%d -%d, want +3 -1", info.Changes[0].Adds, info.Changes[0].Dels)
	}
}

// git quotes a path with a space, a quote or a non-ASCII byte in porcelain's
// default output. The quoted spelling is not the file's name: opening it as a
// diff answers "no such file", and the review drawer and the review watermark
// both name a path that does not exist. `-z` is the spelling git offers for
// exactly this, and it also keeps a rename from being read as "old -> new".
func TestGitInfoReadsQuotedAndRenamedPaths(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old name.go", "one\n")
	write("tabs\tname.go", "one\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	write("has space.go", "new\n")
	write("quote\"name.go", "new\n")
	write("日本.go", "new\n")
	write("tabs\tname.go", "one\ntwo\n")
	runGit(t, dir, "mv", "old name.go", "new name.go")

	info, err := gitInfo(context.Background(), dir)
	if err != nil {
		t.Fatalf("gitInfo: %v", err)
	}
	got := map[string]chrome.Change{}
	for _, ch := range info.Changes {
		got[ch.Path] = ch
	}
	for _, want := range []string{"has space.go", "quote\"name.go", "日本.go", "tabs\tname.go", "new name.go"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("change %q is missing; paths: %v", want, info.Changes)
		}
	}
	if _, ok := got["old name.go"]; ok {
		t.Fatalf("a rename must be named by its new path, not its old one: %v", info.Changes)
	}
	for path := range got {
		if strings.Contains(path, " -> ") {
			t.Fatalf("a rename was left as one quoted string: %q", path)
		}
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatalf("change %q does not name a file in the workspace: %v", path, err)
		}
	}
	// The numstat carries the same names, so a path's line counts are not lost
	// under its quoted spelling: one line added to a file whose name has a tab.
	if ch := got["tabs\tname.go"]; ch.Adds != 1 || ch.Dels != 0 {
		t.Fatalf("quoted path lost its line counts: %+v", ch)
	}
	if _, ok := info.Modified["has space.go"]; !ok {
		t.Fatalf("the review watermark has no timestamp for a quoted path: %v", info.Modified)
	}
}

func TestGitInfoOutsideRepository(t *testing.T) {
	isolateWithGit(t)
	info, err := gitInfo(context.Background(), t.TempDir())
	if info != nil {
		t.Fatalf("expected no info outside a repository, got %+v", info)
	}
	// Not a repository is the workspace's own fact, not a failure: the
	// panels have to tell the two apart or a project without git reads as
	// broken.
	if !errors.Is(err, errNoRepo) {
		t.Fatalf("err = %v, want errNoRepo", err)
	}
}

// A repository that git cannot read is a failure and not a missing
// repository: the drawer must not tell the user to run git init on a tree
// that already has a .git. The mark is what decides, not git's message.
func TestGitInfoInAnUnreadableRepositoryFails(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	// A .git that points nowhere: git refuses to work here, but the
	// repository's mark is right there.
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nowhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := gitInfo(context.Background(), dir)
	if info != nil {
		t.Fatalf("expected no info, got %+v", info)
	}
	if err == nil || errors.Is(err, errNoRepo) {
		t.Fatalf("err = %v, want a real failure", err)
	}
}

// A tracked file's diff is read whole and classified by line, so the drawer
// can colour it. The file headers are kept as context: they are where the
// reader learns which file and which hunk this is.
func TestGitFileDiffClassifiesLines(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	if err := os.WriteFile(path, []byte("one\nTWO\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, omitted, err := gitFileDiff(context.Background(), dir, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if omitted != 0 {
		t.Fatalf("a three-line diff should not be capped: %d omitted", omitted)
	}
	kinds := map[string]string{}
	for _, line := range lines {
		switch line.Text {
		case "+++ b/a.go", "--- a/a.go", "+TWO", "-two", " three":
			kinds[line.Text] = line.Kind
		}
	}
	want := map[string]string{
		"+++ b/a.go": "context",
		"--- a/a.go": "context",
		"+TWO":       "add",
		"-two":       "del",
		" three":     "context",
	}
	for text, kind := range want {
		if kinds[text] != kind {
			t.Errorf("%q classified as %q, want %q", text, kinds[text], kind)
		}
	}
}

// An untracked file has no diff against HEAD, so it is compared against
// /dev/null: the drawer shows what would be added rather than an empty pane.
func TestGitFileDiffReadsUntrackedFile(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("fresh\nlines\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, _, err := gitFileDiff(context.Background(), dir, "new.go")
	if err != nil {
		t.Fatal(err)
	}
	var added []string
	for _, line := range lines {
		if line.Kind == "add" {
			added = append(added, line.Text)
		}
	}
	if len(added) != 2 || added[0] != "+fresh" {
		t.Fatalf("added = %v, want the file's two lines", added)
	}
}

// A file with nothing to diff says nothing rather than guessing: a binary
// blob has only the "Binary files ... differ" note, never coloured lines.
func TestGitFileDiffIsEmptyForABinaryFile(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	blob := append([]byte("\x00\x01\x02binary\x00"), make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(dir, "bin"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "bin")
	blob[0] = 0x7f
	if err := os.WriteFile(filepath.Join(dir, "bin"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	lines, _, err := gitFileDiff(context.Background(), dir, "bin")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("git reports that the binary files differ; that line must reach the drawer")
	}
	for _, line := range lines {
		if line.Kind != chrome.DiffContext {
			t.Fatalf("a binary diff has nothing to colour: %v", lines)
		}
	}
}

// An unchanged file has no diff at all, which is what the drawer's note is
// for: an empty pane would read as a failed read.
func TestGitFileDiffIsEmptyForAnUnchangedFile(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	lines, omitted, err := gitFileDiff(context.Background(), dir, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 0 || omitted != 0 {
		t.Fatalf("an unchanged file has no diff: %v (omitted %d)", lines, omitted)
	}
}

func TestClassifyUnifiedDiffCapsLongDiffs(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < maxReviewDiffLines+7; i++ {
		sb.WriteString("+line\n")
	}
	lines, omitted := classifyUnifiedDiff(sb.String())
	if len(lines) != maxReviewDiffLines {
		t.Fatalf("read %d lines, want the cap %d", len(lines), maxReviewDiffLines)
	}
	if omitted != 7 {
		t.Fatalf("omitted = %d, want 7", omitted)
	}
	if lines[0].Kind != "add" {
		t.Fatalf("kind = %q", lines[0].Kind)
	}
}

func TestDiffLineKind(t *testing.T) {
	tests := []struct{ line, want string }{
		{"+++ b/a.go", "context"},
		{"--- a/a.go", "context"},
		{"+added", "add"},
		{"-removed", "del"},
		{" context", "context"},
		{"@@ -1,2 +1,3 @@", "context"},
		{"\\ No newline at end of file", "context"},
		{"", "context"},
	}
	for _, tc := range tests {
		if got := diffLineKind(tc.line); got != tc.want {
			t.Errorf("diffLineKind(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestGitLastCommitReadsTime(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	if got := gitLastCommit(context.Background(), dir); !got.IsZero() {
		t.Fatalf("a repository with no commits has no commit time, got %v", got)
	}
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	got := gitLastCommit(context.Background(), dir)
	if got.IsZero() {
		t.Fatal("expected a commit time after committing")
	}
	if time.Since(got) > time.Hour {
		t.Fatalf("commit time looks wrong: %v", got)
	}
}

// gitInfo carries the timestamps the watermark needs: when the last commit
// happened, and when each changed path was last written.
func TestGitInfoReadsTheReviewWatermark(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Pin the write later than the commit, so the assertion does not depend
	// on filesystem timestamp granularity.
	written := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, written, written); err != nil {
		t.Fatal(err)
	}

	info, err := gitInfo(context.Background(), dir)
	if err != nil {
		t.Fatalf("gitInfo: %v", err)
	}
	if info == nil {
		t.Fatal("expected git info")
	}
	if info.LastCommit.IsZero() {
		t.Fatal("a committed repository has a commit time")
	}
	if got := info.Modified["a.go"]; !got.After(info.LastCommit) {
		t.Fatalf("a.go's write time %v should be after the commit %v", got, info.LastCommit)
	}
	if !info.unreviewed(time.Time{}) {
		t.Fatal("a write newer than the last commit awaits review")
	}
	if info.unreviewed(written.Add(time.Second)) {
		t.Fatal("a look after the write clears it")
	}
}

// The reviewed watermark is the later of the last commit and the last look;
// a change newer than both is what the rail raises (§5, §12.0).
func TestGitInfoUnreviewed(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	info := &GitInfo{
		Changes:    []chrome.Change{{Path: "a.go"}},
		Modified:   map[string]time.Time{"a.go": base.Add(time.Hour)},
		LastCommit: base,
	}
	if !info.unreviewed(time.Time{}) {
		t.Fatal("a change newer than the last commit awaits review")
	}
	if info.unreviewed(base.Add(2 * time.Hour)) {
		t.Fatal("a change older than the last look has been reviewed")
	}
	// A commit after the change is enough on its own: the review was the
	// commit itself.
	committed := &GitInfo{
		Changes:    []chrome.Change{{Path: "a.go"}},
		Modified:   map[string]time.Time{"a.go": base},
		LastCommit: base.Add(time.Hour),
	}
	if committed.unreviewed(time.Time{}) {
		t.Fatal("a change older than the last commit is already reviewed")
	}
	// A path with no timestamp at all (a deletion) is not assumed reviewed.
	unknown := &GitInfo{
		Changes:    []chrome.Change{{Path: "gone.go"}},
		Modified:   map[string]time.Time{},
		LastCommit: base.Add(time.Hour),
	}
	if !unknown.unreviewed(time.Time{}) {
		t.Fatal("a deletion has no timestamp; it must not be assumed reviewed")
	}
	if (&GitInfo{}).unreviewed(time.Time{}) {
		t.Fatal("a clean tree awaits nothing")
	}
	var nilInfo *GitInfo
	if nilInfo.unreviewed(time.Time{}) {
		t.Fatal("no git info awaits nothing")
	}
}
