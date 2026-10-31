package projects

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"fx-desktop/internal/configdir"
)

const maxRecent = 20

// Entry is a recently opened project directory.
type Entry struct {
	Path       string    `json:"path"`
	LastOpened time.Time `json:"lastOpened"`
}

// Store holds recent projects.
type Store struct {
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
	s.normalize()
	return s, nil
}

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

// Save persists recent projects.
func (s *Store) Save() error {
	s.normalize()
	path, err := configdir.ProjectsPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// List returns recent entries newest first.
func (s *Store) List() []Entry {
	s.normalize()
	return append([]Entry(nil), s.entries...)
}

// Touch records path as recently opened.
func (s *Store) Touch(path string) {
	path = filepath.Clean(path)
	now := time.Now().UTC()
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
	s.entries = nil
}
