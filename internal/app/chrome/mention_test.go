package chrome

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestMentionAtReadsTheMentionUnderTheCaret(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		caret int
		query string
		start int
		ok    bool
	}{
		{name: "just opened", text: "@", caret: 1, start: 0, ok: true},
		{name: "being typed", text: "look at @main", caret: 13, query: "main", start: 8, ok: true},
		{name: "caret inside the mention", text: "look at @main.go", caret: 9, start: 8, ok: true},
		{name: "a second mention", text: "@a.go and @b", caret: 12, query: "b", start: 10, ok: true},
		{name: "a folder is part of the query", text: "@internal/app", caret: 13, query: "internal/app", start: 0, ok: true},
		{name: "a mention on its own line", text: "read this\n@con", caret: 14, query: "con", start: 10, ok: true},
		{name: "an address is not a mention", text: "write to a@b", caret: 12},
		{name: "the word ended", text: "@main.go more", caret: 14},
		{name: "a plain draft", text: "no mention here", caret: 15},
		{name: "an empty draft", text: "", caret: 0},
		{name: "caret before the mention", text: "@main.go", caret: 0},
		{name: "the caret past the text", text: "@x", caret: 99, query: "x", start: 0, ok: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, start, _, ok := mentionAt(test.text, test.caret)
			if ok != test.ok {
				t.Fatalf("mentionAt(%q, %d) ok = %v, want %v", test.text, test.caret, ok, test.ok)
			}
			if !ok {
				return
			}
			if query != test.query || start != test.start {
				t.Fatalf("mentionAt(%q, %d) = %q at %d, want %q at %d", test.text, test.caret, query, start, test.query, test.start)
			}
		})
	}
}

func TestMentionPathsReadsWhatAMessageNames(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{text: "look at @main.go", want: []string{"main.go"}},
		{text: "@a.go and @b.go here", want: []string{"a.go", "b.go"}},
		{text: "@a.go, then @a.go again", want: []string{"a.go"}},
		{text: "read @internal/app/chrome/console.go please", want: []string{"internal/app/chrome/console.go"}},
		// The punctuation a sentence leaves on a name is not part of it.
		{text: "read @main.go, then stop", want: []string{"main.go"}},
		{text: "read @main.go.", want: []string{"main.go"}},
		{text: "read (@main.go) now", want: []string{"main.go"}},
		{text: "read [@main.go]", want: []string{"main.go"}},
		// A name with dots in it keeps them.
		{text: "read @v1.0.min.js", want: []string{"v1.0.min.js"}},
		// An @ inside a word is an address, not a mention.
		{text: "write to a@b", want: nil},
		{text: "write to me@work.example", want: nil},
		{text: "an @ of its own", want: nil},
		{text: "no mention at all", want: nil},
		{text: "", want: nil},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			got := MentionPaths(test.text)
			if len(got) != len(test.want) {
				t.Fatalf("MentionPaths(%q) = %v, want %v", test.text, got, test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("MentionPaths(%q) = %v, want %v", test.text, got, test.want)
				}
			}
		})
	}
}

// Everything the picker writes, the resolver reads: taking a row must leave a
// message that names a file. The two halves are tested together here because
// they are the two halves of one rule.
func TestMentionPickerWritesWhatTheResolverReads(t *testing.T) {
	for _, text := range []string{"@", "look at @con", "look at @internal/app", "(see @con"} {
		query, start, end, ok := mentionAt(text, len([]rune(text)))
		if !ok {
			t.Fatalf("mentionAt(%q) found no mention at the end", text)
		}
		if query != string([]rune(text)[start+1:end]) {
			t.Fatalf("mentionAt(%q) query %q does not match its range", text, query)
		}
		taken := mentionInsertion(text, start, end, "internal/app/chrome/console.go")
		paths := MentionPaths(taken)
		if len(paths) != 1 || paths[0] != "internal/app/chrome/console.go" {
			t.Fatalf("taking a row in %q left %q, which reads as %v", text, taken, paths)
		}
	}
}

