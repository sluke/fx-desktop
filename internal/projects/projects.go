package projects

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
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
	writeMu sync.Mutex // serializes disk writes so an older Save cannot land last
	entries []Entry
	// probing is set while an identity lookup is waiting on the filesystem.
	probing atomic.Bool
}

// statPath is os.Stat unless a test replaces it.
var statPath = os.Stat

// identityBudget bounds how long Touch waits to learn which entries name the
// same folder. Touch runs on the app loop when a project is opened, and an
// entry can live on a volume that no longer answers.
const identityBudget = 250 * time.Millisecond

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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
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
	return configdir.WriteFile(path, b, 0o644)
}

// List returns recent entries newest first. Entries are normalized by Touch,
// Save and Load, so this is a copy under a read lock — no sorting, no rewrite.
func (s *Store) List() []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Entry(nil), s.entries...)
}

// Touch records path as recently opened.
//
// An entry that names the same directory under another spelling — through a
// symbolic link, or in another letter case on a case-insensitive volume — is
// replaced, not kept beside the new one: one folder is one recent project.
// The filesystem is asked before the lock is taken and within identityBudget:
// when it does not answer in time the entries are matched by their cleaned
// path alone, which is also how an entry whose folder is gone is matched.
func (s *Store) Touch(path string) {
	path = filepath.Clean(path)
	now := time.Now().UTC()
	same := s.sameFolder(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	var rest []Entry
	for _, e := range s.entries {
		if filepath.Clean(e.Path) != path && !same[e.Path] {
			rest = append(rest, e)
		}
	}
	s.entries = append([]Entry{{Path: path, LastOpened: now}}, rest...)
	s.normalize()
}

// sameFolder names the entries that are the same directory as path. The
// lookup runs on its own goroutine so a volume that has stopped answering
// costs the caller at most identityBudget; while one lookup is still waiting,
// another is not started behind it.
func (s *Store) sameFolder(path string) map[string]bool {
	if !s.probing.CompareAndSwap(false, true) {
		return nil
	}
	entries := s.List()
	found := make(chan map[string]bool, 1)
	go func() {
		same := map[string]bool{}
		if info, err := statPath(path); err == nil {
			for _, e := range entries {
				if other, err := statPath(e.Path); err == nil && os.SameFile(info, other) {
					same[e.Path] = true
				}
			}
		}
		// Cleared before the answer is handed over, so the caller's next
		// lookup is never refused by this one.
		s.probing.Store(false)
		found <- same
	}()
	timer := time.NewTimer(identityBudget)
	defer timer.Stop()
	select {
	case same := <-found:
		return same
	case <-timer.C:
		return nil
	}
}

// Clear removes all recent entries.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = nil
}
