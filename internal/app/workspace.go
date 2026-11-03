package app

import (
	"sync"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/workspace"
)

// workspaceController owns what the app has read about one window's workspace:
// the git snapshot, the review drawer's diff, the file browser and the @
// picker's file list. Every one of those is produced by a background read, so
// it carries its own lock: a slow `git status` must not contend with the
// conversation's state, and closing the window cancels the reads rather than
// leaving each to invent a scope of its own (§4.4).
//
// The workspace path itself is immutable for a window's lifetime and lives on
// the window as `project`: a path is not state, and replacing it would be
// replacing the window.
type workspaceController struct {
	mu sync.Mutex

	git    *GitInfo
	gitErr error
	// gitGen names the newest working-tree read. A read is published only
	// while its own generation is still the newest: cancelling the older
	// command stops useless work, but a result that was already computed can
	// still arrive late, and a mutex alone would let it overwrite the newer
	// snapshot (N4).
	gitGen uint64
	// reviewedAt is when the user last opened the review drawer. With the
	// last commit time it forms the reviewed watermark: changes newer than
	// both are what the rail raises.
	reviewedAt time.Time
	// review is the diff the drawer has loaded: the path it belongs to, the
	// classified lines, what the read cap left out, and why it may be empty.
	reviewPath string
	reviewDiff []workspace.DiffLine
	reviewMore int
	reviewNote string
	reviewBusy bool
	// reviewGen names the newest selection. A→B→A must not let the first A's
	// late result through a path comparison; only the selection that is
	// still the newest may publish (N4).
	reviewGen uint64
	// files is the right rail's Files tab: the listings it has read and the
	// folders the user opened.
	files *fileBrowser
	// mentionFiles are the project's files the @ picker offers, mentionErr
	// is why they could not be listed.
	mentionFiles   []chrome.MentionFile
	mentionLoading bool
	mentionErr     error
	// gen retires an older listing: the picker may be opened twice, and the
	// slower answer must not overwrite the newer one.
	gen uint64
}

// gitSnapshot is the working-tree state as the last read left it.
func (w *workspaceController) gitSnapshot() (*GitInfo, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.git, w.gitErr
}

// state is the workspace as the rail and the drawer name it.
func (w *workspaceController) state() chrome.WorkspaceState {
	git, err := w.gitSnapshot()
	return workspaceState(git, err)
}

// setGit records a finished read, including "this is not a repository": a
// panel that keeps saying "Reading workspace…" after an answer is lying.
func (w *workspaceController) setGit(info *GitInfo, err error) {
	w.mu.Lock()
	w.git = info
	w.gitErr = err
	w.mu.Unlock()
}

// beginGitRead tickets one working-tree read. Called where the read is asked
// for, so a goroutine that starts late cannot claim the newer generation (N4).
func (w *workspaceController) beginGitRead() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.gitGen++
	return w.gitGen
}

// publishGit records a read only if no newer one was started since. The check
// and the write share one critical section, which is what a lock around the
// write alone cannot provide (§4.1, N4).
func (w *workspaceController) publishGit(gen uint64, info *GitInfo, err error) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if gen != w.gitGen {
		return false
	}
	w.git = info
	w.gitErr = err
	return true
}

// beginReviewRead starts one diff selection: it becomes the path on screen
// immediately, and its generation is what a late reply must still match.
func (w *workspaceController) beginReviewRead(path string) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reviewGen++
	w.reviewPath = path
	w.reviewBusy = true
	w.reviewNote = ""
	return w.reviewGen
}

// clearReview forgets the drawer's diff and retires a read still in flight.
func (w *workspaceController) clearReview() {
	w.mu.Lock()
	w.reviewGen++
	w.reviewPath, w.reviewDiff, w.reviewMore, w.reviewNote, w.reviewBusy = "", nil, 0, "", false
	w.mu.Unlock()
}

// publishReview records a diff only for the selection that is still current.
func (w *workspaceController) publishReview(gen uint64, path string, lines []workspace.DiffLine, omitted int, err error) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if gen != w.reviewGen || w.reviewPath != path {
		return false
	}
	w.reviewBusy = false
	w.reviewDiff, w.reviewMore = lines, omitted
	switch {
	case err != nil:
		w.reviewNote = "git could not read this file's diff"
	case len(lines) == 0:
		// A rename with no content change, or a binary file: there is
		// nothing to colour, and saying so beats an empty pane.
		w.reviewNote = "No text changes to show in this file"
	default:
		w.reviewNote = ""
	}
	return true
}

// changeCount is how many paths the last read reported.
func (w *workspaceController) changeCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.git == nil {
		return 0
	}
	return len(w.git.Changes)
}

// branch is the current branch, empty when it is not a repository.
func (w *workspaceController) branch() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.git == nil {
		return ""
	}
	return w.git.Branch
}

// markReviewed records that the user has seen the current changes.
func (w *workspaceController) markReviewed(now time.Time) {
	w.mu.Lock()
	w.reviewedAt = now
	w.mu.Unlock()
}

// reviewSnapshot is what the rail needs to decide whether anything awaits
// review, without holding the lock across the computation.
func (w *workspaceController) reviewSnapshot() (*GitInfo, time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.git, w.reviewedAt
}
