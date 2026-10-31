package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

// The Files tab reads the workspace, not a fixed snapshot: folders are read on
// first expand and cached, collapsing keeps the listing, and refreshing keeps
// the tree the user opened.
func TestFileBrowserListsExpandsAndRefreshes(t *testing.T) {
	c := NewCoordinator()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root,
		"README.md", "zeta.txt", "src/app/main.go",
	)
	s := &Session{coord: c, project: root, shell: chrome.DefaultShell()}

	// Selecting the tab is what asks for the listing.
	c.selectInspectorTab(s, chrome.InspectorTabFiles)
	if s.shell.InspectorTab != chrome.InspectorTabFiles || !s.shell.InspectorOpen {
		t.Fatalf("tab selection: %+v", s.shell)
	}
	d := waitForFiles(t, c, s, func(d chrome.FileBrowserData) bool {
		return !d.Loading && len(d.Rows) == 3
	})
	want := []string{"src", "README.md", "zeta.txt"}
	for i, name := range want {
		if d.Rows[i].Name != name {
			t.Fatalf("row %d = %q, want %q (all: %v)", i, d.Rows[i].Name, name, rowNames(d.Rows))
		}
	}
	if !d.Rows[0].Dir || d.Rows[1].Dir || d.Rows[2].Dir {
		t.Fatalf("folders must come before files: %v", rowNames(d.Rows))
	}
	if d.Rows[0].Path != "src" || d.Rows[1].Path != "README.md" {
		t.Fatalf("workspace-relative paths: %q, %q", d.Rows[0].Path, d.Rows[1].Path)
	}

	// Expanding a folder inserts its children under it, indented one level.
	c.toggleFileDir(s, "src")
	d = waitForFiles(t, c, s, func(d chrome.FileBrowserData) bool {
		return len(d.Rows) == 4 && len(d.Rows[0].Note) == 0
	})
	if !d.Rows[0].Expanded {
		t.Fatal("src did not stay expanded")
	}
	if got := d.Rows[1]; got.Name != "app" || !got.Dir || got.Depth != 1 {
		t.Fatalf("nested folder row: %+v", got)
	}
	if got := d.Rows[1].Path; got != "src/app" {
		t.Fatalf("nested path = %q, want src/app", got)
	}

	// Collapsing keeps the cached listing but hides the children.
	c.toggleFileDir(s, "src")
	d = waitForFiles(t, c, s, func(d chrome.FileBrowserData) bool { return len(d.Rows) == 3 })
	if d.Rows[0].Expanded {
		t.Fatal("src stayed expanded after a second click")
	}

	// A refresh re-reads the disk and keeps both expansions.
	c.toggleFileDir(s, "src")
	waitForFiles(t, c, s, func(d chrome.FileBrowserData) bool { return len(d.Rows) == 4 })
	c.toggleFileDir(s, "src/app")
	waitForFiles(t, c, s, func(d chrome.FileBrowserData) bool { return len(d.Rows) == 5 })
	writeFiles(t, root, "src/app/notes.md")
	c.refreshFiles(s)
	d = waitForFiles(t, c, s, func(d chrome.FileBrowserData) bool {
		return hasRow(d.Rows, "notes.md") && d.Rows[0].Expanded
	})
	if got := rowDepth(d.Rows, "notes.md"); got != 2 {
		t.Fatalf("notes.md is at depth %d, want 2", got)
	}
}

func TestFileBrowserRowsDoNotReadWhileTabIsClosed(t *testing.T) {
	c := NewCoordinator()
	root := t.TempDir()
	writeFiles(t, root, "a.txt")
	s := &Session{coord: c, project: root, shell: chrome.DefaultShell()}

	d := c.sessionFiles(s)
	if d.Loading || len(d.Rows) != 0 {
		t.Fatalf("a closed tab read the disk: %+v", d)
	}
	if s.files != nil {
		t.Fatal("a closed tab built a browser")
	}
}

func TestReadFileEntriesOrdersFoldersThenNames(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, "alpha.txt", "Zeta.txt", "beta.txt")

	entries, total, err := readFileEntries(root)
	if err != nil || total != 4 || len(entries) != 4 {
		t.Fatalf("read: entries=%v total=%d err=%v", entries, total, err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.name
	}
	want := []string{"Beta", "alpha.txt", "beta.txt", "Zeta.txt"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order = %v, want %v", names, want)
		}
	}
}

func writeFiles(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, path := range paths {
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func waitForFiles(t *testing.T, c *Coordinator, s *Session, cond func(chrome.FileBrowserData) bool) chrome.FileBrowserData {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d := c.sessionFiles(s)
		if cond(d) {
			return d
		}
		if time.Now().After(deadline) {
			t.Fatalf("file browser never settled: %+v", d)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func rowNames(rows []chrome.FileRow) []string {
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Name
	}
	return names
}

func hasRow(rows []chrome.FileRow, name string) bool {
	for _, row := range rows {
		if row.Name == name {
			return true
		}
	}
	return false
}

func rowDepth(rows []chrome.FileRow, name string) int {
	for _, row := range rows {
		if row.Name == name {
			return row.Depth
		}
	}
	return -1
}
