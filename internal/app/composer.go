package app

import (
	"context"
	"errors"
	"strings"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/workspace"
)

// prepareMentions is the file read the submit path performs. It is a variable
// so a test can hold it and prove the composer does not wait for the disk; the
// production value is the workspace reader.
var prepareMentions = resolveMentionsContext

// submitRequest is one composer submission whose referenced files are being
// read. Reading them is file I/O, and the UI thread must not wait for it, so
// the submission is accepted here and delivered to the conversation once the
// content is frozen (§4.2).
type submitRequest struct {
	// version is the draft generation this request belongs to: a newer
	// submission, or a cleared composer, retires it.
	version uint64
	// conversation is the generation of the chat session that must still be
	// the window's session when the files are ready. Replacing the
	// conversation — new project, resumed history, reconnect — retires the
	// request rather than delivering it to the wrong session.
	conversation uint64
	text         string
	images       []promptImage
	// sentIDs are the chips this submission carries, copied at submit time.
	// The composer's slice is compacted while the draft is edited, so keeping
	// a slice header here would describe different chips by the time the
	// files are ready (§B2).
	sentIDs []uint64
	// draftRevision is the draft version this submission was taken at: the
	// prompt clears the field only if the user has not edited since (§B5).
	draftRevision uint64
	mentions      bool
	// ctx and cancel bound the read and let the submission be retired: a
	// newer draft, a replaced conversation or a closing window cancels it
	// instead of letting it finish for nothing.
	ctx    context.Context
	cancel context.CancelFunc
}

// toPromptImages narrows the composer's attachments to what the wire carries.
// The preview and the id are for the chip, not for fx.
func toPromptImages(attachments []chrome.Attachment) []promptImage {
	if len(attachments) == 0 {
		return nil
	}
	images := make([]promptImage, 0, len(attachments))
	for _, a := range attachments {
		images = append(images, promptImage{Name: a.Name, MimeType: a.MimeType, Data: a.Data})
	}
	return images
}

// sendDraft is the composer's submit path. It validates cheaply, then prepares
// the message off the UI thread. It reports false because the draft is not
// consumed yet: the composer keeps what the user typed until the prompt is
// actually accepted, so a slow or failed read never eats the message.
func (s *ProjectWindow) sendDraft(text string, attachments []chrome.Attachment) bool {
	if s.chat == nil || s.project == "" {
		return false
	}
	// Count the edit this submit is based on: a keystroke that arrived since
	// the last frame is still an edit of the draft.
	s.observeDraft()
	text = strings.TrimSpace(text)
	if text == "" && len(attachments) == 0 {
		return false
	}
	if s.preparingRequest() != nil {
		// A submission is already being prepared; a second Enter must not
		// start a second read of the same draft.
		return false
	}
	st := s.chat.status()
	if !st.Ready || st.ConfigChanging {
		return false
	}
	// A running turn takes text alone: images wait for the next turn. The
	// composer already refuses this, but the check is repeated where the
	// promise is made.
	if st.Busy && len(attachments) > 0 {
		return false
	}
	if !st.Files || len(workspace.MentionPaths(text)) == 0 {
		// Nothing to read: the message is already complete, so it goes
		// straight to the conversation and the composer may clear it.
		s.chatUI.AttachError = ""
		return s.chat.submit(text, toPromptImages(attachments), nil)
	}
	ctx, cancel := context.WithTimeout(s.lifetime(), mentionTimeout)
	request := &submitRequest{
		text:          text,
		images:        toPromptImages(attachments),
		sentIDs:       attachmentIDs(attachments),
		draftRevision: s.draftRevision,
		mentions:      st.Files,
		ctx:           ctx,
		cancel:        cancel,
	}
	// The bookkeeping is short and lock-guarded: retiring a submission can
	// come from the app loop (a new draft, a conversation swap) or from the
	// window closing, and neither may tear the pointer.
	s.mu.Lock()
	request.version = s.draftVersion + 1
	request.conversation = s.swap.generation()
	s.draftVersion = request.version
	s.preparing = request
	s.mu.Unlock()
	s.chatUI.AttachError = ""
	s.invalidate()
	go s.prepareSubmit(request)
	return false
}

