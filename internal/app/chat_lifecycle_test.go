package app

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"fx-desktop/internal/acp"
)

// TestChatLifecycleProcess is the simulated fx these tests run. It is not a
// stub inside the client: it is a real child process with a real stdin, so a
// teardown that kills instead of closing stdin is observable as a missing
// cleanup marker.
//
// FX_DESKTOP_ACP_LIFECYCLE_STALL names the opening RPC that never answers.
// FX_DESKTOP_ACP_LIFECYCLE_IGNORE_EOF keeps the child alive after stdin closes,
// which is the process that only a kill can end.
func TestChatLifecycleProcess(t *testing.T) {
	if os.Getenv("FX_DESKTOP_ACP_LIFECYCLE") != "1" {
		return
	}
	stall := os.Getenv("FX_DESKTOP_ACP_LIFECYCLE_STALL")
	dir := os.Getenv("FX_DESKTOP_ACP_LIFECYCLE_DIR")
	mark := func(name string) { _ = os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600) }
	// "started" is written before the first frame is read, so a test can tell
	// that a real child exists at a point where the client has published no
	// handle yet.
	mark("started")
	out := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var p acp.Packet
		if json.Unmarshal(scanner.Bytes(), &p) != nil {
			os.Exit(2)
		}
		switch p.Method {
		case "initialize":
			mark("received-initialize")
			if stall == "initialize" {
				mark("stalled")
				continue
			}
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{},
			}})
		case "session/new", "session/load":
			mark("received-" + strings.ReplaceAll(p.Method, "/", "-"))
			if stall == p.Method {
				mark("stalled")
				continue
			}
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]any{"sessionId": "lifecycle-session"}})
		}
	}
	// stdin reached EOF: this is fx's cue to cancel its own tools and leave.
	// The marker is written first so a test can tell "cleaned up" from
	// "killed", and the delay sits inside the grace period on purpose.
	mark("cleanup")
	if os.Getenv("FX_DESKTOP_ACP_LIFECYCLE_IGNORE_EOF") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	// The delay is inside the grace period on purpose: a teardown that kills
	// as soon as the handshake fails never sees the second marker, while one
	// that closes stdin and waits does.
	time.Sleep(200 * time.Millisecond)
	mark("cleanup-complete")
	os.Exit(0)
}

