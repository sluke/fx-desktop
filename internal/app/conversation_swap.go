package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// conversationSwap serializes conversation replacement for one window. It owns
// two facts the window must not compute on its own (§B1, §B4):
//
//   - the generation of the newest requested conversation, so a worker can
//     decide on another goroutine whether its admission is still current —
//     with a lock, not a race on a window field;
//   - the process barrier: the last conversation whose connection was really
//     started and whose process is not yet confirmed reaped. A new request
//     waits on that barrier, not on whichever session it happened to replace,
//     so A→B→C cannot start C before A is gone.
//
// The displayed conversation (`ProjectWindow.chat`) is still assigned on the
// app loop; only admission and the barrier live here.
type conversationSwap struct {
	window *ProjectWindow
	// start launches a connection. It is a field so a test can observe
	// admission without spawning fx.
	start func(*chatSession)

	mu sync.Mutex
	// gen is the generation of the newest request. It only grows.
	gen uint64
	// waiting is the newest request that has not been admitted yet.
	waiting *chatSession
	// started is the conversation whose connection owns the process barrier.
	// Until it is confirmed reaped, nothing else may start.
	started *chatSession
}

// errSuperseded retires a request that a newer one replaced while it waited.
var errSuperseded = errors.New("conversation request superseded")

// errWindowGone refuses a request that has no window left to show it in.
var errWindowGone = errors.New("This conversation was not opened: the window closed.")

// generation is the newest requested conversation. Safe from any goroutine.
func (sw *conversationSwap) generation() uint64 {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.gen
}

// request makes next the conversation to start and schedules it behind the
// current process barrier. It is called on the app loop.
func (sw *conversationSwap) request(next *chatSession) {
	sw.mu.Lock()
	sw.gen++
	generation := sw.gen
	replaced := sw.waiting
	sw.waiting = next
	sw.mu.Unlock()
	if replaced != nil {
		// A request that never started owns no process, so closing it cannot
		// become anyone's barrier. Close is idempotent; the window may have
		// closed it already.
		replaced.Close()
	}
	go sw.run(generation, next)
}

// run waits for the process barrier, then admits the request. Admission is
// re-checked after the wait because a newer request may have arrived while
// this one waited.
func (sw *conversationSwap) run(generation uint64, next *chatSession) {
	barrier, ok := sw.waitingFor(generation, next)
	if !ok {
		next.Close()
		return
	}
	if barrier != nil {
		select {
		case <-barrier.closedDone:
			// The signal is "the teardown has a result". Only a confirmed
			// reaping admits a replacement: a second fx on the same session
			// directory would have two writers, so a close that could not
			// confirm the exit refuses it and says why (§4.2, R1).
			if reaped, problem := barrier.CloseResult(); !reaped {
				if problem == nil {
					problem = errors.New("the teardown did not confirm the exit")
				}
				sw.retire(generation, next)
				next.abandon(fmt.Errorf("The previous fx process could not be confirmed gone, so this conversation was not reopened (%v).", problem))
				return
			}
		case <-sw.lifetime().Done():
			next.Close()
			return
		case <-time.After(shutdownTimeout + time.Second):
			// The old process could not be confirmed gone; a second fx on
			// the same conversation would have two writers on one session
			// directory. Say so instead (§4.3), naming the teardown's own
			// reason when there is one.
			err := errors.New("The previous fx process did not exit, so this conversation was not reopened.")
			if _, problem := barrier.CloseResult(); problem != nil {
				err = fmt.Errorf("%w (%v)", err, problem)
			}
			sw.retire(generation, next)
			next.abandon(err)
			return
		}
	}
	if !sw.waitForSession(generation, next) {
		return
	}
	if err := sw.admit(generation, next); err != nil {
		if errors.Is(err, errSuperseded) {
			next.Close()
			return
		}
		next.abandon(err)
		return
	}
	sw.launcher()(next)
}

// leaseWait bounds how long a conversation waits for another window to stop
// writing the same saved session. It matches the process barrier's own bound:
// the writer it waits for is a teardown, not an indefinite run.
const leaseWait = shutdownTimeout + time.Second

