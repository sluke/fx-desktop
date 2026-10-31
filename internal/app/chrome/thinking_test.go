package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The thinking row stands in for the reply that is coming, so it belongs in
// the reading column: on the same left edge as the transcript, carrying the
// same author mark. It used to be laid out as a plain row, which stranded
// it against the panel's edge while everything else sat in the column.
func TestThinkingRowSitsInTheReadingColumn(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{
		Ready: true,
		Busy:  true,
		Messages: []ChatMessage{
			{ID: 1, Role: "user", Text: "你会怎么设计技能?", Done: true},
		},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 620)
	ts.Frame()
	thinking, ok := ts.Find("Thinking…")
	if !ok {
		t.Fatal("the thinking row is missing")
	}
	// The reading column is 740 DIPs wide and centered in the 1000 DIP
	// panel, so it starts at 130. The panel's own edge is 0.
	if thinking.X < 120 {
		t.Fatalf("the thinking row starts at %.1f, outside the reading column", thinking.X)
	}
}
