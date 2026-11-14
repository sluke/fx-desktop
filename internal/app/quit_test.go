package app

import (
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
