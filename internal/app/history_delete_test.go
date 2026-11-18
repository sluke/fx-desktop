package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"
)

// historyDeleteRig drives History deletion without a native window: the app
// loop is a queue the test drains, and the list and the disk are fakes the
// test can hold at each stage (r10 §2).
type historyDeleteRig struct {
	t *testing.T
	c *Coordinator
	s *ProjectWindow

	loop chan func()

	mu       sync.Mutex
	lists    int
	removes  [][]string
	alerts   []string
	informs  []string
	listed   []FxSession
	listErr  error
	removeEr error

	// listGate and removeGate, when non-nil, hold the fake until the test
	// sends on them. entered reports that the fake reached its gate.
	listGate, removeGate chan struct{}
	entered              chan string
}

func newHistoryDeleteRig(t *testing.T) *historyDeleteRig {
	t.Helper()
	r := &historyDeleteRig{t: t, c: &Coordinator{}, s: &ProjectWindow{}, loop: make(chan func(), 16), entered: make(chan string, 16)}
	r.s.loop = func(fn func()) { r.loop <- fn }
	r.c.historySeams = historyDeleteSeams{
		list: func(ctx context.Context, _, _ string, _ bool) ([]FxSession, error) {
			r.mu.Lock()
			r.lists++
			gate := r.listGate
			r.mu.Unlock()
			if gate != nil {
				r.entered <- "list"
				select {
				case <-gate:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			return slices.Clone(r.listed), r.listErr
		},
		remove: func(ids []string) ([]string, error) {
			r.mu.Lock()
			r.removes = append(r.removes, slices.Clone(ids))
			gate := r.removeGate
			r.mu.Unlock()
			if gate != nil {
				r.entered <- "remove"
				<-gate
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.removeEr != nil {
				return nil, r.removeEr
			}
			return ids, nil
		},
		alert: func(title, message string) {
			r.mu.Lock()
			r.alerts = append(r.alerts, title+": "+message)
			r.mu.Unlock()
		},
		inform: func(_ *ProjectWindow, title, message, _ string) {
			r.mu.Lock()
			r.informs = append(r.informs, title+": "+message)
			r.mu.Unlock()
		},
	}
	return r
}

func (r *historyDeleteRig) wait(stage string) {
	r.t.Helper()
	select {
	case got := <-r.entered:
		if got != stage {
			r.t.Fatalf("worker reached %q, want %q", got, stage)
		}
	case <-time.After(5 * time.Second):
		r.t.Fatalf("worker never reached %q", stage)
	}
}

// step runs the next callback a worker handed to the app loop.
func (r *historyDeleteRig) step() {
	r.t.Helper()
	select {
	case fn := <-r.loop:
		fn()
	case <-time.After(5 * time.Second):
		r.t.Fatal("no callback reached the app loop")
	}
}

func (r *historyDeleteRig) counts() (lists, removes, alerts, informs int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lists, len(r.removes), len(r.alerts), len(r.informs)
}

func (r *historyDeleteRig) removed(i int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.removes[i])
}

// reopen is what closing the panel and opening it again does to its state.
func (r *historyDeleteRig) reopen() {
	r.s.shell.History.Open = false
	r.c.openHistory(r.s)
}

func (r *historyDeleteRig) mustBeIdle() {
	r.t.Helper()
	if r.s.historyDelete.busy() || r.s.shell.History.Deleting {
		r.t.Fatalf("window still busy: gate=%+v panel=%v", r.s.historyDelete, r.s.shell.History.Deleting)
	}
	if held := r.c.sessionUse.snapshot(); len(held) != 0 {
		r.t.Fatalf("claims survived the deletion: %v", held)
	}
}

// Reopening the panel while a deletion runs keeps the delete controls locked,
// and a second deletion started from the reopened panel never reaches the
// disk or the registry.
func TestHistoryReopenKeepsDeleteGuardDuringWrite(t *testing.T) {
	r := newHistoryDeleteRig(t)
	r.removeGate = make(chan struct{})
	r.c.openHistory(r.s)
	if r.s.shell.History.Deleting {
		t.Fatal("a fresh panel opened as deleting")
	}
	if !r.c.startHistoryDelete(r.s, "first") {
		t.Fatal("the first deletion was refused")
	}
	r.wait("remove")

	r.reopen()
	if !r.s.shell.History.Open || !r.s.shell.History.Deleting {
		t.Fatalf("reopening cleared the in-flight deletion guard: %+v", r.s.shell.History)
	}
	if r.c.startHistoryDelete(r.s, "second") {
		t.Fatal("a second deletion started while the first was writing")
	}
	if r.c.startHistoryClear(r.s, false) {
		t.Fatal("a clear-all started while a deletion was writing")
	}
	if lists, removes, _, _ := r.counts(); lists != 0 || removes != 1 {
		t.Fatalf("lists=%d removes=%d, want 0 and 1", lists, removes)
	}
	if r.c.sessionUse.inUse("second") {
		t.Fatal("the refused deletion left a claim on its ID")
	}
	if !r.c.sessionUse.inUse("first") {
		t.Fatal("the running deletion lost its claim")
	}

	r.removeGate <- struct{}{}
	r.step()
	r.mustBeIdle()
	if got := r.removed(0); len(got) != 1 || got[0] != "first" {
		t.Fatalf("removed %v", got)
	}
	// The window is usable again.
	r.removeGate = nil
	if !r.c.startHistoryDelete(r.s, "second") {
		t.Fatal("deletion stayed locked after the first one finished")
	}
	r.step()
	r.mustBeIdle()
	if _, removes, alerts, _ := r.counts(); removes != 2 || alerts != 0 {
		t.Fatalf("removes=%d alerts=%d", removes, alerts)
	}
}

// A clear-all is one operation across its three stages: reading the list,
// claiming the IDs, and writing. Reopening the panel at any of them keeps the
// guard, and a conversation another window opened before the claim is kept.
func TestHistoryClearHoldsGuardThroughListReserveAndWrite(t *testing.T) {
	r := newHistoryDeleteRig(t)
	r.listed = []FxSession{{ID: "a"}, {ID: "kept"}, {ID: "b"}}
	r.listGate = make(chan struct{})
	r.removeGate = make(chan struct{})
	r.c.openHistory(r.s)
	if !r.c.startHistoryClear(r.s, true) {
		t.Fatal("the clear-all was refused")
	}

	// Stage one: the list is being read.
	r.wait("list")
	r.reopen()
	if !r.s.shell.History.Deleting {
		t.Fatal("reopening during the list read cleared the guard")
	}
	if r.c.startHistoryClear(r.s, true) || r.c.startHistoryDelete(r.s, "a") {
		t.Fatal("a second deletion started while the list was being read")
	}
	if held := r.c.sessionUse.snapshot(); len(held) != 0 {
		t.Fatalf("IDs were claimed before the list was read: %v", held)
	}

	// Stage two: the list is back, the claim has not run yet. Another window
	// resumes "kept" in that gap.
	r.listGate <- struct{}{}
	restore, err := r.c.sessionUse.claim("kept")
	if err != nil {
		t.Fatal(err)
	}
	r.reopen()
	if !r.s.shell.History.Deleting {
		t.Fatal("reopening before the claim cleared the guard")
	}
	r.step()

	// Stage three: the files are being removed.
	r.wait("remove")
	if got := r.removed(0); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("removed %v, want only the closed conversations", got)
	}
	r.reopen()
	if !r.s.shell.History.Deleting {
		t.Fatal("reopening during the write cleared the guard")
	}
	if r.c.startHistoryClear(r.s, true) {
		t.Fatal("a second clear-all started during the write")
	}
	if _, err := r.c.sessionUse.claim("a"); err == nil {
		t.Fatal("a conversation being deleted was restored")
	}

	r.removeGate <- struct{}{}
	r.step()
	restore.release()
	r.mustBeIdle()
	if lists, removes, alerts, informs := r.counts(); lists != 1 || removes != 1 || alerts != 0 || informs != 0 {
		t.Fatalf("lists=%d removes=%d alerts=%d informs=%d", lists, removes, alerts, informs)
	}
}

// A failure ends the operation, says so once, releases its claims, and leaves
// the window able to retry.
func TestHistoryDeleteFailureAllowsRetry(t *testing.T) {
	r := newHistoryDeleteRig(t)
	r.c.openHistory(r.s)

	r.removeEr = errors.New("disk said no")
	if !r.c.startHistoryDelete(r.s, "one") {
		t.Fatal("the deletion was refused")
	}
	r.step()
	r.mustBeIdle()
	if _, removes, alerts, _ := r.counts(); removes != 1 || alerts != 1 {
		t.Fatalf("removes=%d alerts=%d", removes, alerts)
	}
	r.removeEr = nil
	if !r.c.startHistoryDelete(r.s, "one") {
		t.Fatal("the retry was refused")
	}
	r.step()
	r.mustBeIdle()
	if _, removes, alerts, _ := r.counts(); removes != 2 || alerts != 1 {
		t.Fatalf("after retry removes=%d alerts=%d", removes, alerts)
	}

	// A clear-all whose list read fails never reaches the disk.
	r.listErr = errors.New("fx said no")
	if !r.c.startHistoryClear(r.s, false) {
		t.Fatal("the clear-all was refused")
	}
	r.step()
	r.mustBeIdle()
	if lists, removes, alerts, _ := r.counts(); lists != 1 || removes != 2 || alerts != 2 {
		t.Fatalf("lists=%d removes=%d alerts=%d", lists, removes, alerts)
	}

	// One with nothing to delete says so and unlocks.
	r.listErr = nil
	r.listed = nil
	if !r.c.startHistoryClear(r.s, false) {
		t.Fatal("the clear-all retry was refused")
	}
	r.step()
	r.mustBeIdle()
	if _, removes, alerts, informs := r.counts(); removes != 2 || alerts != 2 || informs != 1 {
		t.Fatalf("removes=%d alerts=%d informs=%d", removes, alerts, informs)
	}

	// A partial failure of the write still releases every claim.
	r.listed = []FxSession{{ID: "x"}, {ID: "y"}}
	r.removeEr = errors.New("half failed")
	if !r.c.startHistoryClear(r.s, false) {
		t.Fatal("the clear-all was refused")
	}
	r.step()
	r.step()
	r.mustBeIdle()
	if _, removes, alerts, _ := r.counts(); removes != 3 || alerts != 3 {
		t.Fatalf("removes=%d alerts=%d", removes, alerts)
	}
}

// A deletion refused by the registry — the conversation was resumed while the
// confirmation was up — gives the window's gate back.
func TestHistoryDeleteRefusedByRegistryUnlocksWindow(t *testing.T) {
	r := newHistoryDeleteRig(t)
	r.c.openHistory(r.s)
	restore, err := r.c.sessionUse.claim("open")
	if err != nil {
		t.Fatal(err)
	}
	if r.c.startHistoryDelete(r.s, "open") {
		t.Fatal("a conversation open in a window was deleted")
	}
	restore.release()
	r.mustBeIdle()
	if _, removes, alerts, _ := r.counts(); removes != 0 || alerts != 1 {
		t.Fatalf("removes=%d alerts=%d", removes, alerts)
	}
}

// A completion callback ends only its own operation: one that arrives after a
// newer deletion started must not unlock it.
func TestHistoryDeleteCallbackEndsOnlyItsOwnOperation(t *testing.T) {
	r := newHistoryDeleteRig(t)
	r.c.openHistory(r.s)
	old, ok := r.s.beginHistoryDelete()
	if !ok || !r.s.endHistoryDelete(old) {
		t.Fatal("the first operation did not begin and end")
	}
	current, ok := r.s.beginHistoryDelete()
	if !ok || current == old {
		t.Fatalf("operations share an identity: %d and %d", old, current)
	}
	if _, again := r.s.beginHistoryDelete(); again {
		t.Fatal("two operations were admitted at once")
	}
	r.c.finishHistoryDelete(r.s, old, "Delete saved session", errors.New("old write failed"))
	if _, _, alerts, _ := r.counts(); alerts != 0 {
		t.Fatal("a stale callback displayed an error during the newer operation")
	}
	if r.s.endHistoryDelete(old) || r.s.endHistoryDelete(0) {
		t.Fatal("a stale callback ended the newer operation")
	}
	if !r.s.historyDelete.busy() || !r.s.shell.History.Deleting {
		t.Fatal("a stale callback unlocked the window")
	}
	if !r.s.endHistoryDelete(current) {
		t.Fatal("the current operation could not end itself")
	}
	r.mustBeIdle()
}

// A window that closes mid-operation deletes nothing more on its own behalf
// and leaves no claim behind.
func TestHistoryDeleteSurvivesWindowClose(t *testing.T) {
	// Closed while the clear-all's list was being read: the read is
	// cancelled, nothing is claimed and nothing is removed.
	r := newHistoryDeleteRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	r.s.ctx, r.s.cancel = ctx, cancel
	r.listed = []FxSession{{ID: "a"}}
	r.listGate = make(chan struct{})
	r.c.openHistory(r.s)
	if !r.c.startHistoryClear(r.s, false) {
		t.Fatal("the clear-all was refused")
	}
	r.wait("list")
	cancel()
	r.step()
	r.mustBeIdle()
	if _, removes, alerts, informs := r.counts(); removes != 0 || alerts != 0 || informs != 0 {
		t.Fatalf("removes=%d alerts=%d informs=%d after the window closed", removes, alerts, informs)
	}

	// Closed while one conversation's files were being removed: the claim is
	// released by the worker, without waiting for a callback the dead window
	// would drop.
	r = newHistoryDeleteRig(t)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	r.s.ctx, r.s.cancel = ctx, cancel
	r.removeGate = make(chan struct{})
	r.c.openHistory(r.s)
	if !r.c.startHistoryDelete(r.s, "gone") {
		t.Fatal("the deletion was refused")
	}
	r.wait("remove")
	cancel()
	r.removeGate <- struct{}{}
	// The callback is queued only after the claim is released.
	fn := <-r.loop
	if r.c.sessionUse.inUse("gone") {
		t.Fatal("the claim waited for the dead window's callback")
	}
	fn()
	r.mustBeIdle()
}
