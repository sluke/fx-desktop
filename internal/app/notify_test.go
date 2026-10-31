package app

import (
	"testing"

	"fx-desktop/internal/app/chrome"
)

func summary(id string, status chrome.Attention) chrome.SessionSummary {
	return chrome.SessionSummary{ID: id, Title: id, Status: status}
}

// The badge counts sessions blocked on the user, and only those: a working
// session or one waiting on review is not something to interrupt anyone for.
func TestAttentionDeltaCountsOnlySessionsNeedingTheUser(t *testing.T) {
	sums := []chrome.SessionSummary{
		summary("a", chrome.AttentionNeedsApproval),
		summary("b", chrome.AttentionWorking),
		summary("c", chrome.AttentionErrored),
		summary("d", chrome.AttentionDoneUnreviewed),
	}
	fresh, needing := attentionDelta(nil, sums)
	if len(needing) != 2 || !needing["a"] || !needing["c"] {
		t.Fatalf("badge set: %v", needing)
	}
	if len(fresh) != 2 || fresh[0].ID != "a" || fresh[1].ID != "c" {
		t.Fatalf("both states must be announced, in rail order: %v", fresh)
	}
}

// A session is announced once per transition, not once per frame: the rail
// re-renders constantly, and a notification should not.
func TestAttentionDeltaAnnouncesOnlyOnTransition(t *testing.T) {
	previous := map[string]bool{"a": true}
	fresh, needing := attentionDelta(previous, []chrome.SessionSummary{summary("a", chrome.AttentionNeedsApproval)})
	if len(fresh) != 0 {
		t.Fatalf("a session already needing attention was announced again: %v", fresh)
	}
	if !needing["a"] {
		t.Fatal("it must stay in the badge set")
	}
	// Answered: out of the badge set, so a later approval announces again.
	fresh, needing = attentionDelta(needing, []chrome.SessionSummary{summary("a", chrome.AttentionWorking)})
	if len(fresh) != 0 || len(needing) != 0 {
		t.Fatalf("a resolved session must clear: fresh=%v needing=%v", fresh, needing)
	}
	fresh, needing = attentionDelta(needing, []chrome.SessionSummary{summary("a", chrome.AttentionNeedsApproval)})
	if len(fresh) != 1 || len(needing) != 1 {
		t.Fatalf("a new approval must announce again: fresh=%v needing=%v", fresh, needing)
	}
}

// A notification's ID has to survive the round trip, including the click
// that launches the app: that one arrives with no Notification in memory to
// interpret it.
func TestNotificationIDRoundTrips(t *testing.T) {
	id := notificationPrefix + "/p/proj"
	rail, ok := notificationRailID(id)
	if !ok || rail != "/p/proj" {
		t.Fatalf("round trip: %q %v", rail, ok)
	}
	for _, bad := range []string{"", "some-other-app", notificationPrefix} {
		if _, ok := notificationRailID(bad); ok {
			t.Fatalf("%q must not be read as a rail id", bad)
		}
	}
}
