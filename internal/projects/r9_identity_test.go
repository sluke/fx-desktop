package projects

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// An entry on a volume that has stopped answering must not hold up opening a
// project: Touch waits a bounded time for the identity lookup, records the
// path by its spelling, and does not queue a second lookup behind a stuck one.
func TestRegressionR9_TouchDoesNotWaitForAVolumeThatDoesNotAnswer(t *testing.T) {
	base := t.TempDir()
	a, b, stuck := filepath.Join(base, "a"), filepath.Join(base, "b"), filepath.Join(base, "stuck")
	for _, dir := range []string{a, b, stuck} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{}
	s.Touch(stuck)

	release := make(chan struct{})
	entered := make(chan struct{}, 8)
	original := statPath
	statPath = func(path string) (os.FileInfo, error) {
		if path == stuck {
			entered <- struct{}{}
			<-release
		}
		return original(path)
	}
	defer func() { statPath = original }()
	released := false
	unblock := func() {
		if !released {
			released = true
			close(release)
		}
	}
	defer unblock()

	start := time.Now()
	s.Touch(a)
	first := time.Since(start)
	if first > 3*time.Second {
		t.Fatalf("Touch waited %v for a volume that does not answer", first)
	}
	if first < identityBudget/2 {
		t.Fatalf("Touch returned after %v: the lookup was expected to use its budget", first)
	}
	select {
	case <-entered:
	default:
		t.Fatal("the stuck entry was never looked up")
	}

	// A lookup is still waiting: the next Touch does not start another.
	start = time.Now()
	s.Touch(b)
	if second := time.Since(start); second > identityBudget/2 {
		t.Errorf("a second Touch waited %v behind the stuck lookup", second)
	}
	select {
	case <-entered:
		t.Error("a second lookup was started behind the stuck one")
	default:
	}

	got := s.List()
	if len(got) != 3 || got[0].Path != b || got[1].Path != a || got[2].Path != stuck {
		t.Fatalf("recent = %+v, want b, a, stuck", got)
	}

	// Once the volume answers, lookups work again.
	unblock()
	deadline := time.Now().Add(3 * time.Second)
	for s.probing.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.probing.Load() {
		t.Fatal("the lookup never finished after the volume answered")
	}
	statPath = original
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(a, alias); err != nil {
		t.Skip("symbolic links are not available:", err)
	}
	s.Touch(alias)
	if got := s.List(); len(got) != 3 || got[0].Path != alias {
		t.Fatalf("after the volume answered: %+v, want alias to replace a", got)
	}
}
