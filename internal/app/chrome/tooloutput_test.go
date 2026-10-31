package chrome

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The raw payload behind a tool call can be a whole session's JSON. The
// transcript keeps the part a person can scan and says how much it left
// out, the way the diff block does.
func TestToolPayloadCapsAndReportsOmittedLines(t *testing.T) {
	text := strings.Repeat("payload line\n", 300)
	got, omitted := toolPayload(text, 120)
	if omitted != 180 {
		t.Fatalf("reported %d omitted lines, want 180", omitted)
	}
	if lines := strings.Count(got, "\n"); lines != 119 {
		t.Fatalf("kept %d lines, want 120", lines+1)
	}
	short, omitted := toolPayload("one line", 120)
	if omitted != 0 || short != "one line" {
		t.Fatalf("a short payload was cut: %q, %d", short, omitted)
	}
}

// An expanded tool call is one component: the payload sits in a framed
// block under its own bar, and the bar's chevron says which way it is
// folded.
func TestExpandedToolOutputIsFramedAndCapped(t *testing.T) {
	state := ConversationState{}
	lines := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		lines = append(lines, "payload line")
	}
	h := ConversationHost{
		Ready: true,
		Messages: []ChatMessage{{
			ID:     3,
			Role:   "tool",
			Title:  "Running",
			Status: "completed",
			Text:   strings.Join(lines, "\n"),
			Done:   true,
		}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 900)
	ts.Frame()
	if ts.HasText("… 180 more lines") {
		t.Fatal("the payload was capped before the tool call was opened")
	}
	if err := ts.Click("Running — completed"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if !ts.HasText("… 180 more lines") {
		t.Fatalf("the expanded payload does not say how much it left out: %v", ts.Texts())
	}
	// The payload is a block of its own, framed and exactly as wide as the
	// bar that opens it — not loose text spilled across the page.
	bar, ok := ts.Find("Running — completed")
	if !ok {
		t.Fatal("the tool bar is missing")
	}
	block, ok := ts.Find("… 180 more lines")
	if !ok {
		t.Fatal("the cap marker is missing")
	}
	if diff := block.X - bar.X; diff > 1 || diff < -1 {
		t.Fatalf("the payload block starts at %.1f, want the bar's %.1f", block.X, bar.X)
	}
	if diff := block.W - bar.W; diff > 2 || diff < -2 {
		t.Fatalf("the payload block is %.1f wide, want the bar's %.1f", block.W, bar.W)
	}
}
