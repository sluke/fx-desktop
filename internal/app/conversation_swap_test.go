package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

// swapWindow is a window with the coordinator wired and a launcher that records
// admission instead of spawning fx. The barrier logic is what these tests own.
func swapWindow(t *testing.T) (*ProjectWindow, *[]string, *sync.Mutex) {
	t.Helper()
	s := &ProjectWindow{coord: NewCoordinator(), project: t.TempDir(), shell: chrome.DefaultShell()}
	s.swap.window = s
	var mu sync.Mutex
	var started []string
	s.swap.start = func(c *chatSession) {
		mu.Lock()
		started = append(started, c.project)
		mu.Unlock()
	}
	return s, &started, &mu
}

func startedNow(started *[]string, mu *sync.Mutex) []string {
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), *started...)
}

// waitStarted waits until the recorded admissions match want.
func waitStarted(t *testing.T, started *[]string, mu *sync.Mutex, want ...string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got := startedNow(started, mu)
		if len(got) == len(want) {
			match := true
			for i := range want {
				if got[i] != want[i] {
					match = false
					break
				}
			}
			if match {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("started = %v, want %v", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

// B1: the conversation generation is read by a worker (the composer's accept
// path) while the app loop replaces conversations. It must be synchronized:
// `go test -race` is the assertion.
func TestReplacementGenerationIsRaceFree(t *testing.T) {
	s, _, _ := swapWindow(t)
	s.chat = newChatSession("", s.project, nil, nil)
	stop := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = s.swap.generation()
			}
		}
	}()
	for i := 0; i < 30; i++ {
		s.replaceConversation(nil)
	}
	close(stop)
	workers.Wait()
}

// B4, case 1: A is started and not reaped. B is requested and replaced by C
// while A is still the barrier, so B is retired without ever being admitted.
// The assertion is made while A is unreaped, which is what makes it a fact
// rather than a guess about the scheduler.
func TestReplacementWaitsForTheStartedConversation(t *testing.T) {
	s, started, mu := swapWindow(t)
	a := newChatSession("", "a", nil, nil)
	s.swap.request(a)
	waitStarted(t, started, mu, "a")

	b := newChatSession("", "b", nil, nil)
	c := newChatSession("", "c", nil, nil)
	s.swap.request(b)
	s.swap.request(c)

	// A has not been confirmed reaped, so neither B nor C may start. No
	// request can admit while the barrier is open, so this needs no wait.
	if got := startedNow(started, mu); len(got) != 1 {
		t.Fatalf("a replaced-but-not-reaped barrier let another conversation start: %v", got)
	}

	// A is reaped: only the newest request (C) starts; B was retired unstarted.
	a.signalClosed()
	waitStarted(t, started, mu, "a", "c")
	c.Close()
}

// B4, case 2: B really was admitted before C arrived. C must wait for B's own
// process — the barrier is "the last conversation that started", not "the one
// I replaced" (T17).
func TestReplacementWaitsForAnAdmittedPredecessor(t *testing.T) {
	s, started, mu := swapWindow(t)
	a := newChatSession("", "a", nil, nil)
	s.swap.request(a)
	waitStarted(t, started, mu, "a")
	a.signalClosed()

	b := newChatSession("", "b", nil, nil)
	s.swap.request(b)
	waitStarted(t, started, mu, "a", "b")

	c := newChatSession("", "c", nil, nil)
	s.swap.request(c)
	// B owns the barrier now and has not been reaped.
	if got := startedNow(started, mu); len(got) != 2 {
		t.Fatalf("a request overtook an admitted predecessor: %v", got)
	}
	b.signalClosed()
	waitStarted(t, started, mu, "a", "b", "c")
	c.Close()
}

// The caller of a replacement owns closing the conversation it replaces. If it
// forgot, the new request would be admitted while the old one still held the
// window's connection.
func TestReplaceConversationClosesThePreviousBeforeAdmitting(t *testing.T) {
	s, started, mu := swapWindow(t)
	previous := newChatSession("", "previous", nil, nil)
	s.chat = previous
	s.swap.start = func(c *chatSession) {
		select {
		case <-previous.closedDone:
		default:
			t.Error("the replacement was admitted before the previous conversation was closed")
		}
		mu.Lock()
		*started = append(*started, c.project)
		mu.Unlock()
	}
	s.replaceConversation(nil)
	waitStarted(t, started, mu, s.project)
}

// A window that has gone away refuses admission instead of starting a process
// nobody can show.
func TestSwapRefusesAdmissionAfterTheWindowCloses(t *testing.T) {
	s, started, mu := swapWindow(t)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	a := newChatSession("", "a", nil, nil)
	s.cancel()
	s.swap.request(a)
	select {
	case <-a.closedDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a request for a closed window was left waiting")
	}
	if got := startedNow(started, mu); len(got) != 0 {
		t.Fatalf("a closed window started %v", got)
	}
}

// N3: when the old process could not be confirmed gone, the replacement is not
// started — a second fx on the same session directory would be a second writer
// — and the reason is shown on the conversation that was refused.
func TestReplacementRefusedWhenTheOldProcessCouldNotBeConfirmedGone(t *testing.T) {
	s, started, mu := swapWindow(t)
	a := newChatSession("", "a", nil, nil)
	s.swap.request(a)
	waitStarted(t, started, mu, "a")

	b := newChatSession("", "b", nil, nil)
	s.swap.request(b)
	// The teardown ends without confirming the exit: the barrier reports that
	// as a result, not as a notification that "closing started".
	a.finishClose(false, errors.New("fx did not exit"))
	select {
	case <-b.closedDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a refused replacement was left waiting")
	}
	if got := startedNow(started, mu); len(got) != 1 {
		t.Fatalf("a replacement started despite an unconfirmed exit: %v", got)
	}
	if b.snapshot().Problem == "" {
		t.Fatal("the refusal did not say why")
	}
	b.Close()
}
