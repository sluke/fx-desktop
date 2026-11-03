package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

// leaseFor claims id on the registry and returns the claim, already booked.
func leaseFor(t *testing.T, r *sessionRegistry, id string) *sessionClaim {
	t.Helper()
	claim, err := r.claim(id)
	if err != nil {
		t.Fatalf("claim %q: %v", id, err)
	}
	return claim
}

// stateOf reads one entry's counters: how many claims are booked and how many
// are queued. It is how these tests say "the wait started" instead of sleeping
// and hoping.
func stateOf(r *sessionRegistry, id string) (pending, queued int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[id]
	if entry == nil {
		return 0, 0
	}
	return entry.pending, len(entry.waiters)
}

// waitQueued waits until id has want queued claims, so an assertion about a
// waiter is about a waiter that exists.
func waitQueued(t *testing.T, r *sessionRegistry, id string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, queued := stateOf(r, id); queued == want {
			return
		}
		if time.Now().After(deadline) {
			_, queued := stateOf(r, id)
			t.Fatalf("%s has %d queued claims, want %d", id, queued, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// S1: one saved conversation has one writer. A second claim waits for the
// first to be given back, and the hand-off is what admits it — not elapsed time.
func TestOneSessionHasOneWriter(t *testing.T) {
	var r sessionRegistry
	first := leaseFor(t, &r, "s1")
	if err := first.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := leaseFor(t, &r, "s1")
	done := make(chan error, 1)
	go func() { done <- second.acquire(context.Background()) }()
	waitQueued(t, &r, "s1", 1)

	select {
	case err := <-done:
		t.Fatalf("a second writer was admitted while the first held the lease (err=%v)", err)
	default:
	}
	first.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lease was not handed to the waiter")
	}
	second.release()
	if r.inUse("s1") {
		t.Fatal("a released lease stayed claimed")
	}
}

// A claim that never asked for the lease still blocks a deletion: the window
// has booked the ID, and the deletion must not run under a conversation that is
// about to start writing it.
func TestABookedClaimBlocksDeletion(t *testing.T) {
	var r sessionRegistry
	claim := leaseFor(t, &r, "s1")
	if _, ok := r.beginDelete("s1"); ok {
		t.Fatal("a deletion claimed an ID a conversation had booked")
	}
	claim.release()
	if _, ok := r.beginDelete("s1"); !ok {
		t.Fatal("a released booking still refused deletion")
	}
}

// A queued claim that is cancelled gives its booking back, so the ID does not
// stay protected — and undeletable — for a conversation that never starts.
func TestCancelledWaiterReleasesItsBooking(t *testing.T) {
	var r sessionRegistry
	owner := leaseFor(t, &r, "s1")
	if err := owner.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	queued := leaseFor(t, &r, "s1")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- queued.acquire(ctx) }()
	waitQueued(t, &r, "s1", 1)
	if pending, _ := stateOf(&r, "s1"); pending != 1 {
		t.Fatalf("pending = %d, want the queued waiter's booking", pending)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled wait returned %v", err)
	}
	// Only the owner's claim is left: its booking became the lease, and the
	// cancelled waiter took its own booking back.
	if pending, queued := stateOf(&r, "s1"); pending != 0 || queued != 0 {
		t.Fatalf("the cancelled waiter left pending=%d queued=%d", pending, queued)
	}
	owner.release()
	if r.inUse("s1") {
		t.Fatal("the cancelled waiter's booking outlived its owner")
	}
}

// Releasing one claim must never free another's lease: a closing conversation
// and its replacement both hold claims on the same ID during a same-window
// replacement.
func TestReleaseOnlyFreesItsOwnClaim(t *testing.T) {
	var r sessionRegistry
	closing := leaseFor(t, &r, "s1")
	if err := closing.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := leaseFor(t, &r, "s1")
	done := make(chan error, 1)
	go func() { done <- next.acquire(context.Background()) }()
	waitQueued(t, &r, "s1", 1)

	// The closing conversation's claim is released twice (Close is
	// idempotent); the queued successor must keep its own.
	closing.release()
	closing.release()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the successor was never admitted")
	}
	if _, ok := r.beginDelete("s1"); ok {
		t.Fatal("a deletion was admitted while the successor held the lease")
	}
	next.release()
}

// Different conversations do not wait for each other.
func TestDifferentSessionsRunInParallel(t *testing.T) {
	var r sessionRegistry
	one := leaseFor(t, &r, "one")
	two := leaseFor(t, &r, "two")
	if err := one.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := two.acquire(context.Background()); err != nil {
		t.Fatalf("a second ID waited for the first: %v", err)
	}
	one.release()
	two.release()
}

