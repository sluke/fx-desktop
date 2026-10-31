package chrome

import (
	"sort"
	"time"
)

// Attention is the session-rail state machine. The zero value is "idle", so a
// Summary that was never filled in reads as unremarkable rather than as an
// alarm.
type Attention uint8

const (
	AttentionIdle Attention = iota
	AttentionWorking
	AttentionNeedsApproval
	AttentionErrored
	AttentionDoneUnreviewed
	AttentionExited
)

// attentionNames maps each state to the stable identifier used in the design
// ledger. Keeping it as a table (rather than a String method with a switch)
// makes an accidental gap in the enum obvious.
var attentionNames = [...]string{
	AttentionIdle:           "idle",
	AttentionWorking:        "working",
	AttentionNeedsApproval:  "needs-approval",
	AttentionErrored:        "errored",
	AttentionDoneUnreviewed: "done-unreviewed",
	AttentionExited:         "exited",
}

// attentionRank is the priority order used to float sessions up the rail:
// needs-approval first, then errored, working, done-unreviewed, idle, exited.
// It is deliberately separate from the enum order so that reordering the
// constants cannot silently change the rail.
var attentionRank = [...]int{
	AttentionNeedsApproval:  0,
	AttentionErrored:        1,
	AttentionWorking:        2,
	AttentionDoneUnreviewed: 3,
	AttentionIdle:           4,
	AttentionExited:         5,
}

// String returns the stable identifier from the design ledger.
func (a Attention) String() string {
	if int(a) < len(attentionNames) {
		return attentionNames[a]
	}
	return attentionNames[AttentionIdle]
}

// Rank is the rail sort key: smaller floats up.
func (a Attention) Rank() int {
	if int(a) < len(attentionRank) {
		return attentionRank[a]
	}
	return attentionRank[AttentionIdle]
}

// NeedsAttention reports whether the session is blocked on the user, and so
// justifies a badge or a system notification.
func (a Attention) NeedsAttention() bool {
	return a == AttentionNeedsApproval || a == AttentionErrored
}

// SessionSummary is everything one rail row needs. It is a plain value, so the
// state machine and the sort order can be exercised without a window.
type SessionSummary struct {
	ID         string
	Workspace  string
	Title      string
	Status     Attention
	Model      string
	Branch     string
	Changes    int
	LastActive time.Time
	Unread     bool
}

// SortSessionSummaries orders rows for the rail: attention first, then the
// most recent activity, then workspace and title so equal rows stay stable.
func SortSessionSummaries(list []SessionSummary) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if ar, br := a.Status.Rank(), b.Status.Rank(); ar != br {
			return ar < br
		}
		if !a.LastActive.Equal(b.LastActive) {
			return a.LastActive.After(b.LastActive)
		}
		if a.Workspace != b.Workspace {
			return a.Workspace < b.Workspace
		}
		return a.Title < b.Title
	})
}
