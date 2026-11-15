package fxhome

// TestDefaultRootValidatesType was migrated from the r5 review probe
// TestReviewR5DefaultRootValidatesType.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The default ~/.fx is still a state root. A plain file there must be
// refused just as the same path would be if FX_HOME named it explicitly.
func TestDefaultRootValidatesType(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FX_HOME", "")
	if err := os.WriteFile(filepath.Join(home, ".fx"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := Root()
	if err == nil {
		t.Fatalf("default root accepted a file: %s", root)
	}
	if !strings.Contains(err.Error(), "folder") {
		t.Fatalf("error does not say the root is not a folder: %v", err)
	}

	// The explicit form must fail for the same reason and with the same
	// source-independent wording.
	t.Setenv("FX_HOME", filepath.Join(home, ".fx"))
	if _, explicitErr := Root(); explicitErr == nil {
		t.Fatal("explicit FX_HOME accepted the same file")
	}
}

// A valid default folder, one that does not exist yet, and a legal directory
// link all keep working; only the source in the error changes.
func TestDefaultRootAcceptsValidCandidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FX_HOME", "")
	want := filepath.Join(home, ".fx")
	got, err := Root() // missing folder is legal
	if err != nil || got != want {
		t.Fatalf("missing default root = %q, %v", got, err)
	}
	if err := os.Mkdir(want, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = Root()
	if err != nil || got != want {
		t.Fatalf("existing default root = %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		other := t.TempDir()
		if err := os.Remove(want); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(other, "state")
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, want); err != nil {
			t.Fatal(err)
		}
		if got, err := Root(); err != nil || got != want {
			t.Fatalf("linked default root = %q, %v", got, err)
		}
	}
}

// The default branch must not invent a home when HOME is missing.
func TestDefaultRootWithoutHomeReportsError(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("FX_HOME", "")
	if got, err := Root(); err == nil {
		t.Fatalf("missing HOME produced a fabricated root %q", got)
	}
}

// An error on the default path names its source, so the fix is actionable.
func TestDefaultRootErrorNamesItsSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FX_HOME", "")
	if err := os.WriteFile(filepath.Join(home, ".fx"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Root()
	if err == nil || !strings.Contains(err.Error(), "default state root") {
		t.Fatalf("default error does not name its source: %v", err)
	}
}