// S1, end to end: the window is gone, its fx process is still exiting, and a
// new window restores the same saved conversation. The new window's launcher
// must not run until that process is confirmed gone — the window-local barrier
// cannot know about it, so the application's lease has to.
//
// The connection is simulated at its publication boundary (a process that has
// started but has not been reaped yet), not with a child: the ordering this
// test is about is decided by the lease, and a child process would only add
// scheduling noise to it. The real-child teardown is covered by
// TestChatLifecycleProcess.
func TestReopenedSessionWaitsForTheClosingWriter(t *testing.T) {
	coord := NewCoordinator()
	old := newChatSession("", "project", []string{"--resume", "still-writing"}, nil)
	t.Cleanup(old.cancel)
	if err := old.attachRegistry(&coord.sessionUse); err != nil {
		t.Fatal(err)
	}

	windowA := &ProjectWindow{coord: coord, project: "project", chat: old, shell: chrome.DefaultShell()}
	windowA.ctx, windowA.cancel = context.WithCancel(context.Background())
	windowA.swap.window = windowA
	windowA.swap.start = func(c *chatSession) {
		// The connection began: from here the session has a process to reap
		// and holds the write lease until it is told the process is gone.
		c.mu.Lock()
		c.started = true
		c.mu.Unlock()
	}
	windowA.startConversation(old)
	waitFor(t, "the write lease", func() bool { return coord.sessionUse.leaseHeldBy("still-writing") })

	// The user closes the window before the process is gone. The window leaves
	// the list at once; the claim does not.
	old.Close()
	coord.removeSession(windowA)
	windowA.cancel()

	// A new window reopens the same conversation, as the history panel does.
	next := newChatSession("", "project", []string{"--resume", "still-writing"}, nil)
	t.Cleanup(next.cancel)
	admitted := make(chan struct{})
	windowB := &ProjectWindow{coord: coord, project: "project", chat: next, shell: chrome.DefaultShell()}
	windowB.ctx, windowB.cancel = context.WithCancel(context.Background())
	defer windowB.cancel()
	windowB.swap.window = windowB
	windowB.swap.start = func(c *chatSession) {
		select {
		case <-old.closedDone:
			// Expected: the old process was confirmed gone first.
		default:
			t.Error("the new writer's launcher ran before the old process was confirmed gone")
		}
		close(admitted)
	}
	windowB.startConversation(next)

	// It is queued behind the old writer, not admitted by it.
	waitQueued(t, &coord.sessionUse, "still-writing", 1)
	select {
	case <-admitted:
		t.Fatal("the reopened conversation was admitted while the old writer was still unreaped")
	default:
	}

	// The old process is now confirmed gone.
	old.signalProcessReady()
	select {
	case <-admitted:
	case <-time.After(5 * time.Second):
		t.Fatal("the reopened conversation was never admitted")
	}
	if reaped, problem := old.CloseResult(); !reaped || problem != nil {
		t.Fatalf("the old writer ended as reaped=%v problem=%v", reaped, problem)
	}
	// The new window holds the lease now, so the ID is still protected.
	if got := coord.liveSessionIDs(); !got["still-writing"] {
		t.Fatalf("the reopened conversation is not protected: %v", got)
	}
	next.Close()
	<-next.closedDone
	if coord.sessionUse.inUse("still-writing") {
		t.Fatal("a released conversation kept its claim")
	}
}

// S1's refusal branch: the old writer never confirms its exit, so the new
// window is refused rather than allowed to run a second writer.
func TestReopenedSessionRefusedWhenTheWriterNeverLeaves(t *testing.T) {
	coord := NewCoordinator()
	old := newChatSession("", "project", []string{"--resume", "still-writing"}, nil)
	t.Cleanup(old.cancel)
	if err := old.attachRegistry(&coord.sessionUse); err != nil {
		t.Fatal(err)
	}
	// A writer that is running and never reaped: the lease is held and no
	// signal will ever say "reaped".
	if err := old.waitForSession(context.Background()); err != nil {
		t.Fatal(err)
	}

	next := newChatSession("", "project", []string{"--resume", "still-writing"}, nil)
	t.Cleanup(next.cancel)
	window := &ProjectWindow{coord: coord, project: "project", chat: next, shell: chrome.DefaultShell()}
	window.ctx, window.cancel = context.WithCancel(context.Background())
	defer window.cancel()
	window.swap.window = window
	started := false
	window.swap.start = func(*chatSession) { started = true }
	window.startConversation(next)

	select {
	case <-next.closedDone:
	case <-time.After(leaseWait + 5*time.Second):
		t.Fatal("a refused restore was left waiting")
	}
	if started {
		t.Fatal("a second writer was started on an ID whose writer never left")
	}
	if next.snapshot().Problem == "" {
		t.Fatal("the refusal did not say why")
	}
	old.releaseClaim()
}

