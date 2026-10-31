package chrome

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// A tool row that reads "Running" next to a word like "Completed" tells the
// reader nothing. The row carries the state as a dot and the scale as a
// duration, the way the design's audit log does.
func TestToolRowShowsStateAndDuration(t *testing.T) {
	state := ConversationState{}
	now := time.Now()
	h := ConversationHost{
		Ready: true,
		Messages: []ChatMessage{
			{ID: 3, Role: "tool", Title: "Running", Status: "completed", Text: "ok", Done: true,
				Started: now.Add(-1200 * time.Millisecond), Ended: now},
			{ID: 4, Role: "tool", Title: "Reading", Status: "failed", Text: "boom", Done: true,
				Started: now.Add(-500 * time.Millisecond), Ended: now},
			{ID: 5, Role: "tool", Title: "Running", Status: "in_progress", Text: "", Done: false,
				Started: now.Add(-900 * time.Millisecond)},
		},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 700)
	ts.Frame()
	if !ts.HasText("1.2s") {
		t.Fatalf("the finished call does not say how long it took: %v", ts.Texts())
	}
	if ts.HasText("Completed") {
		t.Fatalf("the row still carries the word that contradicts its title: %v", ts.Texts())
	}
	if !ts.HasText("failed") {
		t.Fatalf("a failed call does not say so: %v", ts.Texts())
	}
	if !ts.HasText("working") {
		t.Fatalf("a call in flight does not say so: %v", ts.Texts())
	}
}

// The duration reads as a measurement: milliseconds for the quick calls,
// seconds for the slow ones.
func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                       "0ms",
		20 * time.Millisecond:   "20ms",
		340 * time.Millisecond:  "340ms",
		1200 * time.Millisecond: "1.2s",
		90 * time.Second:        "1m30s",
	}
	for d, want := range cases {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
