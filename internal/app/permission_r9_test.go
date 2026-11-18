package app

// Round-9 regressions for approvals and quitting. Synthetic data and a fake
// ACP agent only.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"fx-desktop/internal/acp"
	"fx-desktop/internal/sessionstats"
)

// TestRegressionR9_HelperACP is the fake agent. It models an agent that follows
// the ACP prompt-turn contract literally: a turn that asked for permission
// does not resolve until every session/request_permission it issued has been
// answered (after session/cancel the expected answer is the "cancelled"
// outcome). Whether real fx behaves this way is NOT established by this probe.
func TestRegressionR9_HelperACP(t *testing.T) {
	mode := os.Getenv("FX_R9_HELPER")
	if mode == "" {
		return
	}
	logPath := os.Getenv("FX_R9_LOG")
	logf := func(line string) {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		f.WriteString(line + "\n")
		f.Close()
	}
	out := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)
	const sid = "r9-session"
	var promptID json.RawMessage
	var asked []string
	answered := map[string]bool{}
	cancelled := false
	maybeFinish := func() {
		if promptID == nil {
			return
		}
		for _, id := range asked {
			if !answered[id] {
				return
			}
		}
		reason := "end_turn"
		if cancelled {
			reason = "cancelled"
		}
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]string{"stopReason": reason}})
		logf("turn-resolved " + reason)
		promptID = nil
	}
	ask := func(id string) {
		asked = append(asked, `"`+id+`"`)
		out.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/request_permission", "params": map[string]any{
			"sessionId": sid,
			"toolCall":  map[string]any{"title": "Tool " + id, "rawInput": map[string]string{"path": id}},
			"options":   []map[string]string{{"optionId": "allow_once", "name": "Allow once", "kind": "allow_once"}},
		}})
	}
	for scanner.Scan() {
		var p acp.Packet
		if json.Unmarshal(scanner.Bytes(), &p) != nil {
			os.Exit(2)
		}
		switch p.Method {
		case "initialize":
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}}})
		case "session/new":
			out.Encode(map[string]any{"jsonrpc": "2.0", "id": p.ID, "result": map[string]any{"sessionId": sid}})
		case "session/prompt":
			promptID = p.ID
			ask("perm-1")
			if mode == "two" {
				ask("perm-2")
			}
			out.Encode(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": sid, "update": map[string]string{"sessionUpdate": "notice", "title": "requests-sent"}}})
		case "session/cancel":
			cancelled = true
			logf("cancel-received")
			if mode == "late" {
				// A tool that was already on its way asks after the cancel.
				ask("perm-late")
			}
			maybeFinish()
		case "":
			logf("answered " + string(p.ID) + " " + string(p.Result))
			answered[string(p.ID)] = true
			maybeFinish()
		}
	}
	os.Exit(0)
}