func TestMentionInsertionReplacesTheMention(t *testing.T) {
	tests := []struct {
		text       string
		start, end int
		path       string
		want       string
	}{
		{text: "@co", start: 0, end: 3, path: "console.go", want: "@console.go "},
		{text: "read @co now", start: 5, end: 8, path: "console.go", want: "read @console.go  now"},
		{text: "@", start: 0, end: 1, path: "main.go", want: "@main.go "},
		{text: "look @a.go", start: 5, end: 99, path: "b.go", want: "look @b.go "},
		{text: "@x", start: 0, end: 0, path: "y.go", want: "@y.go @x"},
	}
	for _, test := range tests {
		if got := mentionInsertion(test.text, test.start, test.end, test.path); got != test.want {
			t.Errorf("mentionInsertion(%q, %d, %d, %q) = %q, want %q", test.text, test.start, test.end, test.path, got, test.want)
		}
	}
}

// The ranks are the order fx lists its own matches in: the name itself, a name
// that starts with what was typed, a path that does, then anything that
// contains it, and the scattered letters last. Each file here matches exactly
// one rank, so the order is the ranking and nothing else.
func TestRankMentionFilesRanksWhatWasTyped(t *testing.T) {
	// The paths and names are deliberately unlike real ones: each matches
	// "con" one way only.
	files := []MentionFile{
		{Path: "co/nest.go", Name: "nest.go", Dir: "co"},
		{Path: "recon/main.go", Name: "main.go", Dir: "recon"},
		{Path: "con/setup.go", Name: "setup.go", Dir: "con"},
		{Path: "a/recon.go", Name: "recon.go", Dir: "a"},
		{Path: "bin/con", Name: "con", Dir: "bin"},
		{Path: "chrome/console.go", Name: "console.go", Dir: "chrome"},
	}
	want := []string{"bin/con", "chrome/console.go", "con/setup.go", "a/recon.go", "recon/main.go", "co/nest.go"}
	got := rankMentionFiles(files, "con", mentionLimit)
	if len(got) != len(want) {
		t.Fatalf("rankMentionFiles = %+v, want %v", got, want)
	}
	for i, path := range want {
		if got[i].Path != path {
			t.Fatalf("rank %d is %q, want %q (whole list %+v)", i, got[i].Path, path, got)
		}
	}
}

func TestRankMentionFilesLeavesOutWhatDoesNotMatch(t *testing.T) {
	files := []MentionFile{
		{Path: "internal/app/chrome/console.go", Name: "console.go", Dir: "internal/app/chrome"},
		{Path: "main.go", Name: "main.go"},
		{Path: "docs/console.md", Name: "console.md", Dir: "docs"},
	}
	if got := rankMentionFiles(files, "nothing-like-this", mentionLimit); len(got) != 0 {
		t.Fatalf("a query that matches nothing found %+v", got)
	}
	// A folder is how a query narrows to files that are near each other.
	got := rankMentionFiles(files, "internal/app/chrome", mentionLimit)
	if len(got) != 1 || got[0].Path != "internal/app/chrome/console.go" {
		t.Fatalf("a folder query = %+v", got)
	}
	// The name itself is what a whole name finds, and it comes first.
	got = rankMentionFiles(files, "console.go", mentionLimit)
	if len(got) == 0 || got[0].Path != "internal/app/chrome/console.go" {
		t.Fatalf("an exact name = %+v", got)
	}
}

func TestRankMentionFilesKeepsTheListShort(t *testing.T) {
	files := make([]MentionFile, 0, 50)
	for i := 0; i < 50; i++ {
		files = append(files, MentionFile{Path: "file" + string(rune('a'+i%26)) + ".go"})
	}
	if got := rankMentionFiles(files, "", mentionLimit); len(got) != mentionLimit {
		t.Fatalf("an empty query offered %d rows, want %d", len(got), mentionLimit)
	}
	// Nothing typed keeps the app's own order: the list is stable rather than
	// reshuffled on every keystroke.
	if got := rankMentionFiles(files, "", mentionLimit); got[0].Path != files[0].Path {
		t.Fatalf("the list was reordered: %+v", got)
	}
}

