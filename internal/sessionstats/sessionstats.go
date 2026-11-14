// Package sessionstats remembers the provider-confirmed usage of each fx
// conversation. fx replays no usage_update when a session is loaded, and a
// prompt result only describes the turn that just ended, so the app keeps its
// own record: a resumed conversation can show the last numbers it earned
// instead of reading as an empty one until the next turn ends.
package sessionstats

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fx-desktop/internal/configdir"
)

var ErrClosed = errors.New("session stats store closed")

// maxEntries bounds the store. A record is a few dozen bytes, but the map
// would otherwise grow with every conversation ever opened.
const maxEntries = 200

// Stats is the last known usage of one conversation. Counts are stored as
// reported; a missing value stays zero so it can never be mistaken for a
// measurement the provider did not make.
type Stats struct {
	ContextUsed  int   `json:"contextUsed,omitempty"`
	ContextTotal int   `json:"contextTotal,omitempty"`
	InputTokens  int   `json:"inputTokens,omitempty"`
	OutputTokens int   `json:"outputTokens,omitempty"`
	UpdatedMS    int64 `json:"updatedMs,omitempty"`
}

// Empty reports whether there is nothing worth remembering.
func (s Stats) Empty() bool {
	return s.ContextUsed == 0 && s.ContextTotal == 0 && s.InputTokens == 0 && s.OutputTokens == 0
}

type file struct {
	Version  int              `json:"version"`
	Sessions map[string]Stats `json:"sessions"`
}

// Store is the in-memory record mirrored to session-stats.json. Windows share
// one Store, so every method is safe for concurrent use.
type Store struct {
	mu sync.Mutex

	path string
	data map[string]Stats

	closed bool

	persistOnce sync.Once
	stopOnce    sync.Once
	persistCh   chan struct{}
	workerDone  chan struct{}
	cond        sync.Cond

	persistGen   uint64
	committedGen uint64
	lastErr      error
}

// New returns an empty store that persists to path. An empty path keeps the
// store in memory only, which is what tests that do not care about the file
// use.
func New(path string) *Store {
	s := &Store{path: path, data: make(map[string]Stats)}
	s.cond.L = &s.mu
	return s
}

// Load reads the store from the app's config directory. A missing file is an
// empty store, not an error; a damaged file is reported and starts empty, and
// the next write repairs it.
func Load() (*Store, error) {
	path, err := configdir.SessionStatsPath()
	if err != nil {
		return New(""), err
	}
	s := New(path)
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return s, err
	}
	for id, st := range f.Sessions {
		if id == "" || st.Empty() {
			continue
		}
		s.data[id] = st
	}
	s.pruneLocked()
	return s, nil
}

// Get returns id's last known usage, or the zero Stats when the conversation
// has no record. A nil store answers the zero Stats so callers can attach one
// only where persistence is wanted.
func (s *Store) Get(id string) Stats {
	if s == nil || id == "" {
		return Stats{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[id]
}

// Put records id's usage. Disk persistence is asynchronous; use Flush or Close
// when a write must be confirmed. An empty conversation is not recorded.
func (s *Store) Put(id string, st Stats) error {
	if s == nil || id == "" || st.Empty() {
		return nil
	}
	if s.path == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		st.UpdatedMS = time.Now().UnixMilli()
		s.data[id] = st
		s.pruneLocked()
		return nil
	}
	s.startPersist()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	st.UpdatedMS = time.Now().UnixMilli()
	s.data[id] = st
	s.pruneLocked()
	s.persistGen++
	s.mu.Unlock()
	s.signalPersist()
	return nil
}

// Delete forgets each conversation and schedules a persist.
func (s *Store) Delete(ids ...string) error {
	if s == nil || len(ids) == 0 {
		return nil
	}
	if s.path == "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, id := range ids {
			if id != "" {
				delete(s.data, id)
			}
		}
		return nil
	}
	s.startPersist()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	changed := false
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := s.data[id]; ok {
			delete(s.data, id)
			changed = true
		}
	}
	if !changed {
		s.mu.Unlock()
		return nil
	}
	s.persistGen++
	s.mu.Unlock()
	s.signalPersist()
	return nil
}

func (s *Store) startPersist() {
	if s == nil || s.path == "" {
		return
	}
	s.persistOnce.Do(func() {
		s.persistCh = make(chan struct{}, 1)
		s.workerDone = make(chan struct{})
		go s.persistLoop()
	})
}

func (s *Store) signalPersist() {
	if s == nil || s.persistCh == nil {
		return
	}
	select {
	case s.persistCh <- struct{}{}:
	default:
	}
}

func (s *Store) persistLoop() {
	defer close(s.workerDone)
	for range s.persistCh {
		for {
			s.mu.Lock()
			target := s.persistGen
			if target <= s.committedGen {
				s.mu.Unlock()
				break
			}
			snap := make(map[string]Stats, len(s.data))
			for id, st := range s.data {
				snap[id] = st
			}
			path := s.path
			s.mu.Unlock()

			err := saveSnapshot(path, snap)

			s.mu.Lock()
			if err != nil {
				s.lastErr = err
			} else {
				s.lastErr = nil
				if target > s.committedGen {
					s.committedGen = target
				}
			}
			stale := s.persistGen != target
			s.cond.Broadcast()
			s.mu.Unlock()
			if !stale {
				break
			}
		}
	}
}

// Flush waits until every mutation queued before this call has been persisted
// successfully or failed definitively for that generation.
func (s *Store) Flush() error {
	return s.FlushContext(context.Background())
}

// FlushContext is Flush with a deadline.
func (s *Store) FlushContext(ctx context.Context) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.startPersist()
	s.mu.Lock()
	target := s.persistGen
	s.mu.Unlock()
	s.signalPersist()
	return s.waitCommitted(ctx, target)
}

// Close flushes pending writes, stops the worker, and rejects later Put/Delete.
func (s *Store) Close(ctx context.Context) error {
	if s == nil || s.path == "" {
		return nil
	}
	s.startPersist()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	target := s.persistGen
	s.mu.Unlock()

	s.signalPersist()
	err := s.waitCommitted(ctx, target)

	s.stopOnce.Do(func() { close(s.persistCh) })

	select {
	case <-s.workerDone:
	case <-ctx.Done():
		if err == nil {
			err = ctx.Err()
		}
	}
	return err
}

func (s *Store) waitCommitted(ctx context.Context, target uint64) error {
	done := make(chan error, 1)
	go func() {
		s.mu.Lock()
		for s.committedGen < target && s.lastErr == nil {
			s.cond.Wait()
		}
		var err error
		if s.committedGen < target {
			err = s.lastErr
		}
		s.mu.Unlock()
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Len reports how many conversations are remembered. It exists so the cap can
// be tested without reaching into the file format.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

func (s *Store) pruneLocked() {
	if len(s.data) <= maxEntries {
		return
	}
	type entry struct {
		id string
		ms int64
	}
	entries := make([]entry, 0, len(s.data))
	for id, st := range s.data {
		entries = append(entries, entry{id: id, ms: st.UpdatedMS})
	}
	// The oldest write goes first. Ties are broken by id so the survivor set
	// is deterministic rather than map-order dependent.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].ms != entries[j].ms {
			return entries[i].ms < entries[j].ms
		}
		return entries[i].id < entries[j].id
	})
	for _, e := range entries[:len(entries)-maxEntries] {
		delete(s.data, e.id)
	}
}

func saveSnapshot(path string, data map[string]Stats) error {
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(file{Version: 1, Sessions: data}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".session-stats-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(name)
	}
	if _, err := tmp.Write(b); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}