func r9Chat(t *testing.T, mode string) (*chatSession, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "agent.log")
	t.Setenv("FX_R9_HELPER", mode)
	t.Setenv("FX_R9_LOG", logPath)
	t.Setenv("FX_R9_BINARY", executable)
	file := filepath.Join(t.TempDir(), "fake-fx")
	if err = os.WriteFile(file, []byte("#!/bin/sh\nexec \"$FX_R9_BINARY\" -test.run='^TestRegressionR9_HelperACP$' -- \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c := newChatSession(file, t.TempDir(), nil, nil)
	t.Cleanup(func() {
		c.Close()
		select {
		case <-c.closedDone:
		case <-time.After(8 * time.Second):
			t.Log("probe cleanup: fake agent teardown not confirmed")
		}
	})
	go c.connect()
	awaitChat(t, c, func(s chatSnapshot) bool { return s.Ready })
	return c, logPath
}

func r9Log(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

func r9HasNotice(s chatSnapshot, want string) bool {
	for _, n := range s.Notices {
		if n == want {
			return true
		}
	}
	return false
}

// Two tool calls ask for approval before the user answers the first. The user
// answers every request the window presents. The turn must be able to end.
func TestRegressionR9_SecondPermissionRequestMustNotLoseTheFirst(t *testing.T) {
	c, logPath := r9Chat(t, "two")
	if !c.submit("run two tools", nil, nil) {
		t.Fatal("not submitted")
	}
	awaitChat(t, c, func(s chatSnapshot) bool { return r9HasNotice(s, "requests-sent") && s.Permission != nil })
	var presented []string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s := c.snapshot()
		if !s.Busy {
			break
		}
		if s.Permission != nil {
			presented = append(presented, s.Permission.ID)
			c.choosePermission(s.Permission.ID, "allow_once")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s := c.snapshot()
	if s.Busy {
		t.Fatalf("turn still running 3s after every presented approval was answered.\n presented to the user: %v\n pending in UI now: %v\n agent log:\n%s",
			presented, s.Permission != nil, r9Log(logPath))
	}
	// One at a time, in the order fx asked.
	if len(presented) != 2 || presented[0] != `"perm-1"` || presented[1] != `"perm-2"` {
		t.Errorf("approvals presented = %v, want perm-1 then perm-2", presented)
	}
	if s.Permission != nil {
		t.Errorf("an approval is still shown after the turn ended: %+v", s.Permission)
	}
}

// An approval that arrives after Stop — a tool that was already on its way —
// is answered with the cancelled outcome too, and is never put on screen.
func TestRegressionR9_PermissionArrivingWhileStoppingIsAnswered(t *testing.T) {
	c, logPath := r9Chat(t, "late")
	if !c.submit("run one tool", nil, nil) {
		t.Fatal("not submitted")
	}
	awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	c.stopTurn()
	if s := c.snapshot(); s.Permission != nil {
		t.Errorf("the approval stayed on screen after Stop: %+v", s.Permission)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.snapshot().Busy {
		if s := c.snapshot(); s.Permission != nil {
			t.Errorf("an approval was presented while the turn was stopping: %s", s.Permission.ID)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s := c.snapshot(); s.Busy {
		t.Fatalf("turn still running 3s after Stop: stopping=%v\n agent log:\n%s", s.Stopping, r9Log(logPath))
	}
	log := r9Log(logPath)
	for _, id := range []string{`"perm-1"`, `"perm-late"`} {
		if !strings.Contains(log, "answered "+id+` {"outcome":{"outcome":"cancelled"}}`) {
			t.Errorf("%s was not answered with the cancelled outcome\n agent log:\n%s", id, log)
		}
	}
	if !strings.Contains(log, "turn-resolved cancelled") {
		t.Errorf("the turn did not resolve as cancelled\n agent log:\n%s", log)
	}
}

// The queue is bounded, keeps arrival order, and holds one entry per id.
func TestRegressionR9_PermissionQueueIsBoundedAndOrdered(t *testing.T) {
	c := newChatSession("/stub/fx", t.TempDir(), nil, nil)
	t.Cleanup(c.Close)
	request := func(id, title string) acp.Packet {
		params, err := json.Marshal(map[string]any{
			"toolCall": map[string]any{"title": title},
			"options":  []map[string]string{{"optionId": "allow_once", "name": "Allow once", "kind": "allow_once"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return acp.Packet{JSONRPC: "2.0", ID: json.RawMessage(strconv.Quote(id)), Method: "session/request_permission", Params: params}
	}
	for i := 0; i < maxPendingPermissions+10; i++ {
		c.event(request(fmt.Sprintf("p%02d", i), "first"))
	}
	// A repeated id replaces its entry instead of adding one.
	c.event(request("p00", "shown again"))
	c.event(request("p05", "queued again"))

	s := c.snapshot()
	if s.Permission == nil || s.Permission.ID != `"p00"` || s.Permission.Title != "shown again" {
		t.Fatalf("shown approval = %+v, want p00 with its newest title", s.Permission)
	}
	c.mu.Lock()
	queued := len(c.permissionQueue)
	fifth := c.permissionQueue[4]
	ids := c.takePermissionsLocked()
	empty := c.permission == nil && c.permissionQueue == nil
	c.mu.Unlock()
	if queued != maxPendingPermissions {
		t.Errorf("%d approvals queued, want the bound %d", queued, maxPendingPermissions)
	}
	if fifth.ID != `"p05"` || fifth.Title != "queued again" {
		t.Errorf("queued entry 5 = %+v, want p05 replaced in place", fifth)
	}
	if len(ids) != maxPendingPermissions+1 || ids[0] != `"p00"` || ids[1] != `"p01"` || ids[len(ids)-1] != fmt.Sprintf("%q", fmt.Sprintf("p%02d", maxPendingPermissions)) {
		t.Errorf("taken ids: %d entries, first %v, last %v", len(ids), ids[:2], ids[len(ids)-1])
	}
	if !empty {
		t.Error("taking the approvals left some behind")
	}
}

// A permission is pending and the user presses Stop. ACP requires the client
// to answer pending session/request_permission requests with the cancelled
// outcome; an agent that waits for that answer must still be able to end.
func TestRegressionR9_StopWithPendingPermissionMustAnswerIt(t *testing.T) {
	c, logPath := r9Chat(t, "one")
	if !c.submit("run one tool", nil, nil) {
		t.Fatal("not submitted")
	}
	s := awaitChat(t, c, func(s chatSnapshot) bool { return s.Permission != nil })
	id := s.Permission.ID
	c.stopTurn()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && c.snapshot().Busy {
		time.Sleep(5 * time.Millisecond)
	}
	s = c.snapshot()
	if s.Busy {
		// Show there is no way out from the UI either: Stop is disabled while
		// stopping, and the approval buttons are refused.
		c.choosePermission(id, "allow_once")
		c.stopTurn()
		time.Sleep(300 * time.Millisecond)
		after := c.snapshot()
		t.Fatalf("turn still running 3s after Stop with a pending approval.\n busy=%v stopping=%v permissionStillShown=%v (after retrying approval and Stop: busy=%v stopping=%v)\n agent log:\n%s",
			s.Busy, s.Stopping, s.Permission != nil, after.Busy, after.Stopping, r9Log(logPath))
	}
}

// MyGo's quit closes every window first and gives up when one refuses
// (mygo v0.7.1 app.go prepareQuit, window.go close). The project window's
// "Close window to menu bar" listener used to refuse always, so Quit was
// impossible with the option on — after the drain had already closed the
// conversations and the stats store. The listener now asks closeHidesWindow.
func TestRegressionR9_CloseToTrayDoesNotVetoQuit(t *testing.T) {
	store := sessionstats.New(filepath.Join(t.TempDir(), "session-stats.json"))
	c := &Coordinator{stats: store, quitBudget: 500 * time.Millisecond}
	if !c.closeHidesWindow() {
		t.Fatal("before a quit, closing a window must still hide it to the tray")
	}
	done := make(chan struct{})
	event := &mygo.QuitEvent{}
	c.beginQuit(event, func() {
		// This is where MyGo closes the windows: the listener must let go.
		if c.closeHidesWindow() {
			t.Error("the close-to-tray listener would still refuse the close while the app quits")
		}
		close(done)
	})
	if !event.DefaultPrevented() {
		t.Fatal("the first quit request must be held for the drain")
	}
	if c.closeHidesWindow() {
		t.Error("a window closed during the drain must close, not hide")
	}
	waitClosed(t, done, "the drain never handed over to quit")
}
