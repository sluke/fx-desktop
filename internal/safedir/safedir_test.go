package safedir

// Cross-platform contract for the safe directory-open boundary (S6).

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenReadsADirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "entry"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(dir)
	if err != nil {
		t.Fatalf("Open(%q): %v", dir, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		t.Fatalf("opened object is not a directory: %v %+v", err, info)
	}
	entries, err := f.ReadDir(4)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("batched ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "entry" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestOpenMissingDirectory(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "absent"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing directory error = %v", err)
	}
}

func TestOpenRegularFileIsRejectedBeforeOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("regular file error = %v, want ErrNotDirectory", err)
	}
}
