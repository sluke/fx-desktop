package chrome

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestAttentionNamesAreStable(t *testing.T) {
	cases := map[Attention]string{
		AttentionIdle:           "idle",
		AttentionWorking:        "working",
		AttentionNeedsApproval:  "needs-approval",
		AttentionErrored:        "errored",
		AttentionDoneUnreviewed: "done-unreviewed",
		AttentionExited:         "exited",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("Attention(%d).String() = %q, want %q", state, got, want)
		}
	}
}

func TestAttentionRankOrdersNeedsApprovalFirst(t *testing.T) {
	order := []Attention{
		AttentionNeedsApproval,
		AttentionErrored,
		AttentionWorking,
		AttentionDoneUnreviewed,
		AttentionIdle,
		AttentionExited,
	}
	for i := 1; i < len(order); i++ {
		if order[i-1].Rank() >= order[i].Rank() {
			t.Fatalf("%s should out-rank %s", order[i-1], order[i])
		}
	}
}

func TestAttentionNeedsAttention(t *testing.T) {
	for _, a := range []Attention{AttentionIdle, AttentionWorking, AttentionDoneUnreviewed, AttentionExited} {
		if a.NeedsAttention() {
			t.Errorf("%s should not need attention", a)
		}
	}
	for _, a := range []Attention{AttentionNeedsApproval, AttentionErrored} {
		if !a.NeedsAttention() {
			t.Errorf("%s should need attention", a)
		}
	}
}

func TestSortSessionSummariesFloatsAttentionUp(t *testing.T) {
	now := time.Now()
	list := []SessionSummary{
		{ID: "idle", Workspace: "/b", Title: "idle", Status: AttentionIdle, LastActive: now},
		{ID: "approve", Workspace: "/a", Title: "approve", Status: AttentionNeedsApproval, LastActive: now.Add(-time.Hour)},
		{ID: "working", Workspace: "/a", Title: "working", Status: AttentionWorking, LastActive: now},
		{ID: "errored", Workspace: "/a", Title: "errored", Status: AttentionErrored, LastActive: now.Add(-time.Minute)},
	}
	SortSessionSummaries(list)
	got := []string{list[0].ID, list[1].ID, list[2].ID, list[3].ID}
	want := []string{"approve", "errored", "working", "idle"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestSortSessionSummariesTieBreaksOnRecencyThenWorkspace(t *testing.T) {
	now := time.Now()
	list := []SessionSummary{
		{ID: "old", Workspace: "/a", Title: "t", Status: AttentionIdle, LastActive: now.Add(-time.Hour)},
		{ID: "new", Workspace: "/a", Title: "t", Status: AttentionIdle, LastActive: now},
		{ID: "b", Workspace: "/b", Title: "t", Status: AttentionIdle, LastActive: now},
	}
	SortSessionSummaries(list)
	got := []string{list[0].ID, list[1].ID, list[2].ID}
	want := []string{"new", "b", "old"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// Every rail state has a tone of its own: a row that is waiting for the user
// must not look like one that merely changed, or one that failed. This is
// what the Warn token is for (§4.8).
func TestAttentionColorsAreDistinct(t *testing.T) {
	for _, theme := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		p := palette(theme)
		seen := map[ui.Color]Attention{}
		for _, a := range []Attention{AttentionNeedsApproval, AttentionErrored, AttentionDoneUnreviewed, AttentionWorking} {
			color := attentionColor(p, a)
			if prev, dup := seen[color]; dup {
				t.Errorf("dark=%v %s and %s share a colour", theme.Dark, prev, a)
			}
			seen[color] = a
		}
		if got := attentionColor(p, AttentionNeedsApproval); got != p.Warn {
			t.Errorf("dark=%v needs-approval must use the attention tone", theme.Dark)
		}
	}
}
