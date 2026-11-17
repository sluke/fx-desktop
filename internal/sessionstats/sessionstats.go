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

	closed     bool
	stop       bool
	closeOnce  sync.Once
	closeDone  chan struct{}
	closeErr   error
	wake       chan struct{}
	workerDone chan struct{}
	waiters    []*genWait

	persistGen   uint64
	committedGen uint64
	failedGen    uint64
	lastErr      error
	// persist overrides the disk write in tests.
	persist func(path string, data map[string]Stats) error
}

// SetPersistHook replaces the snapshot writer. Tests use it to block or fail
// a write; production code leaves it unset.
func (s *Store) SetPersistHook(fn func(path string, data map[string]Stats) error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.persist = fn
	s.mu.Unlock()
}

type genWait struct {
	ch chan struct{}
}

// New returns an empty store that persists to path. An empty path keeps the
// store in memory only, which is what tests that do not care about the file
// use.
func New(path string) *Store {
	return &Store{path: path, data: make(map[string]Stats), closeDone: make(chan struct{})}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	st.UpdatedMS = time.Now().UnixMilli()
	s.data[id] = st
	s.pruneLocked()
	if s.path == "" {
		return nil
	}
	s.ensureWorkerLocked()
	s.persistGen++
	s.signalLocked()
	return nil
}

// Delete forgets each conversation and schedules a persist.
func (s *Store) Delete(ids ...string) error {
	if s == nil || len(ids) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
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
	if !changed || s.path == "" {
		return nil
	}
	s.ensureWorkerLocked()
	s.persistGen++
	s.signalLocked()
	return nil
}

func (s *Store) ensureWorkerLocked() {
	if s.path == "" || s.workerDone != nil {
		return
	}
	s.wake = make(chan struct{}, 1)
	s.workerDone = make(chan struct{})
	go s.persistLoop()
}

func (s *Store) signalLocked() {
	if s.wake == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Store) persistLoop() {
	defer close(s.workerDone)
	for {
		s.mu.Lock()
		target := s.persistGen
		idle := target <= s.committedGen || (s.failedGen >= target && target > s.committedGen)
		if s.stop && idle {
			s.mu.Unlock()
			return
		}
		need := target > s.committedGen && s.failedGen < target
		var snap map[string]Stats
		var path string
		write := s.persist
		if need {
			snap = make(map[string]Stats, len(s.data))
			for id, st := range s.data {
				snap[id] = st
			}
			path = s.path
		}
		s.mu.Unlock()
		if !need {
			<-s.wake
			continue
		}
		if write == nil {
			write = saveSnapshot
		}
		err := write(path, snap)
		s.mu.Lock()
		if err != nil {
			s.failedGen = target
			s.lastErr = err
		} else if target > s.committedGen {
			s.committedGen = target
			s.lastErr = nil
		}
		s.notifyLocked()
		s.mu.Unlock()
	}
}

func (s *Store) notifyLocked() {
	for _, w := range s.waiters {
		select {
		case w.ch <- struct{}{}:
		default:
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.ensureWorkerLocked()
	target := s.persistGen
	s.signalLocked()
	s.mu.Unlock()
	return s.waitGen(ctx, target)
}

// Close rejects later Put/Delete, flushes once, and stops the worker.
// Every caller waits on the same completion. ctx only bounds this caller;
// a timed-out caller does not cancel the in-flight snapshot.
func (s *Store) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.closeOnce.Do(func() {
		s.closed = true
		go s.finishClose()
	})
	done := s.closeDone
	s.mu.Unlock()
	select {
	case <-done:
		s.mu.Lock()
		err := s.closeErr
		s.mu.Unlock()
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Store) finishClose() {
	s.mu.Lock()
	target := s.persistGen
	wd := s.workerDone
	if wd != nil {
		s.signalLocked()
	}
	s.mu.Unlock()
	var err error
	if s.path != "" && wd != nil && target > 0 {
		err = s.waitGen(context.Background(), target)
	}
	s.mu.Lock()
	s.stop = true
	s.signalLocked()
	s.mu.Unlock()
	if wd != nil {
		<-wd
	}
	s.mu.Lock()
	s.closeErr = err
	close(s.closeDone)
	s.mu.Unlock()
}

func (s *Store) waitGen(ctx context.Context, target uint64) error {
	for {
		s.mu.Lock()
		if s.committedGen >= target {
			s.mu.Unlock()
			return nil
		}
		if s.failedGen >= target && s.lastErr != nil {
			err := s.lastErr
			s.mu.Unlock()
			return err
		}
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return err
		}
		w := &genWait{ch: make(chan struct{}, 1)}
		s.waiters = append(s.waiters, w)
		s.mu.Unlock()
		select {
		case <-w.ch:
			s.dropWaiter(w)
		case <-ctx.Done():
			s.dropWaiter(w)
			return ctx.Err()
		}
	}
}

func (s *Store) dropWaiter(w *genWait) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.waiters[:0]
	for _, cur := range s.waiters {
		if cur != w {
			kept = append(kept, cur)
		}
	}
	s.waiters = kept
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
	// One atomic writer for every app data file: same-directory temporary
	// file, sync, rename.
	return configdir.WriteFile(path, b, 0o644)
}