// The picker is the whole path from typing @ to sending: it asks the app for
// the project's files, offers what matches, and writes the path into the
// message where the mention was typed.
func TestMentionPickerOffersFilesAndWritesThem(t *testing.T) {
	state := ConversationState{}
	var sent []string
	opened := 0
	h := ConversationHost{
		Ready:        true,
		CanMention:   true,
		MentionFiles: []MentionFile{{Path: "internal/app/chrome/console.go", Name: "console.go", Dir: "internal/app/chrome"}},
		OpenMentions: func() { opened++ },
		Send:         func(s string, _ []Attachment) bool { sent = append(sent, s); return true },
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 800)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("look at @con")
	if !state.MentionOpen {
		t.Fatal("typing @ did not open the picker")
	}
	if opened != 1 {
		t.Fatalf("the picker asked for the project's files %d times, want once per opening", opened)
	}
	if !ts.HasText("console.go") || !ts.HasText("internal/app/chrome") {
		t.Fatalf("the picker does not name the file: %v", ts.Texts())
	}
	if err := ts.Click("console.go"); err != nil {
		t.Fatal(err)
	}
	if want := "look at @internal/app/chrome/console.go "; state.Draft != want {
		t.Fatalf("draft = %q, want %q", state.Draft, want)
	}
	if state.MentionOpen {
		t.Fatal("the picker stayed open after taking a file")
	}
	// The field keeps the caret and the focus, so the message goes on.
	ts.Type("for the drawer")
	if want := "look at @internal/app/chrome/console.go for the drawer"; state.Draft != want {
		t.Fatalf("draft = %q, want %q", state.Draft, want)
	}
	// The file is named in the message, so the message is what the app is
	// asked to send: no attachment list, no hidden state.
	ts.Key(0, ui.KeyEnter)
	if len(sent) != 1 || sent[0] != "look at @internal/app/chrome/console.go for the drawer" {
		t.Fatalf("sent = %v", sent)
	}
	if state.Draft != "" {
		t.Fatalf("the draft was not cleared: %q", state.Draft)
	}
}

// Enter belongs to the picker while it is open: it takes the file, rather than
// sending a message that names one halfway.
func TestMentionPickerTakesEnterInsteadOfSending(t *testing.T) {
	state := ConversationState{}
	var sent []string
	h := ConversationHost{
		Ready:        true,
		CanMention:   true,
		MentionFiles: []MentionFile{{Path: "main.go", Name: "main.go"}, {Path: "README.md", Name: "README.md"}},
		Send:         func(s string, _ []Attachment) bool { sent = append(sent, s); return true },
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 800)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("read @")
	// The arrows move through the rows and Enter takes the highlighted one:
	// the second file is the one asked for.
	ts.Key(0, ui.KeyDown)
	ts.Key(0, ui.KeyEnter)
	if state.Draft != "read @README.md " {
		t.Fatalf("the highlighted row was not the one Enter took: %q", state.Draft)
	}
	if len(sent) != 0 {
		t.Fatalf("Enter sent the message instead of taking the file: %v", sent)
	}
	if state.MentionOpen {
		t.Fatal("the picker stayed open")
	}
	// The message goes on, and Enter now sends it.
	ts.Type("please")
	ts.Key(0, ui.KeyEnter)
	if len(sent) != 1 || sent[0] != "read @README.md please" {
		t.Fatalf("sent = %v", sent)
	}
}

func TestMentionPickerClosesWhenTheWordEnds(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{
		Ready:        true,
		CanMention:   true,
		MentionFiles: []MentionFile{{Path: "main.go", Name: "main.go"}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 800)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("@main")
	if !state.MentionOpen {
		t.Fatal("typing @ did not open the picker")
	}
	ts.Type(" ")
	if state.MentionOpen {
		t.Fatal("the picker stayed open once the mention word ended")
	}
	if state.Draft != "@main " {
		t.Fatalf("the draft lost what was typed: %q", state.Draft)
	}
	// Escape dismisses it without touching the message.
	ts.Type("@")
	if !state.MentionOpen {
		t.Fatal("typing @ again did not reopen the picker")
	}
	ts.Key(0, ui.KeyEscape)
	if state.MentionOpen {
		t.Fatal("escape did not dismiss the picker")
	}
	if state.Draft != "@main @" {
		t.Fatalf("escape changed the draft: %q", state.Draft)
	}
}

// An empty picker says which empty it is: still reading, nothing matched, or
// the project's files could not be listed at all.
func TestMentionPickerSaysWhatItIsWaitingFor(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, CanMention: true, MentionFiles: []MentionFile{{Path: "main.go", Name: "main.go"}}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 800)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("@zzz")
	if !state.MentionOpen {
		t.Fatal("typing @ did not open the picker")
	}
	if !ts.HasText("No file matches") {
		t.Fatalf("a query that matches nothing says nothing: %v", ts.Texts())
	}
	h.MentionLoading = true
	ts.Frame()
	if !ts.HasText("Reading this project…") {
		t.Fatalf("a picker still reading does not say so: %v", ts.Texts())
	}
	h.MentionLoading, h.MentionProblem = false, "This project's files could not be listed"
	ts.Frame()
	if !ts.HasText("This project's files could not be listed") {
		t.Fatalf("a picker that failed does not say why: %v", ts.Texts())
	}
}

