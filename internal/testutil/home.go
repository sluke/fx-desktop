package testutil

import (
	"path/filepath"
	"testing"
)

// IsolateHome points HOME (and XDG_CONFIG_HOME on Linux) at a fresh directory.
func IsolateHome(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("FX_DESKTOP_FX_PATH", "")
	t.Setenv("PATH", filepath.Join(dir, "empty-path"))
	return dir
}
