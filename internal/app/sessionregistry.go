package app

import (
	"context"
	"errors"
	"sync"
)

// errSessionDeleting refuses a conversation for an ID a deletion owns.
var errSessionDeleting = errors.New("This conversation is being deleted, so it was not opened.")

// errSessionBusy refuses a conversation for an ID another conversation is
// already writing. It exists for the ID fx reveals (session/new, `--continue`):
// by then a process is running, so waiting would mean waiting for itself.
var errSessionBusy = errors.New("This conversation is open in another window.")

// sessionRegistry is the application's record of which saved conversation this
// app may write to, and from which window (T9, T15).
//
// Its whole reason to exist is that a conversation is not the same thing as a
// visible window. fx keeps writing a session directory while its process is
// shutting down, and a window that was closed — or a conversation that a newer
// one replaced — is not in the window list any more. Deciding from the window
// list therefore both misses the closing writer and is stale by the time the
// deletion runs.
//
// Two facts meet here, and they are deliberately not the same fact:
//
//   - a claim: this conversation intends to write the ID. It is taken on the
//     app loop before the RPC that opens the conversation, so it covers the
//     handshake and protects the ID from deletion, but it grants no write
//     permission. Several conversations may hold one ID at once — a window
//     replacing its own conversation does exactly that (A→B).
//   - the lease: exactly one conversation may hold it, and only the holder may
//     have a process writing the directory. A claim becomes the lease when it
//     is granted; the transfer is where a reopened conversation yields to the
//     process that is still exiting (S1).
//
// Deletion is exclusive with both: it refuses while any claim exists, so a
// conversation that is only waiting to be admitted is not deleted under.
//
// The registry protects this application only. It is not a lock across
// independent fx processes, and it makes no such claim.
type sessionRegistry struct {
	mu      sync.Mutex
	entries map[string]*sessionUse
}

// sessionUse is one session ID's state.
type sessionUse struct {
	// owner is the conversation holding the lease, or nil.
	owner *sessionClaim
	// waiters are the claims queued for the lease, oldest first. A waiter is
	// counted in pending like any other claim.
	waiters []*sessionClaim
	// pending is how many claims do not hold the lease: those not yet
	// acquired plus those queued. It is what makes an opening conversation
	// protected before it has a process.
	pending int
	// deleting is set while a deletion owns the ID.
	deleting bool
}

// leaseState is one claim's place in the protocol. It is guarded by the
// registry's mutex, never by the claim itself.
type leaseState uint8

const (
	// leaseClaimed means the claim is counted but has not asked for the
	// lease yet: the app booked the ID before starting the conversation.
	leaseClaimed leaseState = iota
	// leaseWaiting means the claim is queued behind the current owner.
	leaseWaiting
	// leaseOwned means this claim is the writer.
	leaseOwned
	// leaseDone means the claim has been given back.
	leaseDone
)

// sessionClaim is one conversation's claim on one saved ID. Every method is
// safe on a nil registry and on an empty ID, so a session built by a test — or
// one that never names a saved conversation — needs no special case.
type sessionClaim struct {
	registry *sessionRegistry
	id       string
	// granted is closed when the lease is handed to this claim.
	granted chan struct{}
	// state is read and written under registry.mu.
	state leaseState
}

func (r *sessionRegistry) entry(id string) *sessionUse {
	if r.entries == nil {
		r.entries = make(map[string]*sessionUse, 4)
	}
	entry := r.entries[id]
	if entry == nil {
		entry = &sessionUse{}
		r.entries[id] = entry
	}
	return entry
}

