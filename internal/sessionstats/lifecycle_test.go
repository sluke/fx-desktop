package sessionstats

import (
	"context"
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