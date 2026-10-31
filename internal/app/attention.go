package app

import (
	"path/filepath"
	"time"

	"fx-desktop/internal/app/chrome"
)

// attentionInput is the already-snapshotted state the rail state machine
// needs. Keeping it a plain value — rather than a *Session or a chatSnapshot —
// is what makes the derivation testable without an fx process.
type attentionInput struct {
	chat     bool // the session is a native ACP conversation
	ready    bool // the conversation finished connecting
	busy     bool // a turn is in flight
	approval bool // a chat approval is pending, or the terminal wants input
	problem  bool // the last turn or the connection failed
	exited   bool // a command session's process ended
	changes  int  // uncommitted files in the workspace
	reviewed bool // the user has acknowledged the current changes
}

// deriveAttention maps raw session state to the rail state machine.
// Order matters: a pending approval
// outranks a failure, which outranks work in progress. A conversation that is
// still connecting reuses the working state, so the rail shows one "busy"
// affordance instead of inventing a seventh state.
func deriveAttention(in attentionInput) chrome.Attention {
	switch {
	case in.approval:
		return chrome.AttentionNeedsApproval
	case in.problem:
		return chrome.AttentionErrored
	case in.busy || (in.chat && !in.ready):
		return chrome.AttentionWorking
	case in.changes > 0 && !in.reviewed:
		return chrome.AttentionDoneUnreviewed
	case in.exited:
		return chrome.AttentionExited
	default:
		return chrome.AttentionIdle
	}
}

// railID is the stable identity of a session on the rail: the workspace for a
// project session, a synthetic id for a chrome-less command session.
func (s *Session) railID() string {
	if s.project != "" {
		return s.project
	}
	return "cmd:" + s.title
}

// summary projects the session into the plain value the rail consumes. It is
// the first consumer of the attention state machine (§5).
//
// LastActive is the session start for now. Real activity time needs a
// timestamp the chat snapshot does not carry yet; the sort stays deterministic
// in the meantime because workspace and title break the remaining ties.
func (s *Session) summary() chrome.SessionSummary {
	var (
		branch     string
		changes    int
		exited     bool
		approval   bool
		git        *GitInfo
		reviewedAt time.Time
	)
	s.mu.Lock()
	if s.git != nil {
		branch = s.git.Branch
		changes = len(s.git.Changes)
	}
	git = s.git
	exited = s.exited
	approval = s.attention
	reviewedAt = s.reviewedAt
	s.mu.Unlock()
	// A change is "reviewed" when it is older than both the last commit and
	// the last time the drawer was opened (§5, §12.0). Without that, every
	// session with an old uncommitted edit would claim to await review.
	reviewed := !git.unreviewed(reviewedAt)

	in := attentionInput{
		chat:     s.chat != nil,
		exited:   exited,
		approval: approval,
		changes:  changes,
		reviewed: reviewed,
	}
	model := s.model
	// fxTitle is the conversation name fx generates after the first turn.
	// Until a turn exists the name fx reports is a placeholder, so the rail
	// shows where the session lives instead: a directory name beats a
	// placeholder, but a real name beats both.
	fxTitle := ""
	if s.chat != nil {
		st := s.chat.status()
		in.ready = st.Ready
		in.busy = st.Busy
		in.problem = st.Problem != ""
		in.approval = in.approval || st.Permission
		if st.Model != "" {
			model = st.Model
		}
		if st.SessionTitle != "" && st.MessageCount > 0 {
			fxTitle = st.SessionTitle
		}
	}

	id := s.railID()
	title := s.title
	if title == "" {
		title = fxTitle
	}
	if title == "" {
		title = filepath.Base(s.project)
	}
	return chrome.SessionSummary{
		ID:         id,
		Workspace:  s.project,
		Title:      title,
		Status:     deriveAttention(in),
		Model:      model,
		Branch:     branch,
		Changes:    changes,
		LastActive: s.started,
	}
}

// sessionSummaries is the coordinator's session registry (the existing
// c.sessions list) projected into rail rows and sorted by attention.
func (c *Coordinator) sessionSummaries() []chrome.SessionSummary {
	sessions := c.sessionsSnapshot()
	out := make([]chrome.SessionSummary, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.summary())
	}
	chrome.SortSessionSummaries(out)
	return out
}
