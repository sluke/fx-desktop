package sessionstats

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"fx-desktop/internal/testutil"
)

func TestStoreCloseStopsWorker(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("a", Stats{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("b", Stats{InputTokens: 2}); err != ErrClosed {
		t.Fatalf("Put after Close: %v", err)
	}
}

func TestFlushAfterCloseDoesNotPanic(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put("a", Stats{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Flush after Close = %v", err)
	}
}

func TestMemoryStoreCloseRejectsPut(t *testing.T) {
	store := New("")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("a", Stats{InputTokens: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Put after memory Close = %v", err)
	}
}

func TestCanceledFlushDoesNotLeakWaiters(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	store.SetPersistHook(func(string, map[string]Stats) error {
		close(started)
		<-release
		return nil
	})
	if err := store.Put("a", Stats{InputTokens: 1}); err != nil {
		t.Fatal(err)
	}
	<-started
	before := runtime.NumGoroutine()
	for i := 0; i < 8; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := store.FlushContext(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled flush: %v", err)
		}
	}
	if runtime.NumGoroutine() > before+3 {
		t.Fatalf("waiters leaked: before %d now %d", before, runtime.NumGoroutine())
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestInMemoryStoreHasNoBackgroundWorker(t *testing.T) {
	store := New("")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := store.Put("x", Stats{InputTokens: n}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if store.Len() != 1 {
		t.Fatalf("unexpected records: %d", store.Len())
	}
}