// claim books id for one conversation without granting it. It reports an error
// only when a deletion owns the ID, in which case the caller must not open the
// conversation. The claim is idempotent to release.
func (r *sessionRegistry) claim(id string) (*sessionClaim, error) {
	if id == "" {
		return &sessionClaim{}, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entry(id)
	if entry.deleting {
		return nil, errSessionDeleting
	}
	entry.pending++
	return &sessionClaim{registry: r, id: id, granted: make(chan struct{}), state: leaseClaimed}, nil
}

// acquire makes the claim the ID's only writer, waiting for whoever holds it.
// It must run on a worker — never on the app loop, where the wait would freeze
// the window — and its context bounds the wait.
func (c *sessionClaim) acquire(ctx context.Context) error {
	r := c.registry
	if r == nil || c.id == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	entry := r.entries[c.id]
	if c.state != leaseClaimed || entry == nil {
		// Already the writer, already waiting, or already given back: the
		// caller has nothing left to ask for.
		r.mu.Unlock()
		return nil
	}
	if entry.owner == nil && len(entry.waiters) == 0 {
		r.adopt(entry, c)
		r.mu.Unlock()
		return nil
	}
	c.state = leaseWaiting
	entry.waiters = append(entry.waiters, c)
	r.mu.Unlock()
	select {
	case <-c.granted:
		if err := ctx.Err(); err != nil {
			// Granted and cancelled at the same time: the lease must not
			// stay with a conversation that will not start.
			c.release()
			return err
		}
		return nil
	case <-ctx.Done():
		c.release()
		return ctx.Err()
	}
}

// tryAcquire takes the lease only if it is free right now. It is the path for
// an ID fx just revealed, where waiting would wait for a process that is
// already running.
func (c *sessionClaim) tryAcquire() bool {
	r := c.registry
	if r == nil || c.id == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[c.id]
	if c.state != leaseClaimed || entry == nil {
		return true
	}
	if entry.owner != nil || len(entry.waiters) > 0 {
		return false
	}
	r.adopt(entry, c)
	return true
}

// release gives the claim back: either the lease, or the booking. It is
// idempotent, and it only ever releases this claim — a closing conversation
// cannot free a successor's lease.
func (c *sessionClaim) release() {
	r := c.registry
	if r == nil || c.id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c.state == leaseDone {
		return
	}
	entry := r.entries[c.id]
	if entry == nil {
		c.state = leaseDone
		return
	}
	switch c.state {
	case leaseClaimed:
		entry.pending--
	case leaseWaiting:
		entry.dropWaiter(c)
		entry.pending--
	case leaseOwned:
		entry.owner = nil
		r.handOff(entry)
	}
	c.state = leaseDone
	r.cleanup(c.id, entry)
}

// adopt gives entry's lease to c. The caller holds the mutex.
func (r *sessionRegistry) adopt(entry *sessionUse, c *sessionClaim) {
	entry.owner = c
	c.state = leaseOwned
	if entry.pending > 0 {
		entry.pending--
	}
}

// handOff passes the free lease to the oldest waiter, so a queue of requests
// is served in the order it arrived rather than in a race. The caller holds the
// mutex.
func (r *sessionRegistry) handOff(entry *sessionUse) {
	for len(entry.waiters) > 0 {
		next := entry.waiters[0]
		entry.waiters = entry.waiters[1:]
		if next.state != leaseWaiting {
			// Released while queued: it already took itself out.
			continue
		}
		r.adopt(entry, next)
		close(next.granted)
		return
	}
}

// cleanup forgets an ID nobody holds any more, so the map does not grow with
// every conversation the app ever opened.
func (r *sessionRegistry) cleanup(id string, entry *sessionUse) {
	if entry.owner != nil || entry.pending > 0 || entry.deleting || len(entry.waiters) > 0 {
		return
	}
	delete(r.entries, id)
}

func (e *sessionUse) dropWaiter(c *sessionClaim) {
	for i, waiter := range e.waiters {
		if waiter == c {
			e.waiters = append(e.waiters[:i], e.waiters[i+1:]...)
			return
		}
	}
}

// beginDelete claims id for a deletion. It reports false when a conversation
// still holds the ID — a live writer, one that is still starting, one queued
// behind another window's writer, or one whose process is not yet confirmed
// reaped. The returned finish is idempotent and must run on every path,
// including a failed or partial deletion.
func (r *sessionRegistry) beginDelete(id string) (finish func(), ok bool) {
	if id == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entry(id)
	if entry.deleting || entry.owner != nil || entry.pending > 0 {
		return nil, false
	}
	entry.deleting = true
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			current := r.entries[id]
			if current == nil {
				return
			}
			current.deleting = false
			r.cleanup(id, current)
		})
	}, true
}

// inUse reports whether a conversation of any kind — opening, running,
// queued or closing — still holds id.
func (r *sessionRegistry) inUse(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[id]
	return entry != nil && (entry.owner != nil || entry.pending > 0 || entry.deleting)
}

// snapshot is the set of IDs history must not offer for deletion: everything
// this app may still write, including conversations whose windows are gone but
// whose processes are not yet reaped.
func (r *sessionRegistry) snapshot() map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make(map[string]bool, len(r.entries))
	for id, entry := range r.entries {
		if entry.owner != nil || entry.pending > 0 || entry.deleting {
			ids[id] = true
		}
	}
	return ids
}

