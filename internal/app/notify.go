package app

import (
	"strings"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
)

// notificationPrefix namespaces notification IDs so a click can be traced
// back to a rail row, including on a later run: macOS delivers the click of
// the notification that launched the app, when no Notification of this run
// exists to hold it (§5).
const notificationPrefix = "fx-desktop:"

// notificationRailID is the rail row a notification belongs to.
func notificationRailID(id string) (string, bool) {
	if !strings.HasPrefix(id, notificationPrefix) {
		return "", false
	}
	rail := strings.TrimPrefix(id, notificationPrefix)
	if rail == "" {
		return "", false
	}
	return rail, true
}

// attentionDelta compares the sessions that needed the user against the last
// render: which ones just started needing attention, and the complete set for
// the badge. It is a pure function so the transition rules can be tested
// without a window, a Dock, or a notification.
func attentionDelta(previous map[string]bool, sums []chrome.SessionSummary) (fresh []chrome.SessionSummary, needing map[string]bool) {
	needing = make(map[string]bool, len(sums))
	for _, sum := range sums {
		if !sum.Status.NeedsAttention() {
			continue
		}
		needing[sum.ID] = true
		if !previous[sum.ID] {
			fresh = append(fresh, sum)
		}
	}
	return fresh, needing
}

// notifyAttention raises the Dock badge and, the first time a session starts
// needing the user, posts a system notification that raises that session. It
// is driven by the render pass that already computes the rail, so nothing
// polls for attention.
//
// It runs on the app loop (see Session.view), which is why attention and
// badge need no lock: this is their only writer.
func (c *Coordinator) notifyAttention(sums []chrome.SessionSummary) {
	fresh, needing := attentionDelta(c.attention, sums)
	c.attention = needing
	if n := len(needing); n != c.badge {
		c.badge = n
		mygo.App.SetBadgeCount(n)
	}
	for _, sum := range fresh {
		// A window the user is already looking at does not need to be
		// announced: the rail is right there.
		if s := c.sessionByRailID(sum.ID); s != nil {
			if w := s.winSnapshot(); w != nil && w.IsFocused() {
				continue
			}
		}
		postAttentionNotification(sum)
	}
}

// postAttentionNotification tells the user a session is waiting. Showing
// waits for macOS to answer the permission prompt the first time, so it must
// not run on the app loop.
func postAttentionNotification(sum chrome.SessionSummary) {
	body := "needs your approval"
	if sum.Status == chrome.AttentionErrored {
		body = "hit an error"
	}
	go func() {
		// A stable ID per session both groups repeats into one notification
		// and tells the click handler which row to raise.
		n := mygo.NewNotification(mygo.NotificationOptions{
			ID:    notificationPrefix + sum.ID,
			Title: "fx · " + sum.Title,
			Body:  body,
			Group: "fx-desktop",
		})
		_ = n.Show()
	}()
}

// sessionByRailID finds the live session a rail row stands for.
func (c *Coordinator) sessionByRailID(id string) *ProjectWindow {
	for _, s := range c.sessionsSnapshot() {
		if s.railID() == id {
			return s
		}
	}
	return nil
}
