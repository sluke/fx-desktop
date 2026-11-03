package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

// composerWindow is a project window with a live fixture conversation, but no
// native window: dispatch runs the closures where the work finished because
// there is no app loop to hand them to.
func composerWindow(t *testing.T, chat *chatSession) *ProjectWindow {
	t.Helper()
	s := &ProjectWindow{coord: NewCoordinator(), project: chat.project, chat: chat, shell: chrome.DefaultShell()}
	s.console.window = s
	s.swap.window = s
	s.swap.gen = 1
	return s
}

// manualLoop is an app loop a test owns. Dispatch queues the closure and the
// test drains it on its own goroutine, so the "UI work is serialized" contract
// is exercised instead of bypassed by the inline fallback.
type manualLoop struct {
	mu    sync.Mutex
	queue []func()
}

func (l *manualLoop) dispatch(fn func()) {
	l.mu.Lock()
	l.queue = append(l.queue, fn)
	l.mu.Unlock()
}

// await waits until the loop has work, so a test never drains before the
// worker has handed the result over.
func (l *manualLoop) await(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.mu.Lock()
		n := len(l.queue)
		l.mu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker never handed its result to the app loop")
		}
		time.Sleep(time.Millisecond)
	}
}

// drain runs everything queued, in order, on the caller's goroutine.
func (l *manualLoop) drain() {
	for {
		l.mu.Lock()
		if len(l.queue) == 0 {
			l.mu.Unlock()
			return
		}
		fn := l.queue[0]
		l.queue = l.queue[1:]
		l.mu.Unlock()
		fn()
	}
}

// loopWindow is composerWindow with a loop the test owns.
func loopWindow(t *testing.T, chat *chatSession) (*ProjectWindow, *manualLoop) {
	t.Helper()
	s := composerWindow(t, chat)
	loop := &manualLoop{}
	s.loop = loop.dispatch
	return s, loop
}

// A message with nothing to read goes straight to the conversation, and the
// composer may clear it: there is no preparation stage to wait for.
func TestSendDraftWithoutMentionsSubmitsImmediately(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	s := composerWindow(t, chat)
	if !s.sendDraft("hello there", nil) {
		t.Fatal("a complete message should be accepted on the spot")
	}
	if s.preparingSubmit() {
		t.Fatal("nothing needed preparing")
	}
	awaitChat(t, chat, func(snap chatSnapshot) bool { return snap.Permission != nil })
	snap := awaitChat(t, chat, func(snap chatSnapshot) bool { return len(snap.Messages) > 0 })
	if snap.Messages[0].Text != "hello there" {
		t.Fatalf("transcript: %+v", snap.Messages)
	}
}

