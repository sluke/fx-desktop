package fxprofile

// P5 cancellation, batching and cost tests.
// TestCanceledEmptyScanIsPartial was migrated from the r5 review probe
// TestReviewR5CanceledEmptyScanIsPartial.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A scan whose context was cancelled before it began has established only
// the built-in profiles. Reporting that as the complete set is a lie.
func TestCanceledEmptyScanIsPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := ScanContext(ctx, t.TempDir(), "")
	if !r.Partial {
		t.Fatalf("pre-canceled scan reported complete: %+v", r)
	}
}

// countingCtx cancels after a fixed number of Err() observations. It makes
// "cancelled between two directory batches" deterministic instead of a race
// against a timer.
type countingCtx struct {
	mu    sync.Mutex
	calls int
	limit int
}

func (c *countingCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *countingCtx) Done() <-chan struct{}       { return nil }
func (c *countingCtx) Value(any) any               { return nil }
func (c *countingCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls > c.limit {
		return context.Canceled
	}
	return nil
}

func makeAgentDirs(t testing.TB, state string, n int) {
	t.Helper()
	root := filepath.Join(state, "agents")
	for i := 0; i < n; i++ {
		dir := filepath.Join(root, fmt.Sprintf("agent%05d", i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(`{"description":"fixture"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Every early return observes cancellation: missing folder, empty folder and
// a folder with entries.
func TestScanCancellationAtEveryEarlyReturn(t *testing.T) {
	for _, n := range []int{0, 1, 200} {
		t.Run(fmt.Sprintf("entries-%d", n), func(t *testing.T) {
			state := t.TempDir()
			if n > 0 {
				makeAgentDirs(t, state, n)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			r := ScanContext(ctx, state, t.TempDir())
			if !r.Partial || len(r.Problems) == 0 {
				t.Fatalf("pre-canceled scan reported complete: %+v", r)
			}
			if len(r.Profiles) != 2 {
				t.Fatalf("a pre-canceled scan read %d profiles, want built-ins only", len(r.Profiles))
			}
		})
	}
}

// A cancellation that lands after the first batch must stop before the
// second one, leaving a partial list with a reason.
func TestScanCancelsBetweenBatches(t *testing.T) {
	state := t.TempDir()
	const total = 200
	makeAgentDirs(t, state, total)
	ctx := &countingCtx{limit: 4 + profileDirBatch + 1}
	r := ScanContext(ctx, state, "")
	if !r.Partial || len(r.Problems) == 0 {
		t.Fatalf("batch cancellation was not reported: %+v", r)
	}
	if got := len(r.Profiles) - 2; got >= total || got <= 0 {
		t.Fatalf("walk read %d of %d entries after cancellation", got, total)
	}
}

// A directory over the entry budget is partial, with a stable problem text.
func TestScanEntryBudgetIsPartial(t *testing.T) {
	previous := maxProfileEntries
	maxProfileEntries = 8
	t.Cleanup(func() { maxProfileEntries = previous })
	state := t.TempDir()
	makeAgentDirs(t, state, 20)
	r := ScanContext(context.Background(), state, "")
	if !r.Partial || !strings.Contains(strings.Join(r.Problems, " "), "entry budget") {
		t.Fatalf("entry budget was not reported: %+v", r)
	}
	if len(r.Profiles) != 2+8 {
		t.Fatalf("budget produced %d profiles, want 10", len(r.Profiles))
	}
}

// Batched reads must still produce a deterministic name order, and two
// scans racing on the same state must agree.
func TestScanBatchOrderIsStableAcrossConcurrentScans(t *testing.T) {
	state := t.TempDir()
	makeAgentDirs(t, state, 150)
	for _, name := range []string{"zzz", "aaa", "mmm"} {
		if err := os.MkdirAll(filepath.Join(state, "agents", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	want := ScanContext(context.Background(), state, "")
	if want.Partial {
		t.Fatalf("baseline scan was partial: %+v", want.Problems)
	}
	var names []string
	for _, p := range want.Profiles[2:] {
		names = append(names, p.Name)
	}
	if !sortedNames(names) {
		t.Fatalf("profiles are not in name order: %v", names[:min(8, len(names))])
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := ScanContext(context.Background(), state, "")
			var gotNames []string
			for _, p := range got.Profiles[2:] {
				gotNames = append(gotNames, p.Name)
			}
			if !reflect.DeepEqual(gotNames, names) {
				t.Errorf("concurrent scan order differed: %v", gotNames[:min(8, len(gotNames))])
			}
		}()
	}
	wg.Wait()
}

func sortedNames(names []string) bool {
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			return false
		}
	}
	return true
}

// A prompt that fx accepts must produce a real bounded preview. The old
// benchmarks only exercised files that fx refuses (1/16 MiB), so they could
// not prove the valid path's cost or correctness.
func TestPromptPreviewForAcceptedSizes(t *testing.T) {
	for _, size := range []int{4 << 10, 64 << 10, 256 << 10} {
		t.Run(fmt.Sprintf("%dKiB", size>>10), func(t *testing.T) {
			state := t.TempDir()
			dir := filepath.Join(state, "agents", "ok")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(`{"description":"fixture"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(strings.Repeat("a", size)), 0o600); err != nil {
				t.Fatal(err)
			}
			p := findProfile(t, state, "", "user", "ok")
			if p.Problem != "" || len(p.PromptPreview) != maxPromptPreviewBytes {
				t.Fatalf("valid prompt: problem=%q preview=%d bytes", p.Problem, len(p.PromptPreview))
			}
		})
	}
}

func BenchmarkProfileScanEntries(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		state := b.TempDir()
		makeAgentDirs(b, state, n) // fixture outside the timer
		b.Run(fmt.Sprintf("%d-entries", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = ScanContext(context.Background(), state, "")
			}
		})
	}
}

// A pre-cancelled walk must cost the same whether the directory has 10 or
// 10,000 entries: no ReadDir, no sort, no profile reads.
func BenchmarkProfileScanPreCanceled(b *testing.B) {
	state := b.TempDir()
	makeAgentDirs(b, state, 10000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.ReportAllocs()
	for b.Loop() {
		_ = ScanContext(ctx, state, "")
	}
}

func BenchmarkProfilePromptPreview(b *testing.B) {
	for _, size := range []int{4 << 10, 64 << 10, 256 << 10, 256<<10 + 1} {
		state := b.TempDir()
		dir := filepath.Join(state, "agents", "ok")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "profile.json"), []byte(`{"description":"fixture"}`), 0o600); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(strings.Repeat("a", size)), 0o600); err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("%dKiB", size>>10), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = ScanContext(context.Background(), state, "")
			}
		})
	}
}

// Retained heap after one full 10k-entry scan, measured with GC forced on
// both sides. Not a process RSS figure.
func TestProfileScanRetainedHeapIsBounded(t *testing.T) {
	state := t.TempDir()
	makeAgentDirs(t, state, 10000)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	result := ScanContext(context.Background(), state, "")
	runtime.GC()
	runtime.ReadMemStats(&after)
	if result.Partial {
		t.Fatalf("10k-entry scan was partial: %+v", result.Problems)
	}
	if after.Alloc > before.Alloc+64<<20 {
		t.Fatalf("retained heap grew by %d MiB for one scan", (after.Alloc-before.Alloc)>>20)
	}
	t.Logf("10k-entry scan retained ~%d KiB; %d profiles", (after.Alloc-before.Alloc)>>10, len(result.Profiles))
}
