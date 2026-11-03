package app

import (
	"path/filepath"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

// R3, end to end: the window is gone, the substitute conversation is already
// on screen, and the old fx process is still alive. History must still refuse
// to delete that conversation, because the process is still writing it.
func TestWindowClosedButUnreapedSessionCannotBeDeleted(t *testing.T) {
	// A process that ignores stdin EOF keeps the teardown open for the whole
	// grace period, which is the window this test needs.
	old, dir := lifecycleSession(t, "initialize", true, "--resume", "still-writing")
	coord := NewCoordinator()
	if err := old.attachRegistry(&coord.sessionUse); err != nil {
		t.Fatal(err)
	}
	go old.connect()
	waitForFile(t, filepath.Join(dir, "stalled"))

	window := &ProjectWindow{coord: coord, project: old.project, chat: old, shell: chrome.DefaultShell()}
	coord.addSession(window)

	// The user switches conversations: the window shows a replacement before
	// the old process is gone.
	substitute := newChatSession("", old.project, nil, nil)
	window.chat = substitute
	old.Close()
	// And the window itself goes away.
	coord.removeSession(window)

	if !coord.sessionUse.inUse("still-writing") {
		t.Fatal("a conversation whose process is still alive lost its protection when its window closed")
	}
	if _, ok := coord.sessionUse.beginDelete("still-writing"); ok {
		t.Fatal("a deletion was admitted for a conversation the app is still writing")
	}
	if got := coord.liveSessionIDs(); !got["still-writing"] {
		t.Fatalf("history would offer the live conversation for deletion: %v", got)
	}

	select {
	case <-old.closedDone:
	case <-time.After(shutdownTimeout * 2):
		t.Fatal("the old process was never reaped")
	}
	if coord.sessionUse.inUse("still-writing") {
		t.Fatal("the claim outlived the reaped process")
	}
	if _, ok := coord.sessionUse.beginDelete("still-writing"); !ok {
		t.Fatal("a reaped conversation stayed undeletable")
	}
}
