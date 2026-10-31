package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestTextEditorCommandForDarwinUsesOpenTextFlag(t *testing.T) {
	cmd, err := textEditorCommandFor("darwin", "/p/notes.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(cmd.Path); got != "open" {
		t.Fatalf("command = %q, want open", got)
	}
	if got := strings.Join(cmd.Args, " "); got != "open -t /p/notes.md" {
		t.Fatalf("args = %q, want open -t /p/notes.md", got)
	}
}

// launchTextEditor starts the platform command and does not wait for it. The
// fake `open` records the arguments a real editor would receive.
func TestLaunchTextEditorStartsEditorWithoutWaiting(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the fake open command covers the macOS path")
	}
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + record + "\n"
	if err := os.WriteFile(filepath.Join(bin, "open"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	if err := launchTextEditor("/p/notes.md"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		data, err := os.ReadFile(record)
		if err == nil {
			if got := strings.TrimSpace(string(data)); got != "-t\n/p/notes.md" {
				t.Fatalf("open received %q, want the text flag and path", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the fake editor never ran: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestFindDesktopFileSearchesXDGDataHome(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_DATA_DIRS", "")
	id := "org.example.Editor.desktop"
	path := filepath.Join(data, "applications", id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[Desktop Entry]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findDesktopFile(id)
	if err != nil || got != path {
		t.Fatalf("findDesktopFile = %q, %v; want %q", got, err, path)
	}
	if _, err := findDesktopFile("org.example.Missing.desktop"); err == nil {
		t.Fatal("a missing desktop file was accepted")
	}
}
