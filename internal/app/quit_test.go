package app

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"fx-desktop/internal/sessionstats"
	"fx-desktop/internal/testutil"
)

func TestBeginQuitReturnsBeforeStatsFlush(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := sessionstats.Load()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	store.SetPersistHook(func(string, map[string]sessionstats.Stats) error {
		close(started)
		<-release
		return nil
	})
	if err := store.Put("s", sessionstats.Stats{InputTokens: 3}); err != nil {
		t.Fatal(err)
	}
	<-started
	c := NewCoordinator()
	c.stats = store
	var quit atomic.Bool
	e := &mygo.QuitEvent{}
	done := make(chan struct{})
	go func() {
		c.beginQuit(e, func() { quit.Store(true) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("beginQuit blocked on the stats flush")
	}
	if !e.DefaultPrevented() {
		t.Fatal("first quit was not deferred")
	}
	if quit.Load() {
		t.Fatal("quit continued before the flush was released")
	}
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for !quit.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !quit.Load() {
		t.Fatal("quit did not continue after the flush")
	}
}

// A second quit request while the flush is in flight must be held too. The
// old code used one boolean for "the flush started" and "the app may quit",
// so the second Cmd+Q ran the quit sequence while the disk write was still
// pending (Q4).
func TestRepeatedQuitWaitsForFlush(t *testing.T) {
	store, release := blockedStatsStore(t)
	defer release()
	c := &Coordinator{stats: store}
	var quitCalls atomic.Int32
	finished := make(chan struct{})
	c.beginQuit(&mygo.QuitEvent{}, func() {
		quitCalls.Add(1)
		close(finished)
	})
	second := &mygo.QuitEvent{}
	c.beginQuit(second, func() { t.Error("a second drain was started") })
	if !second.DefaultPrevented() {
		t.Fatal("second user quit bypassed the in-flight flush")
	}
	select {
	case <-finished:
		t.Fatal("quit ran before the flush was released")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	waitClosed(t, finished, "quit did not continue after the flush")
	if got := quitCalls.Load(); got != 1 {
		t.Fatalf("quit was called %d times, want 1", got)
	}
	// Once the drain is ready, a later quit passes instead of being held
	// forever: that is the owner's completion, not a new request.
	after := &mygo.QuitEvent{}
	c.beginQuit(after, func() { t.Error("quit is invoked twice") })
	if after.DefaultPrevented() {
		t.Fatal("a quit after the drain was still deferred")
	}
	if got := quitCalls.Load(); got != 1 {
		t.Fatalf("quit was called %d times after ready, want 1", got)
	}
}

// A flush that never finishes must not trap the app: the bounded budget ends
// the drain and the owner still quits.
func TestQuitTimeoutStillReleases(t *testing.T) {
	store, release := blockedStatsStore(t)
	defer release()
	c := &Coordinator{stats: store, quitBudget: 40 * time.Millisecond}
	done := make(chan struct{})
	c.beginQuit(&mygo.QuitEvent{}, func() { close(done) })
	waitClosed(t, done, "bounded quit budget did not release the quit")
	// The background close is still waiting on the release; let it finish so
	// the store's worker does not leak into another test.
	release()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

// blockedStatsStore returns a store whose first persist blocks until release.
// store.Put has already triggered that write, so Close waits on it.
func blockedStatsStore(t *testing.T) (*sessionstats.Store, func()) {
	t.Helper()
	store := sessionstats.New(filepath.Join(t.TempDir(), "session-stats.json"))
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.SetPersistHook(func(string, map[string]sessionstats.Stats) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	})
	if err := store.Put("s", sessionstats.Stats{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("stats flush never started")
	}
	var released sync.Once
	return store, func() { released.Do(func() { close(release) }) }
}

func waitClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal(message)
	}
}