// A message that names a file is prepared off the submit call: the draft is
// kept, the work runs on its own goroutine, and the prompt arrives with the
// file's content.
func TestSendDraftPreparesMentionedFiles(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	if err := os.WriteFile(filepath.Join(chat.project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := composerWindow(t, chat)
	if s.sendDraft("look at @main.go", nil) {
		t.Fatal("the composer must keep the draft until the file is read")
	}
	// The fixture validates the resource block it receives, so a mention that
	// carried the wrong content would end the turn with a Problem.
	awaitChat(t, chat, func(snap chatSnapshot) bool { return snap.Permission != nil })
	snap := awaitChat(t, chat, func(snap chatSnapshot) bool { return len(snap.Messages) > 0 })
	if snap.Messages[0].Text != "look at @main.go" {
		t.Fatalf("transcript: %+v", snap.Messages)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.preparingSubmit() {
		if time.Now().After(deadline) {
			t.Fatal("preparation never finished")
		}
		time.Sleep(time.Millisecond)
	}
	if snap := chat.snapshot(); snap.Problem != "" {
		t.Fatalf("fx refused the prepared message: %s", snap.Problem)
	}
}

// Submitting must not wait for the disk: the composer returns at once, the
// read runs on its own goroutine, and the message arrives when the file is
// ready. This is the property the old synchronous path broke.
func TestSendDraftDoesNotWaitForFileReads(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	if err := os.WriteFile(filepath.Join(chat.project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	started := make(chan struct{})
	original := prepareMentions
	prepareMentions = func(ctx context.Context, project, text string) ([]promptMention, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return original(ctx, project, text)
	}
	t.Cleanup(func() { prepareMentions = original })

	s := composerWindow(t, chat)
	done := make(chan bool, 1)
	go func() { done <- s.sendDraft("read @main.go", nil) }()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("a message with a file to read cannot be accepted on the spot")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("sendDraft blocked on the file read")
	}
	<-started
	if !s.preparingSubmit() {
		t.Fatal("the submission should be preparing")
	}
	if n := len(chat.snapshot().Messages); n != 0 {
		t.Fatalf("the message was delivered before its file was read: %d rows", n)
	}
	close(release)
	snap := awaitChat(t, chat, func(snap chatSnapshot) bool { return len(snap.Messages) > 0 })
	if snap.Messages[0].Text != "read @main.go" {
		t.Fatalf("transcript: %+v", snap.Messages)
	}
}

// Retiring a submission cancels the read it started, so a replaced
// conversation does not leave a worker reading files for nothing.
func TestAbandonSubmitCancelsTheRead(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	original := prepareMentions
	cancelled := make(chan struct{})
	prepareMentions = func(ctx context.Context, project, text string) ([]promptMention, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	t.Cleanup(func() { prepareMentions = original })

	s := composerWindow(t, chat)
	s.sendDraft("read @main.go", nil)
	deadline := time.After(5 * time.Second)
	for !s.preparingSubmit() {
		select {
		case <-deadline:
			t.Fatal("preparation never started")
		case <-time.After(time.Millisecond):
		}
	}
	s.abandonSubmit()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("retiring the submission did not cancel the read")
	}
}

// A file that cannot be attached refuses the message with the reason, and the
// draft stays exactly as the user left it.
func TestAcceptSubmitKeepsTheDraftWhenAFileIsRefused(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	big := bytes.Repeat([]byte("x"), maxMentionBytes+1)
	if err := os.WriteFile(filepath.Join(chat.project, "big.txt"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	s := composerWindow(t, chat)
	s.chatUI.Draft = "read @big.txt"
	request := &submitRequest{version: 1, conversation: s.swap.generation(), text: "read @big.txt", mentions: true, draftRevision: s.draftRevision}
	s.preparing = request
	mentions, err := resolveMentionsContext(context.Background(), s.project, request.text)
	if err == nil {
		t.Fatal("the oversized file was not refused")
	}
	s.acceptSubmit(request, mentions, err.Error())

	if !strings.Contains(s.chatUI.AttachError, "larger than") {
		t.Fatalf("the refusal was not explained: %q", s.chatUI.AttachError)
	}
	if s.chatUI.Draft != "read @big.txt" {
		t.Fatalf("the draft was lost: %q", s.chatUI.Draft)
	}
	if s.preparingSubmit() {
		t.Fatal("a refused message is not still preparing")
	}
	if n := len(chat.snapshot().Messages); n != 0 {
		t.Fatalf("a refused message reached the conversation: %d rows", n)
	}
}

// A prepared message whose conversation has been replaced is dropped: it
// belongs to the session that accepted it, not to whichever one is current.
func TestAcceptSubmitDropsAStaleConversation(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	s := composerWindow(t, chat)
	s.chatUI.Draft = "read @main.go"
	request := &submitRequest{version: 1, conversation: s.swap.generation() + 1, text: "read @main.go", draftRevision: s.draftRevision}
	s.preparing = request
	s.acceptSubmit(request, nil, "")

	if !strings.Contains(s.chatUI.Draft, "@main.go") {
		t.Fatal("a dropped message must not consume the draft")
	}
	if n := len(chat.snapshot().Messages); n != 0 {
		t.Fatalf("a stale message reached the new conversation: %d rows", n)
	}
}

// Only the chips that were sent are removed: an image added while the message
// was being prepared belongs to the next message.
func TestWithoutAttachmentsKeepsLaterChips(t *testing.T) {
	current := []chrome.Attachment{{ID: 1}, {ID: 2}, {ID: 3}}
	kept := withoutAttachments(current, []uint64{1, 3})
	if len(kept) != 1 || kept[0].ID != 2 {
		t.Fatalf("kept = %+v", kept)
	}
	// The composer's slice is not rewritten: an in-flight submission may still
	// be reading the backing array (§B2).
	if len(current) != 3 || current[0].ID != 1 {
		t.Fatalf("the source slice was mutated: %+v", current)
	}
}

// Closing a conversation that never connected must not wait for a process that
// does not exist: a replacement conversation waits on this signal.
func TestCloseWithoutConnectionSignalsImmediately(t *testing.T) {
	c := newChatSession("", "", nil, nil)
	c.Close()
	select {
	case <-c.closedDone:
	case <-time.After(2 * time.Second):
		t.Fatal("a conversation that never connected left its close signal waiting")
	}
}

// An abandoned conversation reports why it never started, and is closed so no
// caller waits for a connection that will not happen.
func TestAbandonedConversationReportsTheReason(t *testing.T) {
	c := newChatSession("", "", nil, nil)
	c.abandon(errors.New("the previous fx process did not exit"))
	if c.snapshot().Problem == "" {
		t.Fatal("an abandoned conversation must say why")
	}
	select {
	case <-c.closedDone:
	default:
		t.Fatal("an abandoned conversation must be closed")
	}
}

// B2: a submission must own the chips it was taken with. The composer's slice
// is edited in place while the files are read, so a captured slice header would
// describe different chips by the time the prompt is sent.
func TestPreparedSubmissionOwnsItsAttachmentIDs(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	if err := os.WriteFile(filepath.Join(chat.project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	original := prepareMentions
	prepareMentions = func(ctx context.Context, project, text string) ([]promptMention, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return original(ctx, project, text)
	}
	t.Cleanup(func() { prepareMentions = original })

	s, loop := loopWindow(t, chat)
	// Capacity 4 so the composer's own edit below reuses the array, exactly as
	// it does in the app.
	chips := make([]chrome.Attachment, 2, 4)
	chips[0] = chrome.Attachment{ID: 1, Name: "one.png"}
	chips[1] = chrome.Attachment{ID: 2, Name: "two.png"}
	s.chatUI.Attachments = chips
	if s.sendDraft("read @main.go", chips) {
		t.Fatal("a message with a file to read must not be accepted on the spot")
	}
	if !s.preparingSubmit() {
		t.Fatal("the submission should be preparing")
	}
	// While it is preparing: remove 1 (compacting the same array) and add 3
	// (reusing its capacity). The in-flight request must not see [2, 3].
	compact := append(chips[:0], chips[1])
	compact = append(compact, chrome.Attachment{ID: 3, Name: "three.png"})
	s.chatUI.Attachments = compact

	close(release)
	loop.await(t)
	loop.drain()
	awaitChat(t, chat, func(snap chatSnapshot) bool { return len(snap.Messages) > 0 })
	kept := s.chatUI.Attachments
	if len(kept) != 1 || kept[0].ID != 3 {
		t.Fatalf("chips after the send = %+v, want only the one added during preparation", kept)
	}
}

// B5: the prompt clears the draft only if the user has not edited since it was
// taken — same text typed again is a different draft.
func TestAcceptSubmitKeepsADraftEditedSinceSubmit(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	if err := os.WriteFile(filepath.Join(chat.project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	original := prepareMentions
	prepareMentions = func(ctx context.Context, project, text string) ([]promptMention, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return original(ctx, project, text)
	}
	t.Cleanup(func() { prepareMentions = original })

	s, loop := loopWindow(t, chat)
	s.chatUI.Draft = "read @main.go"
	s.observeDraft()
	if s.sendDraft("read @main.go", nil) {
		t.Fatal("a message with a file to read must not be accepted on the spot")
	}
	// The frame after the keystroke counts the edit; the text it submits here
	// is the same as the prepared one, which text equality alone would miss.
	s.chatUI.Draft = "read @main.go "
	s.observeDraft()
	close(release)
	loop.await(t)
	loop.drain()
	if s.chatUI.Draft != "read @main.go " {
		t.Fatalf("an edited draft was cleared: %q", s.chatUI.Draft)
	}
}

// B5 complement: an untouched draft is cleared when the prompt is accepted.
func TestAcceptSubmitClearsAnUntouchedDraft(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	if err := os.WriteFile(filepath.Join(chat.project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, loop := loopWindow(t, chat)
	s.chatUI.Draft = "read @main.go"
	s.observeDraft()
	if s.sendDraft("read @main.go", nil) {
		t.Fatal("a message with a file to read must not be accepted on the spot")
	}
	loop.await(t)
	loop.drain()
	if s.chatUI.Draft != "" {
		t.Fatalf("the accepted draft was kept: %q", s.chatUI.Draft)
	}
}

// N2: the revision cannot only be sampled at draw time. A keystroke that
// arrives after the submission was taken, but before the next frame repaints,
// is still an edit of the draft: publishing the submission must not clear it.
func TestReviewAcceptPreservesEditBeforeNextFrame(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	if err := os.WriteFile(filepath.Join(chat.project, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, loop := loopWindow(t, chat)
	s.chatUI.Draft = "read @main.go"
	if s.sendDraft("read @main.go", nil) {
		t.Fatal("a message with a file to read must not be accepted on the spot")
	}
	// No observeDraft and no frame: this is the same interval a keystroke
	// lands in while the files are being read.
	edited := "read @main.go and run it"
	s.chatUI.Draft = edited
	loop.await(t)
	loop.drain()
	if s.chatUI.Draft != edited {
		t.Fatalf("an edit made before the next frame was cleared: %q", s.chatUI.Draft)
	}
}

// N2, ABA: deleting the draft and retyping the same words is a new draft even
// though a sampled text comparison sees no change. The edit events are what
// carry the identity, so the revision must move on every one of them.
func TestDraftEditEventCountsEvenWhenTheTextReturns(t *testing.T) {
	s := &ProjectWindow{}
	s.chatUI.Draft = "same words"
	s.draftEdited()
	taken := s.draftRevision
	s.chatUI.Draft = ""
	s.draftEdited()
	s.chatUI.Draft = "same words"
	s.draftEdited()
	if s.draftRevision == taken {
		t.Fatal("clearing and retyping the same text did not count as an edit")
	}
}

// B3: a preparation that times out must not send the message as if it carried
// the files, and the draft must survive so the user can retry.
func TestSubmitTimesOutWithoutSending(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	original := prepareMentions
	prepareMentions = func(ctx context.Context, project, text string) ([]promptMention, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Cleanup(func() { prepareMentions = original })

	s, loop := loopWindow(t, chat)
	s.chatUI.Draft = "read @main.go"
	s.observeDraft()
	if s.sendDraft("read @main.go", nil) {
		t.Fatal("a message with a file to read must not be accepted on the spot")
	}
	request := s.preparingRequest()
	if request == nil {
		t.Fatal("the submission should be preparing")
	}
	// Expire the read the way the mention timeout would.
	request.cancel()
	loop.await(t)
	loop.drain()
	if s.preparingSubmit() {
		t.Fatal("a cancelled read was left preparing")
	}
	// A retirement (not a timeout) stays quiet; nothing is sent either way.
	if n := len(chat.snapshot().Messages); n != 0 {
		t.Fatalf("a cancelled preparation sent %d rows", n)
	}
	if s.chatUI.Draft != "read @main.go" {
		t.Fatalf("the draft was lost: %q", s.chatUI.Draft)
	}
}

// B3: a deadline is the user's business: it says so, and still sends nothing.
func TestSubmitDeadlineIsReported(t *testing.T) {
	testutil.IsolateHome(t)
	chat := chatFixture(t)
	s := composerWindow(t, chat)
	s.chatUI.Draft = "read @main.go"
	request := &submitRequest{version: 1, conversation: s.swap.generation(), text: "read @main.go", draftRevision: s.draftRevision}
	s.preparing = request
	s.cancelSubmit(request, context.DeadlineExceeded)
	if !strings.Contains(s.chatUI.AttachError, "took too long") {
		t.Fatalf("the timeout was not explained: %q", s.chatUI.AttachError)
	}
	if s.chatUI.Draft != "read @main.go" {
		t.Fatalf("the draft was lost: %q", s.chatUI.Draft)
	}
	if n := len(chat.snapshot().Messages); n != 0 {
		t.Fatalf("a timed-out preparation sent %d rows", n)
	}
}