// prepareSubmit reads the files the message names. It runs entirely off the UI
// thread; the result is delivered back on it through dispatch, which also
// re-checks that the window and the conversation are still the ones that
// accepted the draft.
func (s *ProjectWindow) prepareSubmit(request *submitRequest) {
	defer request.cancel()
	var mentions []promptMention
	var problem string
	var cancelled error
	if request.mentions && s.project != "" {
		resolved, err := prepareMentions(request.ctx, s.project, request.text)
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// The read was retired, not refused. The submission must not go
			// out without the content it named (§B3).
			cancelled = err
		case err != nil:
			problem = err.Error()
		default:
			mentions = resolved
		}
	}
	s.dispatch(func() {
		if cancelled != nil {
			s.cancelSubmit(request, cancelled)
			return
		}
		s.acceptSubmit(request, mentions, problem)
	})
}

// cancelSubmit retires a submission whose files could not be read in time. A
// deadline is the user's business (they are waiting for a message that will
// not arrive); a retirement is not, because something newer already replaced
// the draft or the conversation.
func (s *ProjectWindow) cancelSubmit(request *submitRequest, err error) {
	s.mu.Lock()
	if s.preparing != request {
		s.mu.Unlock()
		return
	}
	s.preparing = nil
	s.mu.Unlock()
	if errors.Is(err, context.DeadlineExceeded) {
		s.chatUI.AttachError = "Reading the referenced files took too long. The message was not sent."
	}
	s.invalidate()
}

// acceptSubmit delivers a prepared message to the conversation, or reports why
// it could not. It runs on the UI thread, so the draft, the attachment chips
// and the conversation pointer are all stable while it decides.
func (s *ProjectWindow) acceptSubmit(request *submitRequest, mentions []promptMention, problem string) {
	s.mu.Lock()
	if s.preparing != request {
		s.mu.Unlock()
		// Superseded by a newer submission, a conversation swap or a close.
		return
	}
	current := s.swap.generation()
	s.preparing = nil
	s.mu.Unlock()
	if s.chat == nil || current != request.conversation {
		return
	}
	// An edit may have landed without an event (the control publishes its
	// text one frame later than the keystroke). Count it before deciding
	// whether this submission still owns the field (§B5, N2).
	if problem == "" {
		s.observeDraft()
	}
	if problem != "" {
		s.chatUI.AttachError = problem
		s.invalidate()
		return
	}
	if !s.chat.submit(request.text, request.images, mentions) {
		s.chatUI.AttachError = "This message could not be sent."
		s.invalidate()
		return
	}
	// Clear only the draft revision that was accepted: anything typed while
	// the files were being read stays in the composer (§B5).
	if s.draftRevision == request.draftRevision {
		s.chatUI.Draft = ""
		s.draftSeen = ""
	}
	s.chatUI.Attachments = withoutAttachments(s.chatUI.Attachments, request.sentIDs)
	s.invalidate()
}

// attachmentIDs copies the ids of the chips a submission carries. The ids are
// what a completed submission removes; the slice itself belongs to the
// composer and is edited while the message is prepared (§B2).
func attachmentIDs(attachments []chrome.Attachment) []uint64 {
	if len(attachments) == 0 {
		return nil
	}
	ids := make([]uint64, 0, len(attachments))
	for _, a := range attachments {
		ids = append(ids, a.ID)
	}
	return ids
}

// withoutAttachments returns a new slice without the ids that were sent. A
// chip added or removed while the message was prepared is left alone, and the
// composer's own slice is never rewritten in place: it is the array an
// in-flight submission may still be reading (§B2).
func withoutAttachments(current []chrome.Attachment, sent []uint64) []chrome.Attachment {
	if len(current) == 0 || len(sent) == 0 {
		return current
	}
	drop := make(map[uint64]bool, len(sent))
	for _, id := range sent {
		drop[id] = true
	}
	kept := make([]chrome.Attachment, 0, len(current))
	for _, a := range current {
		if !drop[a.ID] {
			kept = append(kept, a)
		}
	}
	return kept
}

// abandonSubmit retires a pending preparation. Called when the window closes
// or the conversation is replaced, so a result that arrives later cannot be
// delivered.
func (s *ProjectWindow) abandonSubmit() {
	s.mu.Lock()
	request := s.preparing
	s.preparing = nil
	s.mu.Unlock()
	if request != nil && request.cancel != nil {
		request.cancel()
	}
}

// preparingRequest is the submission being prepared, if any.
func (s *ProjectWindow) preparingRequest() *submitRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preparing
}

// preparingSubmit reports whether a submission is being prepared, for the
// composer's status line.
func (s *ProjectWindow) preparingSubmit() bool { return s.preparingRequest() != nil }