// attachRegistry gives a conversation its claim book and books the launch-time
// target, if the flags already name it. It must be called before the
// conversation is started, so the window is protected during the handshake.
// Booking never waits: the lease is taken on the swap worker
// (waitForSession), not on the app loop.
func (c *chatSession) attachRegistry(registry *sessionRegistry) error {
	if registry == nil {
		return nil
	}
	c.mu.Lock()
	c.registry = registry
	resume := c.resume
	c.mu.Unlock()
	if resume == "" || resume == "last" {
		// "last" names no ID until fx resolves it; the claim is taken
		// before the session/load RPC instead.
		return nil
	}
	return c.bookSession(resume)
}

// bookSession records the intent to write id and claims it in the registry.
// The claim protects the ID from deletion and from a second writer's start, but
// it is not the write permission: that is waitForSession's job.
func (c *chatSession) bookSession(id string) error {
	_, err := c.bookClaim(id)
	return err
}

// bookClaim is bookSession that also reports the claim it took, so a caller
// that must take the lease at once can undo exactly its own booking and leave
// the conversation's earlier ones alone.
//
// A second ID is a second claim, not a replacement (S6): fx can answer
// session/load with a different conversation, and the directory it was told to
// load is still open in that process. Releasing that lease here would hand the
// directory to another window while this process may still write it.
func (c *chatSession) bookClaim(id string) (*sessionClaim, error) {
	if id == "" {
		return nil, nil
	}
	c.mu.Lock()
	if c.sessionUseID == id {
		c.mu.Unlock()
		return nil, nil
	}
	registry, closed := c.registry, c.closed
	c.mu.Unlock()
	if registry == nil {
		// No app registry (a bare session in a test): the booking is
		// nominal, but still recorded so a later frame reports the same ID.
		c.mu.Lock()
		c.sessionUseID = id
		c.mu.Unlock()
		return nil, nil
	}
	if closed {
		return nil, errChatClosed
	}
	claim, err := registry.claim(id)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.sessionUseID = id
	c.sessionClaims = append(c.sessionClaims, claim)
	c.mu.Unlock()
	return claim, nil
}

// waitForSession takes the exclusive write lease on the ID this request is
// opening, waiting for whoever holds it — another window's exiting process, in
// the case this exists for. It runs on the swap worker so the app loop never
// waits. The claim it waits on is the newest one: the ID this attempt named.
func (c *chatSession) waitForSession(ctx context.Context) error {
	c.mu.Lock()
	var claim *sessionClaim
	if n := len(c.sessionClaims); n > 0 {
		claim = c.sessionClaims[n-1]
	}
	c.mu.Unlock()
	if claim == nil {
		return nil
	}
	return claim.acquire(ctx)
}

// takeSession books id and takes its lease at once. It is the path for an ID
// that only fx could name (session/new, `--continue`, `--resume last`): the
// process is already running, so the answer is "now or never".
func (c *chatSession) takeSession(id string) error {
	claim, err := c.bookClaim(id)
	if err != nil {
		return err
	}
	if claim == nil {
		return nil
	}
	if !claim.tryAcquire() {
		// This ID will not be written after all, so its booking must not keep
		// a deletion away from it — but the earlier ones still belong to a
		// process that is running (S6).
		c.unbook(claim)
		return errSessionBusy
	}
	return nil
}

// unbook gives back one booking and forgets it, keeping every other. An ID that
// was refused must not take an earlier directory's lease with it.
func (c *chatSession) unbook(target *sessionClaim) {
	c.mu.Lock()
	kept := make([]*sessionClaim, 0, len(c.sessionClaims))
	for _, claim := range c.sessionClaims {
		if claim != target {
			kept = append(kept, claim)
		}
	}
	c.sessionClaims = kept
	if len(kept) > 0 {
		c.sessionUseID = kept[len(kept)-1].id
	} else {
		c.sessionUseID = ""
	}
	c.mu.Unlock()
	target.release()
}

// releaseClaim gives back every claim this conversation holds, exactly once.
func (c *chatSession) releaseClaim() {
	c.mu.Lock()
	claims := c.sessionClaims
	c.sessionClaims = nil
	c.mu.Unlock()
	for _, claim := range claims {
		claim.release()
	}
}

// leaseHeldBy reports whether id has a writer right now. The lease itself is
// never handed out: this is the question a window or a test may ask about it.
func (r *sessionRegistry) leaseHeldBy(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.entries[id]
	return entry != nil && entry.owner != nil
}
