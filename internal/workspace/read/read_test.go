package read

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"fx-desktop/internal/workspace"
)

func TestResolveReadsTheFilesAMessageNames(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mentions, err := Resolve(context.Background(), project, "look at @main.go and @missing.go")
	if err != nil || len(mentions) != 1 {
		t.Fatalf("Resolve = %+v, %v", mentions, err)
	}
	if mentions[0].Path != "main.go" || !strings.HasSuffix(mentions[0].Abs, "main.go") || mentions[0].Text != "package main\n" {
		t.Fatalf("mention = %+v", mentions[0])
	}
}

// A file that is too large is refused with a reason, rather than attached
// truncated: the read is bounded, so it cannot attach more than fx accepts.
func TestResolveRefusesOversizedAndBinaryFiles(t *testing.T) {
	project := t.TempDir()
	big := bytes.Repeat([]byte("x"), workspace.MaxMentionBytes+1)
	if err := os.WriteFile(filepath.Join(project, "big.txt"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), project, "read @big.txt"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
	binary := []byte{0xff, 0xfe, 0x00, 0x01}
	if err := os.WriteFile(filepath.Join(project, "blob.bin"), binary, 0o600); err != nil {
		t.Fatal(err)
	}
	if utf8.Valid(binary) {
		t.Skip("the fixture is not actually invalid UTF-8")
	}
	if _, err := Resolve(context.Background(), project, "read @blob.bin"); err == nil || !strings.Contains(err.Error(), "not a text file") {
		t.Fatalf("err = %v", err)
	}
}

// B3: a cancelled read is a failure, never a successful empty result — the
// caller would send the message without the content it named.
func TestResolveCancellationIsAnError(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mentions, err := Resolve(ctx, project, "read @main.go")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if mentions != nil {
		t.Fatalf("a cancelled read returned %+v", mentions)
	}

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := Resolve(expired, project, "read @main.go"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}
