package chrome

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A shell call's expanded payload is the call, not fx's result envelope: the
// command is the one part a reader can use, and the envelope's handles and
// capture modes never enter the transcript.
func TestExpandedShellCallShowsTheCommand(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{
		Ready: true,
		Messages: []ChatMessage{{
			ID:      3,
			Role:    "tool",
			Title:   "Running",
			Status:  "completed",
			Command: "ls -la /Users/luweiqing/Documents/VC",
			Shell:   &ShellResult{},
			Done:    true,
		}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	ts.Frame()
	if err := ts.Click("Running — completed"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if !ts.HasText("$ ls -la /Users/luweiqing/Documents/VC") {
		t.Fatalf("the command is missing from the expanded call: %v", ts.Texts())
	}
	for _, text := range ts.Texts() {
		if strings.Contains(text, "full_output_handle") || strings.Contains(text, "output_terminal_safe") {
			t.Fatalf("fx's envelope is still in the transcript: %q", text)
		}
	}
}

// A result that lost something says so in the block's foot — the one place a
// shell call should raise its voice.
func TestShellResultWarningsSitInTheFooter(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{
		Ready: true,
		Messages: []ChatMessage{{
			ID:      3,
			Role:    "tool",
			Title:   "Running",
			Status:  "completed",
			Command: "make build",
			Shell:   &ShellResult{Truncated: true},
			Done:    true,
		}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	ts.Frame()
	if err := ts.Click("Running — completed"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if !ts.HasText("output truncated") {
		t.Fatalf("a truncated result is silent: %v", ts.Texts())
	}
}

// The footer's vocabulary is the result's meaning, in order, and a clean call
// has nothing to say.
func TestShellNotesOnlySpeakWhenSomethingChanged(t *testing.T) {
	p := palette(ui.LightTheme())
	if notes := shellNotes(&ShellResult{}, p); len(notes) != 0 {
		t.Fatalf("a clean result raised %d notes", len(notes))
	}
	notes := shellNotes(&ShellResult{Truncated: true, Incomplete: true, Indeterminate: true, Signal: "SIGKILL"}, p)
	want := []string{"output truncated", "output incomplete", "termination unknown", "signal SIGKILL"}
	if len(notes) != len(want) {
		t.Fatalf("got %d notes, want %d: %+v", len(notes), len(want), notes)
	}
	for i, w := range want {
		if notes[i].text != w {
			t.Fatalf("note %d is %q, want %q", i, notes[i].text, w)
		}
	}
}
