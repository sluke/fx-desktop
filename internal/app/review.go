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
	// review instead of jumping back to the top of the list — as long as
	// that file is still a change. One that was committed or reverted while
	// the drawer was closed is not resumed: the drawer would open on a file
	// that is not in its own list.
	if path := s.shell.ReviewPath; path != "" {
		if known, listed, _ := s.workspace.reviewVerdict(path); known && !listed {
			s.shell.ReviewPath = ""
		}
	}
	if s.shell.ReviewPath == "" {
		s.shell.ReviewPath = first
	}
	s.invalidate()
	s.loadReviewDiff(s.shell.ReviewPath)
}

// reconcileReview brings the drawer's file in line with a working-tree read
// that has just been published. The list and the diff are separate reads, and
// nothing used to connect them: after an edit the list said +2 while the pane
// still showed the old diff, and after a commit the pane kept showing a diff
// for a clean tree. The drawer is where a change is read before "Stage
// everything and commit", so what it shows has to be the file.
//
// It runs on the app loop. A file that is no longer a change loses its diff.
// One that is still a change is read again only when the read says something
// new about it, so a quiet tree costs no git call per refresh.
func (s *ProjectWindow) reconcileReview() {
	path := s.shell.ReviewPath
	if path == "" {
		return
	}
	known, listed, stale := s.workspace.reviewVerdict(path)
	switch {
	case !known:
		return
	case !listed:
		s.shell.ReviewPath = ""
		s.workspace.clearReview()
		s.invalidate()
	case stale && s.shell.ReviewOpen:
		// Closed, the drawer reloads when it is opened; there is nobody to
		// read the diff until then.
		s.loadReviewDiff(path)
	}
}

// closeReview hides the drawer without touching the watermark: closing a
// panel is not a decision about the changes.
func (c *Coordinator) closeReview(s *ProjectWindow) {
	s.shell.ReviewOpen = false
	s.invalidate()
}

// selectChange opens one path's diff. Choosing the file that is already open
// reads it again — the obvious way to retry a failed read or to ask for the
// current diff — unless that read is still in flight.
func (c *Coordinator) selectChange(s *ProjectWindow, path string) {
	if s.shell.ReviewPath == path && s.workspace.reviewLoading(path) {
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
		lines, omitted, truncated, err := gitFileDiff(scope, s.project, path)
		if scope.Err() != nil {
			return
		}
		if s.workspace.publishReview(gen, path, lines, omitted, truncated, err) {
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
		d.ListTruncated = git.ListTruncated
		d.CountsUnknown = git.CountsUnknown
	}
	return d
}
