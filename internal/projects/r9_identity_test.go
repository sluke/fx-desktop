package projects

import (
	"os"
	"path/filepath"
	"testing"
)

// one directory reached through two spellings is two recent projects.
func TestRegressionR9_TouchSameDirectoryTwoSpellings(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "Proj")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Run("symlink", func(t *testing.T) {
		alias := filepath.Join(base, "alias")
		if err := os.Symlink(real, alias); err != nil {
			t.Fatal(err)
		}
		s := &Store{}
		s.Touch(real)
		s.Touch(alias)
		if got := s.List(); len(got) != 1 {
			t.Errorf("%d recent entries for one directory: %+v", len(got), got)
		}
	})
	t.Run("case", func(t *testing.T) {
		lower := filepath.Join(base, "proj")
		a, errA := os.Stat(real)
		b, errB := os.Stat(lower)
		if errA != nil || errB != nil || !os.SameFile(a, b) {
			t.Skip("case-sensitive filesystem")
		}
		s := &Store{}
		s.Touch(real)
		s.Touch(lower)
		if got := s.List(); len(got) != 1 {
			t.Errorf("%d recent entries for one directory: %+v", len(got), got)
		}
	})
}

// Different folders stay different entries, and an entry whose folder is
// gone is still replaced when the same spelling is opened again.
func TestRegressionR9_TouchKeepsDistinctAndMissingFolders(t *testing.T) {
	base := t.TempDir()
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	for _, dir := range []string{a, b} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{}
	s.Touch(a)
	s.Touch(b)
	gone := filepath.Join(base, "gone")
	s.Touch(gone)
	s.Touch(gone + string(filepath.Separator))
	got := s.List()
	if len(got) != 3 || got[0].Path != gone {
		t.Fatalf("recent = %+v, want gone, b, a", got)
	}
	s.Touch(a)
	if got := s.List(); len(got) != 3 || got[0].Path != a {
		t.Fatalf("after reopening a: %+v", got)
	}
}
