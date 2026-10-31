package app

import (
	"context"
	"time"

	"fx-desktop/internal/app/chrome"
)

// toggleReview opens or closes the review drawer.
//
// Opening it is also the acknowledgement §5 asks for: the changes are on
// screen now, so the rail stops raising them. The timestamp is what makes
// that stick — a change written after this moment is new again, and the green
// dot comes back on its own.
func (c *Coordinator) toggleReview(s *Session) {
	s.shell.ReviewOpen = !s.shell.ReviewOpen
	if !s.shell.ReviewOpen {
		s.invalidate()
		return
	}
	s.mu.Lock()
	s.reviewedAt = time.Now()
	first := ""
	if s.git != nil && len(s.git.Changes) > 0 {
		first = s.git.Changes[0].Path
	}
	s.mu.Unlock()
	// The drawer keeps whatever file was open, so reopening it resumes the
	// review instead of jumping back to the top of the list.
	if s.shell.ReviewPath == "" {
		s.shell.ReviewPath = first
	}
	s.invalidate()
	s.loadReviewDiff(s.shell.ReviewPath)
}

// closeReview hides the drawer without touching the watermark: closing a
// panel is not a decision about the changes.
func (c *Coordinator) closeReview(s *Session) {
	s.shell.ReviewOpen = false
	s.invalidate()
}

// selectChange opens one path's diff.
func (c *Coordinator) selectChange(s *Session, path string) {
	if s.shell.ReviewPath == path {
		return
	}
	s.shell.ReviewPath = path
	s.loadReviewDiff(path)
}

// loadReviewDiff reads one file's diff off the UI thread and repaints. A
// second selection while a read is in flight wins: the reply is dropped
// unless it is still the path on screen.
func (s *Session) loadReviewDiff(path string) {
	if s.project == "" {
		return
	}
	if path == "" {
		s.mu.Lock()
		s.reviewPath, s.reviewDiff, s.reviewMore, s.reviewNote, s.reviewBusy = "", nil, 0, "", false
		s.mu.Unlock()
		s.invalidate()
		return
	}
	s.mu.Lock()
	s.reviewPath = path
	s.reviewBusy = true
	s.reviewNote = ""
	s.mu.Unlock()
	s.invalidate()

	go func() {
		lines, omitted, err := gitFileDiff(context.Background(), s.project, path)
		s.mu.Lock()
		if s.reviewPath != path {
			// Another file was picked while this read ran.
			s.mu.Unlock()
			return
		}
		s.reviewBusy = false
		s.reviewDiff, s.reviewMore = lines, omitted
		switch {
		case err != nil:
			s.reviewNote = "git could not read this file's diff"
		case len(lines) == 0:
			// A rename with no content change, or a binary file: there is
			// nothing to colour, and saying so beats an empty pane.
			s.reviewNote = "No text changes to show in this file"
		default:
			s.reviewNote = ""
		}
		s.mu.Unlock()
		s.invalidate()
	}()
}

// sessionReview projects the working tree into the drawer. It reads only; the
// render pass must not start a git call of its own.
func (c *Coordinator) sessionReview(s *Session) chrome.ReviewData {
	d := chrome.ReviewData{Open: s.shell.ReviewOpen, Selected: s.shell.ReviewPath}
	s.mu.Lock()
	git := s.git
	err := s.gitErr
	d.Selected = s.reviewPath
	d.Diff = s.reviewDiff
	d.Omitted = s.reviewMore
	d.Note = s.reviewNote
	d.Busy = s.reviewBusy
	s.mu.Unlock()
	d.Workspace = workspaceState(git, err)
	if git != nil {
		d.Branch = git.Branch
		d.Changes = git.Changes
	}
	return d
}