// lifecycleSession builds a chat session backed by that child. stall names the
// RPC that stays unanswered; ignoreEOF leaves a child that must be killed.
func lifecycleSession(t *testing.T, stall string, ignoreEOF bool, args ...string) (*chatSession, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("FX_DESKTOP_ACP_LIFECYCLE", "1")
	t.Setenv("FX_DESKTOP_ACP_LIFECYCLE_BINARY", binary)
	t.Setenv("FX_DESKTOP_ACP_LIFECYCLE_STALL", stall)
	t.Setenv("FX_DESKTOP_ACP_LIFECYCLE_DIR", dir)
	if ignoreEOF {
		t.Setenv("FX_DESKTOP_ACP_LIFECYCLE_IGNORE_EOF", "1")
	} else {
		t.Setenv("FX_DESKTOP_ACP_LIFECYCLE_IGNORE_EOF", "0")
	}
	executable := filepath.Join(dir, "fx")
	body := "#!/bin/sh\nexec \"$FX_DESKTOP_ACP_LIFECYCLE_BINARY\" -test.run=^TestChatLifecycleProcess$ -- \"$@\"\n"
	if err := os.WriteFile(executable, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	c := newChatSession(executable, t.TempDir(), args, nil)
	t.Cleanup(c.cancel)
	return c, dir
}

// waitForFile waits until the child recorded one step of its own progress.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child never reached %s", filepath.Base(path))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// R1: the completion signal is evidence about data that is already readable.
// The publication boundary is where that is decided, so the assertion lives
// there — the defect was an announcement that overtook its own data, not a
// data race.
func TestCreationResultIsPublishedBeforeItIsAnnounced(t *testing.T) {
	c, _ := lifecycleSession(t, "initialize", false)
	observed := make(chan struct{})
	c.publishHook = func() {
		c.mu.Lock()
		published := c.client != nil
		c.mu.Unlock()
		announced := false
		select {
		case <-c.processDone:
			announced = true
		default:
		}
		if !published {
			t.Error("the creation result was announced before it was published")
		}
		if announced {
			t.Error("processDone was closed before the creation result was published")
		}
		close(observed)
	}
	go c.connect()
	select {
	case <-observed:
	case <-time.After(10 * time.Second):
		t.Fatal("fx was never created")
	}
	c.Close()
	select {
	case <-c.closedDone:
	case <-time.After(shutdownTimeout + 2*time.Second):
		t.Fatal("the close never reached a result")
	}
}

// R2: a handshake that fails on its own — a cancelled or timed-out initialize
// — must hand the process to the teardown owner rather than kill it from the
// error branch. Killing there skips fx's own stdin-EOF cleanup, which is the
// difference the child's marker records.
func TestCancelledHandshakeHandsTheProcessToTheTeardownOwner(t *testing.T) {
	c, dir := lifecycleSession(t, "initialize", false)
	go c.connect()
	waitForFile(t, filepath.Join(dir, "stalled"))

	// The same cancellation a close performs, without the close: this is a
	// start that failed, not a session that was closed.
	c.handshakeCancel()
	select {
	case <-c.closedDone:
	case <-time.After(shutdownTimeout + 2*time.Second):
		t.Fatal("a failed start left the process without an owner")
	}
	waitForFile(t, filepath.Join(dir, "cleanup-complete"))
	if reaped, problem := c.CloseResult(); !reaped || problem != nil {
		t.Fatalf("reaped=%v problem=%v", reaped, problem)
	}
}

// R1: the creation result is one publication. A close that arrives while the
// process exists but its handle is not yet published must not report a reaped
// process — the old order could, and the swap barrier trusted it.
func TestProcessHandleIsPublishedBeforeTheCloseBarrier(t *testing.T) {
	c, dir := lifecycleSession(t, "initialize", false)
	reached := make(chan struct{})
	release := make(chan struct{})
	c.startedHook = func() {
		close(reached)
		<-release
	}
	go c.connect()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("fx was never created")
	}
	waitForFile(t, filepath.Join(dir, "started"))

	c.mu.Lock()
	published := c.client != nil
	c.mu.Unlock()
	if published {
		t.Fatal("the handle was readable before it was published")
	}

	c.Close()
	select {
	case <-c.closedDone:
		t.Fatal("the teardown reported a result while the process was still unreaped")
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	select {
	case <-c.closedDone:
	case <-time.After(shutdownTimeout + 2*time.Second):
		t.Fatal("the close never reached a result")
	}
	reaped, problem := c.CloseResult()
	if !reaped || problem != nil {
		t.Fatalf("the close reported reaped=%v problem=%v, want a confirmed exit", reaped, problem)
	}
	// The process really is gone, and it left through stdin EOF rather than
	// a kill: it finished its own cleanup.
	waitForFile(t, filepath.Join(dir, "cleanup-complete"))
}

// R2: a handshake cancelled by Close must still let fx perform its own stdin
// EOF cleanup, for all three opening RPCs. The old code killed the process from
// the error branch, which skipped it.
func TestHandshakeCloseLetsFxCleanUp(t *testing.T) {
	cases := []struct {
		stall string
		args  []string
	}{
		{"initialize", nil},
		{"session/new", nil},
		{"session/load", []string{"--resume", "saved-session"}},
	}
	for _, tc := range cases {
		t.Run(tc.stall, func(t *testing.T) {
			c, dir := lifecycleSession(t, tc.stall, false, tc.args...)
			go c.connect()
			waitForFile(t, filepath.Join(dir, "stalled"))

			c.Close()
			c.Close() // idempotent: a second close must not start a second teardown
			select {
			case <-c.closedDone:
			case <-time.After(shutdownTimeout + 2*time.Second):
				t.Fatal("the close never reached a result")
			}
			if reaped, problem := c.CloseResult(); !reaped || problem != nil {
				t.Fatalf("reaped=%v problem=%v", reaped, problem)
			}
			waitForFile(t, filepath.Join(dir, "cleanup-complete"))
		})
	}
}

// R2: a process that ignores stdin EOF is the one case a kill is for — and it
// happens only after the grace period, not as the first resort.
func TestCloseTerminatesOnlyAfterTheGracePeriod(t *testing.T) {
	c, dir := lifecycleSession(t, "initialize", true)
	go c.connect()
	waitForFile(t, filepath.Join(dir, "stalled"))

	started := time.Now()
	c.Close()
	select {
	case <-c.closedDone:
	case <-time.After(shutdownTimeout + 2*time.Second):
		t.Fatal("the close never reached a result")
	}
	if elapsed := time.Since(started); elapsed < shutdownGrace/2 {
		t.Fatalf("the process was killed after %v, before its stdin-EOF grace", elapsed)
	}
	if reaped, problem := c.CloseResult(); !reaped || problem != nil {
		t.Fatalf("a killed process is not a reaped one: reaped=%v problem=%v", reaped, problem)
	}
	// It did see the EOF first: the kill was the fallback, not the start.
	if _, err := os.Stat(filepath.Join(dir, "cleanup")); err != nil {
		t.Fatalf("stdin was not closed before the kill: %v", err)
	}
}

// §4.1: a late Exited is evidence. Once the process is confirmed gone, a stale
// "could not confirm" must not keep blocking replacements forever.
func TestAConfirmedLateExitLiftsAnUnconfirmedBarrier(t *testing.T) {
	s, started, mu := swapWindow(t)
	a := newChatSession("", "a", nil, nil)
	s.swap.request(a)
	waitStarted(t, started, mu, "a")

	b := newChatSession("", "b", nil, nil)
	s.swap.request(b)
	a.finishClose(false, errors.New("fx did not exit"))
	select {
	case <-b.closedDone:
	case <-time.After(5 * time.Second):
		t.Fatal("a refused replacement was left waiting")
	}
	if got := startedNow(started, mu); len(got) != 1 {
		t.Fatalf("a replacement started on an unconfirmed exit: %v", got)
	}

	// The process exits afterwards. The reason was real when recorded and is
	// stale now; a new request must be admissible.
	a.markReaped()
	c := newChatSession("", "c", nil, nil)
	s.swap.request(c)
	waitStarted(t, started, mu, "a", "c")
	c.Close()
}

// Close is idempotent and safe to call from several goroutines: exactly one
// teardown, one result, one signal.
func TestCloseIsIdempotentUnderConcurrency(t *testing.T) {
	c := newChatSession("", "", nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait()
	select {
	case <-c.closedDone:
	case <-time.After(2 * time.Second):
		t.Fatal("a close that never started left its signal waiting")
	}
	if reaped, problem := c.CloseResult(); !reaped || problem != nil {
		t.Fatalf("a session with no process reported reaped=%v problem=%v", reaped, problem)
	}
}
