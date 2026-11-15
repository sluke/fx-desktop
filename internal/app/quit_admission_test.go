package app

// Q5 tests. TestQuitRejectsConversationAdmission was migrated from the r5
// review probe TestReviewR5QuitRejectsConversationAdmission.

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/sessionstats"
)

// The final drain closes the stats store. A conversation that is still
// waiting to be admitted must not be allowed to start a new fx process.
func TestQuitRejectsConversationAdmission(t *testing.T) {
	c := &Coordinator{quitPhase: quitDraining}
	s := &ProjectWindow{coord: c}
	next := &chatSession{}
	s.swap.window = s
	s.swap.gen = 1
	s.swap.waiting = next
	if err := s.swap.admit(1, next); err == nil {
		t.Fatal("conversation admitted after quit entered draining")
	}
}

// Every entry point that can open a conversation or a Quick Ask must refuse
// as well — not just the final admission and not just the composer.
func TestQuitRejectsEveryNewWorkEntry(t *testing.T) {
	dir := t.TempDir()
	c := &Coordinator{quitPhase: quitDraining, fxPath: "/stub/fx"}
	current := newChatSession("/stub/fx", dir, nil, nil)
	defer current.Close()
	s := &ProjectWindow{coord: c, project: dir, chat: current, shell: chrome.DefaultShell()}
	s.swap.window = s
	c.sessions = []*ProjectWindow{s}

	c.openProjectWithFlags(dir)
	c.HandleSecondInstance([]string{dir}, dir)
	if len(c.sessions) != 1 {
		t.Fatalf("quit opened a new session window: %d sessions", len(c.sessions))
	}
	s.replaceConversation([]string{"--model", "m"})
	if s.chat != current {
		t.Fatal("quit replaced the displayed conversation")
	}
	next := newChatSession("/stub/fx", dir, nil, nil)
	defer next.Close()
	s.startConversation(next)
	if got := s.swap.generation(); got != 0 {
		t.Fatalf("quit admitted a conversation request: generation=%d", got)
	}
	loop := &manualLoop{}
	s.loop = loop.dispatch
	c.runAsk(s, chrome.AskRequest{Prompt: "fixture"})
	loop.drain()
	if s.shell.AskBusy || s.shell.AskProblem != errQuitting.Error() {
		t.Fatalf("Quick Ask was not refused during quit: busy=%v problem=%q", s.shell.AskBusy, s.shell.AskProblem)
	}
}

// A swap may have s.chat = B while A still owns the process barrier and
// another request is waiting. The drain must collect all three, withdraw the
// pending one, and wait on both teardown and stats producers.
func TestQuitCollectsEveryStatsProducer(t *testing.T) {
	c := &Coordinator{}
	a := newChatSession("/stub/fx", "/p", nil, nil)
	b := newChatSession("/stub/fx", "/p", nil, nil)
	pending := newChatSession("/stub/fx", "/p", nil, nil)
	s := &ProjectWindow{coord: c, project: "/p", chat: b}
	s.swap.window = s
	s.swap.gen = 1
	s.swap.started = a
	s.swap.waiting = pending
	c.sessions = []*ProjectWindow{s}

	drain := c.stopStatsProducers()
	if len(drain.chats) != 3 {
		t.Fatalf("drain collected %d producers, want 3", len(drain.chats))
	}
	if s.swap.waiting != nil || s.swap.generation() != 2 {
		t.Fatalf("pending replacement was not withdrawn: gen=%d", s.swap.generation())
	}
	for _, chat := range drain.chats {
		if !chat.producers.stopped() {
			t.Fatal("a collected producer was not stopped")
		}
		select {
		case <-chat.closedDone:
		case <-time.After(time.Second):
			t.Fatal("a collected process was not closed")
		}
	}
	if len(drain.wait) != 6 {
		t.Fatalf("drain waits on %d signals, want 2 per producer", len(drain.wait))
	}
	for _, done := range drain.wait {
		select {
		case <-done:
		default:
			t.Fatal("a drain signal was left open with no producer")
		}
	}
}

// The last accepted stats must be written after the producer that received
// the response has finished, and before Store.Close. A quit that continued
// early would persist the stale value.
func TestQuitWaitsForProducersBeforeFinalSnapshot(t *testing.T) {
	store := sessionstats.New(filepath.Join(t.TempDir(), "stats.json"))
	chat := newChatSession("/stub/fx", "/p", nil, nil)
	chat.stats = store
	chat.mu.Lock()
	chat.sessionID = "session-1"
	chat.inputTokens = 7
	chat.mu.Unlock()
	if !chat.producers.enter() {
		t.Fatal("could not take a producer slot")
	}
	c := &Coordinator{stats: store, quitBudget: 2 * time.Second}
	s := &ProjectWindow{coord: c, project: "/p", chat: chat}
	s.swap.window = s
	c.sessions = []*ProjectWindow{s}

	finished := make(chan struct{})
	c.beginQuit(&mygo.QuitEvent{}, func() { close(finished) })
	select {
	case <-finished:
		t.Fatal("quit continued while a stats producer was still running")
	case <-time.After(40 * time.Millisecond):
	}
	// The producer's last write lands after the quit began.
	chat.mu.Lock()
	chat.inputTokens = 99
	chat.mu.Unlock()
	chat.producers.exit()
	waitClosed(t, finished, "quit did not continue after producers finished")
	if got := store.Get("session-1"); got.InputTokens != 99 {
		t.Fatalf("final snapshot = %+v, want input tokens 99", got)
	}
}

// Once the drain has started, no new turn can be reserved and no new stats
// write can start; the single final snapshot covers it.
func TestQuitRefusesNewTurnsAndWrites(t *testing.T) {
	chat := newChatSession("/stub/fx", "/p", nil, nil)
	defer chat.Close()
	chat.mu.Lock()
	chat.ready = true
	chat.mu.Unlock()
	done := chat.producers.stop()
	select {
	case <-done:
	default:
		t.Fatal("empty producer group did not signal")
	}
	if chat.producers.enter() {
		t.Fatal("a new producer was admitted after stop()")
	}
	if chat.submit("one more turn", nil, nil) {
		t.Fatal("a new turn was reserved after the quit drain began")
	}
}
