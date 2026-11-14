package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewR2TruncationReported(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	blob := make([]byte, 2<<20)
	for i := range blob {
		blob[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, truncated, err := gitFileDiff(context.Background(), dir, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Fatal("expected byte-cap truncation to be reported")
	}
}

func TestGitNumstatFileNamedZero(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "0"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "0"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := gitInfo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Changes) != 1 || info.Changes[0].Path != "0" || info.Changes[0].Adds == 0 {
		t.Fatalf("file named 0 lost its count: %+v", info.Changes)
	}
}

func TestGitUntrackedLiteralName(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "*.txt"), []byte("star\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, _, _, err := gitFileDiff(context.Background(), dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, line := range lines {
		text += line.Text + "\n"
	}
	if !strings.Contains(text, "+star") || strings.Contains(text, "other.txt") {
		t.Fatalf("untracked literal diff = %s", text)
	}
}

func TestGitSHA256UnbornRepoShowsStagedDiff(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--object-format=sha256", dir).CombinedOutput(); err != nil {
		t.Skipf("sha256 repos unsupported: %v %s", err, out)
	}
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "a.txt")
	lines, _, _, err := gitFileDiff(context.Background(), dir, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range lines {
		if line.Text == "+hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sha256 unborn diff = %v", lines)
	}
}

func TestGitLiteralPathspecDoesNotMatchSiblings(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	for name, body := range map[string]string{"*.txt": "star\n", "other.txt": "other\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, "*.txt"), []byte("star\nchanged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, _, _, err := gitFileDiff(context.Background(), dir, "*.txt")
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Builder{}
	for _, line := range lines {
		text.WriteString(line.Text)
		text.WriteByte('\n')
	}
	if strings.Contains(text.String(), "other.txt") {
		t.Fatalf("literal pathspec leaked sibling file: %s", text.String())
	}
}

func TestGitNumstatRenameCountsNewPath(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "t@e.com")
	runGit(t, dir, "config", "user.name", "t")
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "old.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	runGit(t, dir, "mv", "old.txt", "new.txt")
	sb.WriteString("extra\n")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := gitInfo(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range info.Changes {
		if ch.Path == "new.txt" && ch.Adds > 0 {
			return
		}
	}
	t.Fatalf("rename lost numstat on new path: %+v", info.Changes)
}