// A new conversation has no saved ID until fx names one, so its booking starts
// at the reply, not before it: "the ID is always known in advance" is not a
// premise this protocol may rest on (§4.1, T15).
func TestNewConversationBooksTheIDfxReturns(t *testing.T) {
	var r sessionRegistry
	c := newChatSession("", "project", nil, nil)
	if err := c.attachRegistry(&r); err != nil {
		t.Fatal(err)
	}
	if c.sessionUseID != "" {
		t.Fatalf("a new conversation booked %q before fx named it", c.sessionUseID)
	}
	if err := c.takeSession("fresh-id"); err != nil {
		t.Fatal(err)
	}
	if c.sessionUseID != "fresh-id" || !r.leaseHeldBy("fresh-id") {
		t.Fatalf("the returned ID was not booked and leased: %q", c.sessionUseID)
	}
	c.releaseClaim()
	if r.inUse("fresh-id") {
		t.Fatal("a released new conversation kept its claim")
	}
}

// The same rule for an ID only fx could resolve (`--continue`, `--resume
// last`): if another window is writing it, the connection is refused now
// rather than waiting for a process that is already running.
func TestResolvedIDHeldByAnotherWindowIsRefusedAtOnce(t *testing.T) {
	var r sessionRegistry
	other := newChatSession("", "project", []string{"--resume", "busy"}, nil)
	if err := other.attachRegistry(&r); err != nil {
		t.Fatal(err)
	}
	if err := other.waitForSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	next := newChatSession("", "project", nil, nil)
	if err := next.attachRegistry(&r); err != nil {
		t.Fatal(err)
	}
	if err := next.takeSession("busy"); !errors.Is(err, errSessionBusy) {
		t.Fatalf("a second writer was admitted on a leased ID: %v", err)
	}
	if next.sessionUseID != "" {
		t.Fatalf("the refused conversation kept a booking on %q", next.sessionUseID)
	}
	other.releaseClaim()
}

// S6: fx may answer session/load with a different conversation than the one it
// was told to load. The directory it did load is still open in that process, so
// its lease must survive the second booking — releasing it there would let
// another window write a directory this process may still touch.
func TestSecondBookingKeepsTheFirstLease(t *testing.T) {
	var r sessionRegistry
	c := newChatSession("", "project", nil, nil)
	if err := c.attachRegistry(&r); err != nil {
		t.Fatal(err)
	}
	if err := c.takeSession("loaded"); err != nil {
		t.Fatal(err)
	}
	if err := c.takeSession("returned"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"loaded", "returned"} {
		if !r.leaseHeldBy(id) {
			t.Fatalf("%s lost its writer when fx named another conversation", id)
		}
		if _, ok := r.beginDelete(id); ok {
			t.Fatalf("%s could be deleted while this process may still write it", id)
		}
	}
	if c.sessionUseID != "returned" {
		t.Fatalf("the newest booking is not the one reported: %q", c.sessionUseID)
	}
	c.releaseClaim()
	for _, id := range []string{"loaded", "returned"} {
		if r.inUse(id) {
			t.Fatalf("%s kept a claim after the conversation was confirmed gone", id)
		}
	}
}

// The refusal branch undoes one booking, not all of them: an ID the window
// cannot take must not cost the directory the running process is already using.
func TestRefusedSecondIDKeepsTheFirstLease(t *testing.T) {
	var r sessionRegistry
	other := newChatSession("", "project", nil, nil)
	if err := other.attachRegistry(&r); err != nil {
		t.Fatal(err)
	}
	if err := other.takeSession("busy"); err != nil {
		t.Fatal(err)
	}
	c := newChatSession("", "project", nil, nil)
	if err := c.attachRegistry(&r); err != nil {
		t.Fatal(err)
	}
	if err := c.takeSession("mine"); err != nil {
		t.Fatal(err)
	}
	if err := c.takeSession("busy"); !errors.Is(err, errSessionBusy) {
		t.Fatalf("a second writer was admitted on a leased ID: %v", err)
	}
	if !r.leaseHeldBy("mine") {
		t.Fatal("the refused ID took the first directory's lease with it")
	}
	if c.sessionUseID != "mine" {
		t.Fatalf("the conversation reports %q after its second booking was refused", c.sessionUseID)
	}
	if _, ok := r.beginDelete("busy"); ok {
		t.Fatal("the refusal released another window's lease")
	}
	c.releaseClaim()
	other.releaseClaim()
}
