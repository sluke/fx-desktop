package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// textEditorCommand is the platform's way to open a path in its default text
// editor, as distinct from the application that owns the file's type. macOS
// has a direct verb for that; a Linux desktop publishes its text/plain
// handler, which is launched with the path.
func textEditorCommand(path string) (*exec.Cmd, error) {
	return textEditorCommandFor(runtime.GOOS, path)
}

// textEditorCommandFor separates the platform choice from runtime.GOOS so the
// macOS command can be checked on every machine the tests run on.
func textEditorCommandFor(goos, path string) (*exec.Cmd, error) {
	if goos == "darwin" {
		return exec.Command("open", "-t", path), nil
	}
	return linuxTextEditorCommand(path)
}

// linuxTextEditorCommand asks xdg for the desktop's text/plain handler. It
// prefers gtk-launch, which resolves a desktop id in the application paths,
// and falls back to gio launch with the .desktop file it finds.
func linuxTextEditorCommand(path string) (*exec.Cmd, error) {
	out, err := exec.Command("xdg-mime", "query", "default", "text/plain").Output()
	if err != nil {
		return nil, fmt.Errorf("no text editor is registered for text files")
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return nil, fmt.Errorf("no text editor is registered for text files")
	}
	if gtk, err := exec.LookPath("gtk-launch"); err == nil {
		return exec.Command(gtk, id, path), nil
	}
	if gio, err := exec.LookPath("gio"); err == nil {
		if desktop, err := findDesktopFile(id); err == nil {
			return exec.Command(gio, "launch", desktop, path), nil
		}
	}
	return nil, fmt.Errorf("the text editor %s could not be launched", id)
}

// findDesktopFile locates a desktop id in the XDG application directories.
func findDesktopFile(id string) (string, error) {
	var dirs []string
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		dirs = append(dirs, filepath.Join(data, "applications"))
	} else if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "share", "applications"))
	}
	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	for _, dir := range filepath.SplitList(dataDirs) {
		dirs = append(dirs, filepath.Join(dir, "applications"))
	}
	for _, dir := range dirs {
		path := filepath.Join(dir, id)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s was not found", id)
}

// launchTextEditor starts the editor without waiting for it, and reaps the
// child when it exits so a long-lived editor does not leave a zombie behind.
func launchTextEditor(path string) error {
	cmd, err := textEditorCommand(path)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
