package projects

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fx-desktop/internal/configdir"
)

const maxRecent = 20

// Entry is a recently opened project directory.
type Entry struct {
	Path       string    `json:"path"`
	LastOpened time.Time `json:"lastOpened"`
}

// Store holds recent projects. The app loop reads it on every frame while a
// project can be opened from a background path, so every access is guarded.
// List is a read-only RLock; normalize is only reached from the write paths,
// never from a frame.
type Store struct {
	mu      sync.RWMutex
	entries []Entry
}

// Load reads recent projects from disk.
func Load() (*Store, error) {
	s := &Store{}
	path, err := configdir.ProjectsPath()
	if err != nil {
		return s, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	if err := json.Unmarshal(b, &s.entries); err != nil {
		return &Store{}, err
	}
	s.mu.Lock()
	s.normalize()
	s.mu.Unlock()
	return s, nil
}

// normalize deduplicates, sorts and caps entries. The caller holds the write
// lock, or owns a Store that has not been published yet (a test fixture).
// It is deliberately not called from List: a frame must not write shared state.
func (s *Store) normalize() {
	var out []Entry
	seen := map[string]bool{}
	for _, e := range s.entries {
		if e.Path == "" || seen[e.Path] {
			continue
		}
		seen[e.Path] = true
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastOpened.After(out[j].LastOpened)
	})
	if len(out) > maxRecent {
		out = out[:maxRecent]
	}
	s.entries = out
}

// Save persists recent projects. The disk write happens outside the lock: a
// slow filesystem must not hold up a frame that only wants to read the list.
func (s *Store) Save() error {
	s.mu.Lock()
	s.normalize()
	entries := append([]Entry(nil), s.entries...)
	s.mu.Unlock()
	path, err := configdir.ProjectsPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// List returns recent entries newest first. Entries are normalized by Touch,
// Save and Load, so this is a copy under a read lock — no sorting, no rewrite.
func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Entry(nil), s.entries...)
}

// Touch records path as recently opened.
func (s *Store) Touch(path string) {
	path = filepath.Clean(path)
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	var rest []Entry
	for _, e := range s.entries {
		if filepath.Clean(e.Path) != path {
			rest = append(rest, e)
		}
	}
	s.entries = append([]Entry{{Path: path, LastOpened: now}}, rest...)
	s.normalize()
}

// Clear removes all recent entries.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
}
