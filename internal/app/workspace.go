package app

import (
	"context"
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
	// fileRead is the gate every directory read passes through before it
	// touches the disk. It is a per-window value fixed before the first read
	// starts rather than a process-wide variable, so a read that is already
	// running can never observe a different function than the one it started
	// with (R4). A nil value means "observe the context".
	fileRead func(context.Context) error
	// fileReads counts directory reads that have been started but not yet
	// returned. Tests wait on it to prove a window quiesced instead of racing
	// their own cleanup against a scheduled goroutine (R4).
	fileReads sync.WaitGroup
	// fileGate is the window's directory-read limit. It outlives a refresh,
	// which only bumps fileReadGen and cancels the previous context.
	fileGate    chan struct{}
	fileReadGen uint64
	fileCtx     context.Context
	fileCancel  context.CancelFunc
	// mentionFiles are the project's files the @ picker offers, mentionErr
	// is why they could not be listed.
	mentionFiles   []chrome.MentionFile
	mentionEpoch   uint64
	mentionLoading bool
	mentionErr     error
	// gen retires an older listing: the picker may be opened twice, and the
	// slower answer must not overwrite the newer one.
	gen uint64
}

func (w *workspaceController) fileReadPermit() (context.Context, uint64, chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fileGate == nil {
		w.fileGate = make(chan struct{}, maxFileReads)
	}
	if w.fileCtx == nil {
		w.fileCtx, w.fileCancel = context.WithCancel(context.Background())
	}
	return w.fileCtx, w.fileReadGen, w.fileGate
}

// fileReadGate returns the gate a read must pass before touching the disk. The
// caller captures it before starting the goroutine, so the read uses the
// function that was fixed when it was scheduled.
func (w *workspaceController) fileReadGate() func(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fileRead != nil {
		return w.fileRead
	}
	return func(ctx context.Context) error { return ctx.Err() }
}

// waitFileReads blocks until every directory read the window has started has
// returned. It exists for tests that inject a gate; the UI thread never waits
// on the disk.
func (w *workspaceController) waitFileReads() {
	w.fileReads.Wait()
}

func (w *workspaceController) retireFileReads() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fileReadGen++
	if w.fileCancel != nil {
		w.fileCancel()
	}
	w.fileCtx, w.fileCancel = context.WithCancel(context.Background())
	if w.fileGate == nil {
		w.fileGate = make(chan struct{}, maxFileReads)
	}
}

func (w *workspaceController) cancelFileReads() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fileReadGen++
	if w.fileCancel != nil {
		w.fileCancel()
	}
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
func (w *workspaceController) publishReview(gen uint64, path string, lines []workspace.DiffLine, omitted int, truncated bool, err error) bool {
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
	case truncated:
		w.reviewNote = "Diff output was too large; showing the first portion only"
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
