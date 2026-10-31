package app

import (
	"runtime"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

func TestDeriveAttentionStates(t *testing.T) {
	cases := []struct {
		name string
		in   attentionInput
		want chrome.Attention
	}{
		{"idle", attentionInput{chat: true, ready: true}, chrome.AttentionIdle},
		{"connecting counts as working", attentionInput{chat: true}, chrome.AttentionWorking},
		{"busy", attentionInput{chat: true, ready: true, busy: true}, chrome.AttentionWorking},
		{"approval", attentionInput{chat: true, ready: true, approval: true}, chrome.AttentionNeedsApproval},
		{"errored", attentionInput{chat: true, ready: true, problem: true}, chrome.AttentionErrored},
		{"changes await review", attentionInput{chat: true, ready: true, changes: 2}, chrome.AttentionDoneUnreviewed},
		{"reviewed changes are idle", attentionInput{chat: true, ready: true, changes: 2, reviewed: true}, chrome.AttentionIdle},
		{"exited", attentionInput{exited: true}, chrome.AttentionExited},
	}
	for _, tc := range cases {
		if got := deriveAttention(tc.in); got != tc.want {
			t.Errorf("%s: deriveAttention = %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestDeriveAttentionPriority(t *testing.T) {
	// Approval beats a failure; a failure beats work in progress; work beats
	// unreviewed changes; unreviewed changes beat a clean exit.
	full := attentionInput{chat: true, ready: true, busy: true, approval: true, problem: true, changes: 3, exited: true}
	if got := deriveAttention(full); got != chrome.AttentionNeedsApproval {
		t.Fatalf("approval should win, got %s", got)
	}
	full.approval = false
	if got := deriveAttention(full); got != chrome.AttentionErrored {
		t.Fatalf("failure should win next, got %s", got)
	}
	full.problem = false
	if got := deriveAttention(full); got != chrome.AttentionWorking {
		t.Fatalf("work in progress should win next, got %s", got)
	}
	full.busy = false
	if got := deriveAttention(full); got != chrome.AttentionDoneUnreviewed {
		t.Fatalf("unreviewed changes should win next, got %s", got)
	}
	full.changes = 0
	if got := deriveAttention(full); got != chrome.AttentionExited {
		t.Fatalf("exit should win next, got %s", got)
	}
}

func TestSessionSummaryNamesCommandSession(t *testing.T) {
	s := &Session{title: "fx doctor"}
	sum := s.summary()
	if sum.Title != "fx doctor" {
		t.Fatalf("title = %q, want fx doctor", sum.Title)
	}
	if sum.ID != "cmd:fx doctor" {
		t.Fatalf("id = %q, want cmd:fx doctor", sum.ID)
	}
	if sum.Status != chrome.AttentionIdle {
		t.Fatalf("status = %s, want idle", sum.Status)
	}
}

func TestSessionSummaryFallsBackToProjectBase(t *testing.T) {
	s := &Session{project: "/home/me/work/thing"}
	if got := s.summary().Title; got != "thing" {
		t.Fatalf("title = %q, want thing", got)
	}
}

func TestSessionSummariesSortsRegistry(t *testing.T) {
	c := &Coordinator{}
	c.addSession(&Session{project: "/b", started: time.Now()})
	c.addSession(&Session{project: "/a", started: time.Now()})
	got := c.sessionSummaries()
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Workspace != "/a" || got[1].Workspace != "/b" {
		t.Fatalf("order = %q, %q", got[0].Workspace, got[1].Workspace)
	}
}

// The rail asks every session for a summary on every frame. That read must
// not copy the conversation: at 5000 messages it used to copy the whole
// transcript once per session per frame (§4.10.4).
func TestSessionSummariesDoNotCopyTranscripts(t *testing.T) {
	chat := &chatSession{ready: true, messages: make([]chrome.ChatMessage, 5000)}
	for i := range chat.messages {
		chat.messages[i] = chrome.ChatMessage{ID: uint64(i), Role: "assistant", Text: "x"}
	}
	c := &Coordinator{sessions: []*Session{{chat: chat, project: "/tmp/example", started: time.Now()}}}
	_ = c.sessionSummaries()

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 20; i++ {
		_ = c.sessionSummaries()
	}
	runtime.ReadMemStats(&after)
	if used := after.TotalAlloc - before.TotalAlloc; used > 200<<10 {
		t.Fatalf("sessionSummaries allocated %d bytes over 20 frames of a 5000-message session; want at most 200 KiB", used)
	}
}
