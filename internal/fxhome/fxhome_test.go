package fxhome

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRootUsesFXHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FX_HOME", root)
	got, err := Root()
	if err != nil || got != root {
		t.Fatalf("Root() = %q, %v", got, err)
	}
}

func TestRootRejectsRelativeFXHome(t *testing.T) {
	t.Setenv("FX_HOME", "relative/fx")
	if _, err := Root(); err == nil {
		t.Fatal("relative FX_HOME was accepted")
	}
}

func TestRootDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FX_HOME", "")
	got, err := Root()
	if err != nil || got != filepath.Join(home, ".fx") {
		t.Fatalf("Root() = %q, %v", got, err)
	}
}

// fx keeps its state private to the user. A state root that is the whole
// disk, the home folder, or a regular file must fail instead of becoming the
// anchor for reads and deletes (H4).
func TestRootRejectsForbiddenRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"root": "/",
		"home": home,
		"file": file,
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("FX_HOME", path)
			if got, err := Root(); err == nil {
				t.Fatalf("accepted forbidden state root %q", got)
			}
		})
	}
}

// A link that resolves to a forbidden root is that root, not a safe alias.
func TestRootRejectsSymlinkAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need a privilege on Windows")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_HOME", link)
	if got, err := Root(); err == nil {
		t.Fatalf("accepted home folder through a link: %q", got)
	}
	rootLink := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink("/", rootLink); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_HOME", rootLink)
	if got, err := Root(); err == nil {
		t.Fatalf("accepted filesystem root through a link: %q", got)
	}
}

// fx creates the folder on first use, so a legal path that does not exist yet
// must be accepted — and returned cleaned.
func TestRootAcceptsMissingFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("absolute-path fixture is POSIX")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(t.TempDir(), "not", "created", "yet")
	t.Setenv("FX_HOME", filepath.Join(want, ".", "..", "yet"))
	if got, err := Root(); err != nil || got != want {
		t.Fatalf("Root() = %q, %v; want %q", got, err, want)
	}
}

// FX_HOME works when HOME is not set: the default is the only branch that
// needs a home folder.
func TestRootWorksWithoutHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("FX_HOME", root)
	if got, err := Root(); err != nil || got != root {
		t.Fatalf("Root() = %q, %v", got, err)
	}
}

// A set value is used verbatim. Trimming it would silently point the app at a
// different folder than fx itself uses.
func TestRootDoesNotTrimValue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX whitespace fixture")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A trailing space is part of a directory name.
	base := t.TempDir()
	withSpace := filepath.Join(base, "fx ") // name keeps the space
	if err := os.Mkdir(withSpace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_HOME", withSpace)
	if got, err := Root(); err != nil || got != withSpace {
		t.Fatalf("Root() = %q, %v; want %q", got, err, withSpace)
	}
	// A value that is only spaces is not an absolute path.
	t.Setenv("FX_HOME", "   ")
	if _, err := Root(); err == nil {
		t.Fatal("blank FX_HOME was accepted")
	}
}

// An unreadable ancestor is a real failure, not "not created yet": the app
// must report it rather than guess the folder's type.
func TestRootReportsPermissionError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission fixture needs a non-root POSIX user")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
	t.Setenv("FX_HOME", filepath.Join(locked, "state"))
	if _, err := Root(); err == nil {
		t.Fatal("unreadable FX_HOME ancestor was accepted")
	}
}
