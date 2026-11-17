package configdir

import (
	"os"
	"path/filepath"
	"testing"

	"fx-desktop/internal/testutil"
)

// a config file that is a symlink (dotfiles checkout) is replaced by a
// regular file; the link target keeps the old bytes.
func TestRegressionR9_WriteFileKeepsSymlinkedConfig(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(t.TempDir(), "dotfiles-prefs.json")
	if err := os.WriteFile(real, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte(`{"new":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("config path is no longer a symlink (mode %v)", info.Mode())
	}
	got, _ := os.ReadFile(real)
	if string(got) != `{"new":true}` {
		t.Errorf("link target holds %q, want the new document", got)
	}
}

// A link whose target cannot be resolved is replaced by the document, as a
// plain rename always did, and an ordinary file is still replaced atomically
// with the requested mode.
func TestRegressionR9_WriteFileDanglingLinkAndPlainFile(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := ProjectsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing.json"), path); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte(`[1]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != `[1]` {
		t.Fatalf("after writing over a dangling link: %q, %v", got, err)
	}

	plain, err := SessionStatsPath()
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"a":1}`, `{"a":2}`} {
		if err := WriteFile(plain, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(plain)
		info, _ := os.Stat(plain)
		if string(got) != body || info.Mode().Perm() != 0o600 {
			t.Fatalf("plain write = %q mode %v", got, info.Mode().Perm())
		}
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(plain), ".*.tmp"))
	if len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}
