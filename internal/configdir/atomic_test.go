package configdir

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileReplacesAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prefs.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte(`{"ok":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"ok":false}` {
		t.Fatalf("file = %s", b)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("temp left behind: %v", entries)
	}
}

func TestWriteFileRenameFailureKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prefs.json")
	if err := os.WriteFile(path, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := renameFile
	renameFile = func(string, string) error { return errors.New("rename failed") }
	t.Cleanup(func() { renameFile = orig })
	if err := WriteFile(path, []byte(`{"ok":false}`), 0o644); err == nil {
		t.Fatal("expected rename failure")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"ok":true}` {
		t.Fatalf("old file was replaced: %s", b)
	}
}

func TestWriteFileMissingDir(t *testing.T) {
	err := WriteFile(filepath.Join(t.TempDir(), "missing", "prefs.json"), []byte("{}"), 0o644)
	if err == nil {
		t.Fatal("expected create failure")
	}
}
