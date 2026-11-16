package read

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRefusesASymlinkThatLeavesTheProject(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "repo")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "note.txt"), []byte("synthetic outside content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "my"), []byte("prefix"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "note.txt"), filepath.Join(project, "link.txt")); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(root, "repo-other")
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "file.txt"), []byte("sibling"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "file.txt"), filepath.Join(project, "side.txt")); err != nil {
		t.Fatal(err)
	}
	_, err := Resolve(context.Background(), project, "read @link.txt")
	var refused *Refusal
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("external symlink err = %v", err)
	}
	_, err = Resolve(context.Background(), project, "read @side.txt")
	if !errors.As(err, &refused) {
		t.Fatalf("sibling-prefix symlink err = %v", err)
	}
	// An unclosed quote must not attach the prefix file "my".
	mentions, err := Resolve(context.Background(), project, `read @"my file.go`)
	if err != nil || len(mentions) != 0 {
		t.Fatalf("unclosed quote = %+v, %v", mentions, err)
	}
	// A link that stays inside the project is still that file.
	if err := os.WriteFile(filepath.Join(project, "real.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", filepath.Join(project, "inner.txt")); err != nil {
		t.Fatal(err)
	}
	mentions, err = Resolve(context.Background(), project, "read @inner.txt")
	if err != nil || len(mentions) != 1 || mentions[0].Text != "inside" {
		t.Fatalf("internal symlink = %+v, %v", mentions, err)
	}
	if err := os.Symlink("missing.txt", filepath.Join(project, "dangling.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), project, "read @dangling.txt"); !errors.As(err, &refused) {
		t.Fatalf("dangling symlink err = %v", err)
	}
}
