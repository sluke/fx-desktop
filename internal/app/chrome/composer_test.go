package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The stop square and the send arrow sit side by side in the composer. One
// of them drawn as a text glyph would sit on the baseline and the pair
// would not line up.
func TestComposerStopAndSendButtonsLineUp(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Busy: true}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 620)
	ts.Frame()
	stop, ok := ts.Find("Stop response")
	if !ok {
		t.Fatal("the stop button is missing")
	}
	send, ok := ts.Find("Send message")
	if !ok {
		t.Fatal("the send button is missing")
	}
	if stop.W != send.W || stop.H != send.H {
		t.Fatalf("the two buttons differ in size: %+v vs %+v", stop, send)
	}
	if stop.Y != send.Y {
		t.Fatalf("the two buttons do not line up: %.2f vs %.2f", stop.Y, send.Y)
	}
}
