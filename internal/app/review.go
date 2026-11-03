package app

import (
	"time"

	"fx-desktop/internal/app/chrome"
)

// toggleReview opens or closes the review drawer.
//
// Opening it is also the acknowledgement §5 asks for: the changes are on
// screen now, so the rail stops raising them. The timestamp is what makes
// that stick — a change written after this moment is new again, and the green
// dot comes back on its own.
func (c *Coordinator) toggleReview(s *ProjectWindow) {
	s.shell.ReviewOpen = !s.shell.ReviewOpen
	if !s.shell.ReviewOpen {
		s.invalidate()
		return
	}
	s.workspace.markReviewed(time.Now())
	first := ""
	if git, _ := s.workspace.gitSnapshot(); git != nil && len(git.Changes) > 0 {
		first = git.Changes[0].Path
	}
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
func (c *Coordinator) closeReview(s *ProjectWindow) {
	s.shell.ReviewOpen = false
	s.invalidate()
}

// selectChange opens one path's diff.
func (c *Coordinator) selectChange(s *ProjectWindow, path string) {
	if s.shell.ReviewPath == path {
		return
	}
	s.shell.ReviewPath = path
	s.loadReviewDiff(path)
}

// loadReviewDiff reads one file's diff off the UI thread and repaints. A
// second selection while a read is in flight wins: the reply is dropped
// unless it is still the path on screen.
func (s *ProjectWindow) loadReviewDiff(path string) {
	if s.project == "" {
		return
	}
	if path == "" {
		s.workspace.clearReview()
		s.invalidate()
		return
	}
	// The selection is ticketed here, on the app loop, before the read
	// starts: A→B→A gives each A its own generation, so the first A's late
	// answer cannot pass on a path comparison alone (N4).
	gen := s.workspace.beginReviewRead(path)
	s.invalidate()

	// The scope belongs to the selection, so it is claimed here rather than
	// in the goroutine: otherwise two selections racing to start could have
	// the older one cancel the newer one's read.
	scope, stop := s.scope("review")
	go func() {
		defer stop()
		lines, omitted, err := gitFileDiff(scope, s.project, path)
		if scope.Err() != nil {
			return
		}
		if s.workspace.publishReview(gen, path, lines, omitted, err) {
			s.invalidate()
		}
	}()
}

// sessionReview projects the working tree into the drawer. It reads only; the
// render pass must not start a git call of its own.
func (c *Coordinator) sessionReview(s *ProjectWindow) chrome.ReviewData {
	d := chrome.ReviewData{Open: s.shell.ReviewOpen, Selected: s.shell.ReviewPath}
	s.workspace.mu.Lock()
	git := s.workspace.git
	err := s.workspace.gitErr
	d.Selected = s.workspace.reviewPath
	d.Diff = s.workspace.reviewDiff
	d.Omitted = s.workspace.reviewMore
	d.Note = s.workspace.reviewNote
	d.Busy = s.workspace.reviewBusy
	s.workspace.mu.Unlock()
	d.Workspace = workspaceState(git, err)
	if git != nil {
		d.Branch = git.Branch
		d.Changes = git.Changes
	}
	return d
}
