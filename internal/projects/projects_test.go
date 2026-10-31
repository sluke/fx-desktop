package projects

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"fx-desktop/internal/configdir"
	"fx-desktop/internal/testutil"
)

func TestTouchAndMaxRecent(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		s.Touch(filepath.Join(dir, "p", string(rune('a'+i))))
	}
	if len(s.List()) > 20 {
		t.Fatalf("expected at most 20 entries, got %d", len(s.List()))
	}
	s.Touch(filepath.Join(dir, "latest"))
	if s.List()[0].Path != filepath.Join(dir, "latest") {
		t.Fatalf("expected latest first, got %q", s.List()[0].Path)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.List()) == 0 {
		t.Fatal("expected persisted entries")
	}
}

func TestTouchPromotesExisting(t *testing.T) {
	testutil.IsolateHome(t)
	s := &Store{}
	a := filepath.Join(t.TempDir(), "alpha")
	b := filepath.Join(t.TempDir(), "beta")
	s.Touch(a)
	s.Touch(b)
	s.Touch(a)
	list := s.List()
	if len(list) != 2 || list[0].Path != a {
		t.Fatalf("expected %q first, got %v", a, list)
	}
}

func TestNormalizeDedupesAndSorts(t *testing.T) {
	s := &Store{
		entries: []Entry{
			{Path: "/b", LastOpened: time.Unix(1, 0)},
			{Path: "/a", LastOpened: time.Unix(3, 0)},
			{Path: "/b", LastOpened: time.Unix(2, 0)},
			{Path: "", LastOpened: time.Unix(9, 0)},
		},
	}
	s.normalize()
	list := s.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	if list[0].Path != "/a" || list[1].Path != "/b" {
		t.Fatalf("order wrong: %v", list)
	}
}

func TestClear(t *testing.T) {
	testutil.IsolateHome(t)
	s := &Store{}
	s.Touch("/tmp/x")
	s.Clear()
	if len(s.List()) != 0 {
		t.Fatal("expected empty after clear")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := configdir.ProjectsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("["), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
	if len(s.List()) != 0 {
		t.Fatal("expected empty store on bad json")
	}
}

func TestSaveCreatesFile(t *testing.T) {
	testutil.IsolateHome(t)
	s := &Store{}
	s.Touch(filepath.Join(t.TempDir(), "proj"))
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	path, err := configdir.ProjectsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestSaveReportsErrorWhenConfigDirIsBlocked(t *testing.T) {
	home := testutil.IsolateHome(t)
	root := filepath.Join(home, "Library", "Application Support", "fx-desktop")
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Store{}
	s.Touch(filepath.Join(home, "proj"))
	if err := s.Save(); err == nil {
		t.Fatal("expected a save error")
	}
}

func TestStoreCapsAndDropsStaleEntries(t *testing.T) {
	testutil.IsolateHome(t)
	s := &Store{}
	now := time.Now()
	// More than maxRecent entries, plus a duplicate and an empty path.
	for i := 0; i < maxRecent+6; i++ {
		s.entries = append(s.entries, Entry{
			Path:       filepath.Join("/p", "proj-"+strconv.Itoa(i)),
			LastOpened: now.Add(time.Duration(i) * time.Minute),
		})
	}
	s.entries = append(s.entries,
		Entry{Path: filepath.Join("/p", "proj-0"), LastOpened: now.Add(time.Hour)},
		Entry{Path: "", LastOpened: now},
	)
	s.normalize()
	if len(s.entries) != maxRecent {
		t.Fatalf("expected %d entries, got %d", maxRecent, len(s.entries))
	}
	seen := map[string]bool{}
	var last time.Time
	for _, e := range s.entries {
		if e.Path == "" || seen[e.Path] {
			t.Fatalf("stale entry survived: %+v", e)
		}
		seen[e.Path] = true
		if !last.IsZero() && e.LastOpened.After(last) {
			t.Fatalf("entries are not newest-first: %+v", s.entries)
		}
		last = e.LastOpened
	}
}

func TestLoadIsSafeOnEmptyStore(t *testing.T) {
	testutil.IsolateHome(t)
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 0 {
		t.Fatalf("expected an empty store, got %v", s.List())
	}
	s.Touch("/p/a")
	if got := s.List(); len(got) != 1 || got[0].Path != "/p/a" {
		t.Fatalf("after touch: %v", got)
	}
}