// A mention is completed where the caret is, not where the message ends: the
// picker reads the token under the caret and replaces exactly that.
func TestMentionPickerCompletesWhereTheCaretIs(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{
		Ready:        true,
		CanMention:   true,
		MentionFiles: []MentionFile{{Path: "console.go", Name: "console.go"}, {Path: "main.go", Name: "main.go"}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 800)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("read @con and stop")
	if state.MentionOpen {
		t.Fatal("the picker opened for a word that had ended")
	}
	// Walk the caret back into "@con".
	for range " and stop" {
		ts.Key(0, ui.KeyLeft)
	}
	if !state.MentionOpen {
		t.Fatal("the picker did not open for the mention under the caret")
	}
	ts.Key(0, ui.KeyEnter)
	if want := "read @console.go  and stop"; state.Draft != want {
		t.Fatalf("draft = %q, want %q", state.Draft, want)
	}
	// The caret is left after the mention just written, so typing goes on
	// there rather than at the end of the message.
	ts.Type("I")
	if want := "read @console.go I and stop"; state.Draft != want {
		t.Fatalf("typing landed at %q, want %q", state.Draft, want)
	}
}

// Folding the project once at load time and ranking in one bounded pass must
// produce exactly the rows the on-the-fly version produced (§4.10.3).
func TestRankMentionFilesPrefoldedMatchesFoldingOnUse(t *testing.T) {
	files := []MentionFile{
		{Path: "internal/App/Main.go", Name: "Main.go", Dir: "internal/App"},
		{Path: "internal/app/main_test.go", Name: "main_test.go", Dir: "internal/app"},
		{Path: "docs/app.md", Name: "app.md", Dir: "docs"},
		{Path: "internal/app/x.go", Name: "x.go", Dir: "internal/app"},
		{Path: "README.md", Name: "README.md"},
	}
	folded := append([]MentionFile(nil), files...)
	for i := range folded {
		folded[i].LowerName = strings.ToLower(folded[i].Name)
		folded[i].LowerPath = strings.ToLower(folded[i].Path)
	}
	paths := func(rows []MentionFile) []string {
		out := make([]string, len(rows))
		for i := range rows {
			out[i] = rows[i].Path
		}
		return out
	}
	for _, q := range []string{"app", "MAIN", "internal/app", "m", "readme"} {
		got := paths(rankMentionFiles(folded, q, 8))
		want := paths(rankMentionFiles(files, q, 8))
		if !slices.Equal(got, want) {
			t.Fatalf("query %q: pre-folded rows = %v; folded on use = %v", q, got, want)
		}
	}
}

// The picker ranks again on every frame it is open. At 100k files the old
// version allocated a full-size candidate slice per frame, 5.6MB each
// (§4.10.3); the scan itself must now be a bounded pass.
func TestRankMentionFilesDoesNotAllocatePerFile(t *testing.T) {
	const n = 100_000
	files := make([]MentionFile, n)
	for i := range files {
		path := fmt.Sprintf("internal/pkg%d/file-%d.go", i%50, i)
		name := fmt.Sprintf("file-%d.go", i)
		files[i] = MentionFile{Path: path, Name: name, Dir: "internal", LowerName: name, LowerPath: path}
	}
	_ = rankMentionFiles(files, "file-99", 8)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 5; i++ {
		_ = rankMentionFiles(files, "file-99", 8)
	}
	runtime.ReadMemStats(&after)
	if used := after.TotalAlloc - before.TotalAlloc; used > 1<<20 {
		t.Fatalf("ranking 100k files allocated %d bytes over 5 passes; want at most 1 MiB", used)
	}
}
