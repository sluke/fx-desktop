package app

import (
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
)

func kinds(lines []chrome.DiffLine) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.Kind
	}
	return out
}

func TestLineDiffClassifiesLines(t *testing.T) {
	got := lineDiff("a\nb\nc\n", "a\nB\nc\n")
	want := []chrome.DiffLine{
		{Kind: chrome.DiffContext, Text: "a"},
		{Kind: chrome.DiffDel, Text: "b"},
		{Kind: chrome.DiffAdd, Text: "B"},
		{Kind: chrome.DiffContext, Text: "c"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A trailing newline is not a line: "a\n" and "a" must compare equal, or
// every single-line edit would show a spurious removed blank line.
func TestLineDiffIgnoresTrailingNewline(t *testing.T) {
	got := lineDiff("a\n", "a")
	if len(got) != 1 || got[0].Kind != chrome.DiffContext {
		t.Fatalf("a trailing newline should not produce a change: %+v", got)
	}
}

func TestLineDiffNewFileIsAllAdds(t *testing.T) {
	got := lineDiff("", "one\ntwo\n")
	for _, l := range got {
		if l.Kind != chrome.DiffAdd {
			t.Fatalf("a new file should only add lines: %+v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 added lines, got %+v", got)
	}
}

func TestLineDiffDeletedFileIsAllRemovals(t *testing.T) {
	got := lineDiff("one\ntwo\n", "")
	for _, l := range got {
		if l.Kind != chrome.DiffDel {
			t.Fatalf("a deleted file should only remove lines: %+v", got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("want 2 removed lines, got %+v", got)
	}
}

func TestLineDiffIdenticalTextIsAllContext(t *testing.T) {
	got := lineDiff("same\nlines", "same\nlines")
	for _, l := range got {
		if l.Kind != chrome.DiffContext {
			t.Fatalf("identical text should be all context: %+v", got)
		}
	}
}

// A file too large to compare must degrade loudly rather than lie: it
// reports the new text as added and never claims a context line it did not
// actually match.
func TestLineDiffDegradesOnHugeInput(t *testing.T) {
	big := strings.Repeat("line\n", maxDiffLines+10)
	got := lineDiff(big, big+"extra\n")
	if len(got) == 0 {
		t.Fatal("degraded diff should still show the new text")
	}
	for _, l := range got {
		if l.Kind != chrome.DiffAdd {
			t.Fatalf("a degraded diff must not claim context: %+v", l)
		}
	}
}

func TestToolContentKeepsDiffStructured(t *testing.T) {
	raw := []byte(`[
		{"type":"content","content":{"type":"text","text":"Applied 1 edit"}},
		{"type":"diff","path":"a.go","oldText":"one\ntwo\n","newText":"one\nTWO\n"}
	]`)
	text, diff := toolContent(raw)
	if text != "Applied 1 edit" {
		t.Fatalf("text = %q", text)
	}
	if diff == nil {
		t.Fatal("a diff block must not be flattened into text")
	}
	if diff.Path != "a.go" {
		t.Fatalf("path = %q", diff.Path)
	}
	if got := kinds(diff.Lines); len(got) != 3 || got[1] != chrome.DiffDel || got[2] != chrome.DiffAdd {
		t.Fatalf("diff lines = %v (%+v)", got, diff.Lines)
	}
	if strings.Contains(text, "TWO") {
		t.Fatalf("the diff leaked back into the collapsed text: %q", text)
	}
}
