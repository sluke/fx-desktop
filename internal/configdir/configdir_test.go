package configdir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"fx-desktop/internal/testutil"
)

func TestRootCreatesAppDirectory(t *testing.T) {
	home := testutil.IsolateHome(t)
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "darwin" {
		want := filepath.Join(home, "Library", "Application Support", appFolder)
		if root != want {
			t.Fatalf("root %q want %q", root, want)
		}
	} else {
		want := filepath.Join(home, ".config", appFolder)
		if root != want {
			t.Fatalf("root %q want %q", root, want)
		}
	}
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		t.Fatalf("root not a directory: %v", err)
	}
}

func TestRootIsStableAcrossCalls(t *testing.T) {
	testutil.IsolateHome(t)
	a, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("root changed between calls: %q vs %q", a, b)
	}
}

func TestDataFilePaths(t *testing.T) {
	testutil.IsolateHome(t)
	prefs, err := PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(prefs) != "prefs.json" {
		t.Fatalf("prefs path %q", prefs)
	}
	projects, err := ProjectsPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(projects) != "projects.json" {
		t.Fatalf("projects path %q", projects)
	}
	stats, err := SessionStatsPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(stats) != "session-stats.json" {
		t.Fatalf("session stats path %q", stats)
	}
	if filepath.Dir(prefs) != filepath.Dir(projects) || filepath.Dir(prefs) != filepath.Dir(stats) {
		t.Fatal("app data files should share parent dir")
	}
}

func TestRootFailsWithoutHome(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("HOME", "")
	if runtime.GOOS == "darwin" {
		// os.UserHomeDir fails with no HOME, so Root must report it
		// instead of creating a directory under an empty path.
		if _, err := Root(); err == nil {
			t.Fatal("expected an error without HOME")
		}
		return
	}
	// Linux uses XDG_CONFIG_HOME, which IsolateHome still points at home.
	_ = home
	if _, err := Root(); err != nil {
		t.Fatalf("linux root should still work: %v", err)
	}
}

func TestRootFailsWhenPathIsAFile(t *testing.T) {
	testutil.IsolateHome(t)
	// A file where the app directory should be makes MkdirAll fail.
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Root(); err == nil {
		t.Fatal("expected an error when the root path is a file")
	}
	if _, err := PrefsPath(); err == nil {
		t.Fatal("expected PrefsPath to surface the same error")
	}
}
