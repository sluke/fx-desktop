//go:build unix

package safedir

// POSIX-only special-file cases for the safe directory-open boundary (S6).
// A FIFO is created in a temp dir and never opened by the test process; Open
// must reject it from its type before a blocking open can happen.

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenStaticFIFODoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	start := time.Now()
	_, err := Open(path)
	if !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("FIFO error = %v, want ErrNotDirectory", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("opening a FIFO took %v", elapsed)
	}
}

func TestOpenSymlinkToFIFODoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	link := filepath.Join(dir, "link")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := Open(link); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("link-to-FIFO error = %v, want ErrNotDirectory", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("opening a link to a FIFO took %v", elapsed)
	}
}

func TestOpenSymlinkToDirectoryIsAllowed(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	link := filepath.Join(dir, "link")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "entry"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	f, err := Open(link)
	if err != nil {
		t.Fatalf("a legal directory link was rejected: %v", err)
	}
	defer f.Close()
	entries, err := f.ReadDir(4)
	if err != nil && len(entries) == 0 {
		t.Fatalf("ReadDir on a linked directory: %v", err)
	}
}