// waitForSession takes the app-wide write lease on the saved conversation this
// request names, waiting outside every window's own barrier (T15, S1). It
// reports whether the request may go on to be admitted.
//
// The local barrier only knows the conversations this window started. A window
// that was closed leaves its process exiting and its ID claimed in the
// registry, but its replacement is a *different* window whose swap has no
// barrier at all — so the application's registry, not the window, has to be
// the thing that admits the second writer.
func (sw *conversationSwap) waitForSession(generation uint64, next *chatSession) bool {
	if _, ok := sw.waitingFor(generation, next); !ok {
		// Replaced while the barrier was being waited for: do not queue for
		// a lease nobody needs.
		next.Close()
		return false
	}
	ctx, cancel := context.WithTimeout(sw.lifetime(), leaseWait)
	defer cancel()
	if err := next.waitForSession(ctx); err != nil {
		sw.retire(generation, next)
		if sw.lifetime().Err() != nil {
			// The window went away while the lease was being waited for:
			// that is not a refusal to report, it is a window with no
			// conversation left to show.
			next.Close()
			return false
		}
		next.abandon(fmt.Errorf("This conversation is still open in another window, so it was not reopened (%v).", err))
		return false
	}
	return true
}

// waitingFor reports the barrier this request must wait for, and whether it is
// still the newest request.
func (sw *conversationSwap) waitingFor(generation uint64, next *chatSession) (*chatSession, bool) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.gen != generation || sw.waiting != next {
		return nil, false
	}
	return sw.started, true
}

// admit claims the request: it becomes the started conversation and the new
// process barrier. A request that is no longer the newest, or a window that
// has gone away, is refused.
func (sw *conversationSwap) admit(generation uint64, next *chatSession) error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.gen != generation || sw.waiting != next {
		return errSuperseded
	}
	if sw.lifetime().Err() != nil {
		return errWindowGone
	}
	if sw.window != nil {
		if w := sw.window.winSnapshot(); w != nil && w.IsDestroyed() {
			return errWindowGone
		}
	}
	sw.waiting = nil
	sw.started = next
	return nil
}

// retire drops a request that will never start. The barrier is left alone: a
// process that could not be confirmed gone still guards every later start.
func (sw *conversationSwap) retire(generation uint64, next *chatSession) {
	sw.mu.Lock()
	if sw.gen == generation && sw.waiting == next {
		sw.waiting = nil
	}
	sw.mu.Unlock()
}

// launcher is the connection attempt. A test replaces it to watch admission
// without a real process.
func (sw *conversationSwap) launcher() func(*chatSession) {
	if sw.start != nil {
		return sw.start
	}
	return func(c *chatSession) { c.connect() }
}

// lifetime is the window's context, with a background fallback for a window a
// test built by hand.
func (sw *conversationSwap) lifetime() context.Context {
	if sw.window == nil {
		return context.Background()
	}
	return sw.window.lifetime()
}

// draftEdited counts one edit event of the composer. It is the editor's own
// change callback — typing, paste, undo/redo, a mention insertion, and any
// programmatic rewrite that is an edit all reach it — so the revision follows
// the edits instead of the draw cycles (§B5, N2).
//
// Every event counts, even one that leaves the text as it was: "delete it and
// retype the same words" is a new draft, and comparing the sampled text cannot
// tell it from the old one.
func (s *ProjectWindow) draftEdited() {
	s.draftRevision++
	s.draftSeen = s.chatUI.Draft
}

// observeDraft is the fallback for a draft written without an edit event: a
// test setting the field, a programmatic restore, or a keystroke the control
// has not delivered yet (k.Text() may be ahead of the bound value). It counts
// a text difference at the frame boundary and again before a submission is
// published. It cannot replace draftEdited: a difference it sees says only
// that the text changed, and one it does not see may still be an edit (§B5).
func (s *ProjectWindow) observeDraft() {
	if s.chatUI.Draft == s.draftSeen {
		return
	}
	s.draftSeen = s.chatUI.Draft
	s.draftRevision++
}
