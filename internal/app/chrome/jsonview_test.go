package chrome

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A payload laid out as JSON is a grid: every key at one depth shares a
// value column, so a reader scans values instead of hunting for them after
// keys of different lengths.
func TestFormatJSONAlignsKeysAndValues(t *testing.T) {
	lines, ok := formatJSON(`{"command":"go test ./...","cwd":"/tmp","env":{"CGO_ENABLED":"0"},"items":[1,2,3],"ok":true}`)
	if !ok {
		t.Fatal("an object was not read as JSON")
	}
	want := []struct {
		depth int
		key   string
		value string
		mark  string
	}{
		{0, "", "", "{"},
		{1, `"command"`, `"go test ./..."`, ""},
		{1, `"cwd"`, `"/tmp"`, ""},
		{1, `"env"`, "", "{"},
		{2, `"CGO_ENABLED"`, `"0"`, ""},
		{1, "", "", "}"},
		{1, `"items"`, "[1, 2, 3]", ""},
		{1, `"ok"`, "true", ""},
		{0, "", "", "}"},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(lines), len(want), lines)
	}
	for i, w := range want {
		if lines[i].depth != w.depth || lines[i].key != w.key || lines[i].value != w.value || lines[i].mark != w.mark {
			t.Fatalf("line %d is %+v, want %+v", i, lines[i], w)
		}
	}
	// Every keyed line at one depth starts its value at the same column;
	// that column is what makes the block a grid.
	cols := map[int]int{}
	for _, l := range lines {
		if l.key == "" || l.value == "" {
			continue
		}
		if col, seen := cols[l.depth]; seen && col != l.col {
			t.Fatalf("line %+v starts at column %d, its depth starts at %d", l, l.col, col)
		}
		cols[l.depth] = l.col
	}
}

// A running call sends half an object. It still lays out, and it says it
// continues instead of leaving an unclosed brace that reads as a bug.
func TestFormatJSONReadsAStreamingPayload(t *testing.T) {
	lines, ok := formatJSON(`{"description":"Audit the composer","prompt":"Read the tool row`)
	if !ok {
		t.Fatal("a partial object was not read as JSON")
	}
	if len(lines) < 2 || lines[1].key != `"description"` {
		t.Fatalf("the first key was lost: %+v", lines)
	}
	last := lines[len(lines)-1]
	if last.mark != "…" {
		t.Fatalf("an unfinished payload ends with %+v, want the continuation mark", last)
	}
	if last.depth != 1 {
		t.Fatalf("the continuation mark sits at depth %d, want 1", last.depth)
	}
}

// Tool output is often text that merely starts with a bracket. Only data is
// laid out as data; everything else stays verbatim.
func TestFormatJSONLeavesPlainTextAlone(t *testing.T) {
	for _, text := range []string{
		`[ -f main.go ] && go build ./...`,
		`{not json}`,
		"Reading main.go\nok",
		"",
	} {
		if _, ok := formatJSON(text); ok {
			t.Fatalf("%q was laid out as JSON", text)
		}
	}
	if _, ok := formatJSON(`[1, 2, 3]`); !ok {
		t.Fatal("an array of scalars was not read as JSON")
	}
}

// The expanded payload of a JSON tool call is the same framed block as any
// other tool output, but its text is indented and its values are a column.
func TestExpandedJSONPayloadIsLaidOutAsData(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{
		Ready: true,
		Messages: []ChatMessage{{
			ID:     3,
			Role:   "tool",
			Title:  "Bash",
			Status: "completed",
			Done:   true,
			Text:   `{"command":"go test ./...","cwd":"/tmp"}`,
		}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	ts.Frame()
	if err := ts.Click("Bash — completed"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if ts.HasText(`{"command":"go test ./..."`) {
		t.Fatal("the payload is still one compact line")
	}
	block := jsonTextOf(t, ts)
	if !strings.HasPrefix(block, "{\n  \"command\": ") {
		t.Fatalf("the payload is not indented: %q", block)
	}
	if !strings.HasSuffix(block, "\n}") {
		t.Fatalf("the payload lost its closing brace: %q", block)
	}
	// The values share a column because the keys were padded to it.
	keyed := strings.Split(block, "\n")
	if len(keyed) < 3 {
		t.Fatalf("the block has %d lines", len(keyed))
	}
	valueCol := func(line string) int {
		col := strings.Index(line, ":")
		for col+1 < len(line) && line[col+1] == ' ' {
			col++
		}
		return col + 1
	}
	if got, want := valueCol(keyed[2]), valueCol(keyed[1]); got != want {
		t.Fatalf("values start at columns %d and %d:\n%s", got, want, block)
	}
}

// jsonTextOf returns the laid-out payload: the one block in the transcript
// that carries a brace.
func jsonTextOf(t *testing.T, ts *ui.Tester) string {
	t.Helper()
	for _, text := range ts.Texts() {
		if strings.HasPrefix(text, "{") {
			return text
		}
	}
	t.Fatalf("no JSON block in %v", ts.Texts())
	return ""
}

// A long JSON payload is capped the way verbatim output is, and says how
// many lines it left out.
func TestJSONPayloadReportsWhatItLeftOut(t *testing.T) {
	items := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		items = append(items, `{"path":"main.go","line":1}`)
	}
	text := "[" + strings.Join(items, ",") + "]"
	lines, ok := formatJSON(text)
	if !ok {
		t.Fatal("the array was not read as JSON")
	}
	shown, omitted := capJSONLines(lines, 120)
	if omitted != len(lines)-120 || len(shown) != 120 {
		t.Fatalf("kept %d of %d lines, reported %d omitted", len(shown), len(lines), omitted)
	}
}

// The cap is wired into the block itself: an opened array of 300 objects says
// at its foot how much of it is not on screen.
func TestLongJSONPayloadIsCappedInTheBlock(t *testing.T) {
	items := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		items = append(items, `{"path":"main.go","line":1}`)
	}
	text := "[" + strings.Join(items, ",") + "]"
	lines, _ := formatJSON(text)
	omitted := itoa(len(lines) - maxToolOutputLines)
	state := ConversationState{}
	h := ConversationHost{
		Ready:    true,
		Messages: []ChatMessage{{ID: 3, Role: "tool", Title: "Search", Status: "completed", Done: true, Text: text}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	ts.Frame()
	if err := ts.Click("Search — completed"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if !ts.HasText("… " + omitted + " more lines") {
		t.Fatalf("the capped payload does not say it left %s lines out: %v", omitted, ts.Texts())
	}
}

// The layout is cached per text, so a frame does not re-parse a payload, and
// a payload that grows while the call runs is laid out again.
func TestPayloadLayoutFollowsTheText(t *testing.T) {
	state := ConversationState{}
	m := ChatMessage{ID: 7, Text: `{"a":1}`}
	lines, ok := payloadLines(&state, m)
	if !ok || len(lines) != 3 {
		t.Fatalf("the first payload became %d lines (%v)", len(lines), ok)
	}
	m.Text = `{"a":1,"b":2}`
	lines, ok = payloadLines(&state, m)
	if !ok || len(lines) != 4 {
		t.Fatalf("the grown payload became %d lines (%v), want the new keys", len(lines), ok)
	}
	if lines[2].key != `"b"` {
		t.Fatalf("the grown payload kept %+v, want the new key", lines[2])
	}
}
