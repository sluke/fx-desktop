package app

// P5 skill-scan cancellation and request-ownership tests.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

// countingCtx makes cancellation land after a chosen number of Err() calls,
// so "cancelled between two directory batches" is deterministic.
type skillCountingCtx struct {
	mu    sync.Mutex
	calls int
	limit int
}

func (c *skillCountingCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *skillCountingCtx) Done() <-chan struct{}       { return nil }
func (c *skillCountingCtx) Value(any) any               { return nil }
func (c *skillCountingCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls > c.limit {
		return context.Canceled
	}
	return nil
}

func makeSkillDirs(t testing.TB, workspace string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		dir := filepath.Join(workspace, ".fx", "skills", fmt.Sprintf("skill%05d", i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestScanSkillsPreCanceledEmptyIsPartial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := scanSkills(ctx, t.TempDir(), "", "")
	if !r.Partial || len(r.Problems) == 0 || len(r.Skills) != 0 {
		t.Fatalf("pre-canceled skill scan reported complete: %+v", r)
	}
}

func TestScanSkillsCancelsBetweenBatches(t *testing.T) {
	workspace := t.TempDir()
	const total = 200
	makeSkillDirs(t, workspace, total)
	ctx := &skillCountingCtx{limit: 4 + skillDirBatch + 1}
	r := scanSkills(ctx, workspace, "", "")
	if !r.Partial || len(r.Problems) == 0 {
		t.Fatalf("batch cancellation was not reported: %+v", r)
	}
	if len(r.Skills) >= total || len(r.Skills) == 0 {
		t.Fatalf("walk read %d of %d skills after cancellation", len(r.Skills), total)
	}
}

func TestScanSkillsEntryBudgetIsPartial(t *testing.T) {
	previous := maxSkillEntries
	maxSkillEntries = 8
	t.Cleanup(func() { maxSkillEntries = previous })
	workspace := t.TempDir()
	makeSkillDirs(t, workspace, 20)
	r := scanSkills(context.Background(), workspace, "", "")
	if !r.Partial || !strings.Contains(strings.Join(r.Problems, " "), "entry budget") {
		t.Fatalf("entry budget was not reported: %+v", r)
	}
	if len(r.Skills) > 8 {
		t.Fatalf("budget produced %d skills", len(r.Skills))
	}
}

// runSkills must fix its context in the thread that received the request.
// Otherwise an old worker that starts late can cancel the newer request.
func TestRunSkillsOwnsItsScopeAtRequestTime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FX_HOME", filepath.Join(home, ".fx"))
	c := &Coordinator{}
	loop := &manualLoop{}
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell(), loop: loop.dispatch}
	s.shell.Tools.Open = true
	c.runSkills(s)
	s.scopes.mu.Lock()
	_, registered := s.scopes.cancel["skills"]
	s.scopes.mu.Unlock()
	if !registered {
		t.Fatal("skills scope was not created in the requesting thread")
	}
	loop.await(t)
	loop.drain()
}

// Closing the tools panel cancels the scans it owns, including the path
// that goes through the backdrop/Escape rather than the Close button.
func TestToolsCloseCancelsOwnedScans(t *testing.T) {
	c := &Coordinator{}
	loop := &manualLoop{}
	s := &ProjectWindow{coord: c, project: t.TempDir(), shell: chrome.DefaultShell(), loop: loop.dispatch}
	s.shell.Tools.Open = true
	skillsCtx, skillsStop := s.scope("skills")
	readCtx, readStop := s.scope("management-read")
	defer skillsStop()
	defer readStop()
	c.sessionManagement(s).Close()
	if skillsCtx.Err() == nil || readCtx.Err() == nil {
		t.Fatal("Close did not cancel the panel's scans")
	}
	s.shell.Tools.Open = false
	s.optionalHosts(c)
	second, stop := s.scope("skills")
	defer stop()
	s.optionalHosts(c)
	if second.Err() == nil {
		t.Fatal("a scan started while the panel is closed was not cancelled at the next frame")
	}
}

// Concurrent scans are bounded by the caller, not by an internal queue.
// Record the observed maximum so a regression that serialises or spawns
// unbounded workers is visible.
func TestSkillScansReportMaxConcurrency(t *testing.T) {
	workspace := t.TempDir()
	makeSkillDirs(t, workspace, 120)
	var inFlight, maxInFlight atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			now := inFlight.Add(1)
			for {
				old := maxInFlight.Load()
				if now <= old || maxInFlight.CompareAndSwap(old, now) {
					break
				}
			}
			r := scanSkills(context.Background(), workspace, "", "")
			if r.Partial || len(r.Skills) != 120 {
				t.Errorf("concurrent scan result: partial=%v skills=%d", r.Partial, len(r.Skills))
			}
			inFlight.Add(-1)
		}()
	}
	wg.Wait()
	if got := maxInFlight.Load(); got < 1 || got > 8 {
		t.Fatalf("max concurrent scans = %d, want 1..8", got)
	}
	t.Logf("max concurrent skill scans: %d", maxInFlight.Load())
}

func BenchmarkSkillScanEntries(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		workspace := b.TempDir()
		makeSkillDirs(b, workspace, n)
		b.Run(fmt.Sprintf("%d-entries", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = scanSkills(context.Background(), workspace, "", "")
			}
		})
	}
}

func BenchmarkSkillScanPreCanceled(b *testing.B) {
	workspace := b.TempDir()
	makeSkillDirs(b, workspace, 10000)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.ReportAllocs()
	for b.Loop() {
		_ = scanSkills(ctx, workspace, "", "")
	}
}
