package presenter

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/fxcli"
)

// The details page is fx's own JSON rendered as rows: every field the
// inspection answered shows, and a blank one shows its fallback rather than
// an empty cell.
func TestDetailsRendersStatusPermissionsAndUsage(t *testing.T) {
	fetched := int64(1700000000000)
	status := &fxcli.Status{
		Model:              "test/model",
		Effort:             "",
		Mode:               "ask",
		Auth:               "configured",
		ConnectedProviders: []string{"test", "other"},
		MCP: fxcli.MCP{
			Servers:             []json.RawMessage{json.RawMessage(`{"name":"srv"}`)},
			ConfigurationIssues: []string{"bad entry"},
			InspectionError:     json.RawMessage(`{"message":"offline"}`),
		},
	}
	permissions := &fxcli.Permissions{
		Mode: "ask", GrantCount: 2, RulesScope: "user",
		Rules:  []json.RawMessage{json.RawMessage(`{"tool":"shell"}`)},
		Grants: []json.RawMessage{json.RawMessage(`{"tool":"read"}`)},
	}
	usage := &fxcli.Usage{
		Period: "month", Status: "", Completeness: "partial", FetchedAtMS: &fetched,
	}
	usage.Totals.TotalTokens = 12345
	usage.Totals.RequestCount = 7
	usage.Totals.Spend = 1.5
	sections := Details(status, permissions, usage, []string{"doctor: unavailable"})

	titles := make([]string, 0, len(sections))
	for _, section := range sections {
		titles = append(titles, section.Title)
	}
	joined := strings.Join(titles, "|")
	for _, want := range []string{"Status", "MCP issues", "Permissions", "Runtime grants", "Usage · month", "Unavailable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("sections %q missing %q", joined, want)
		}
	}
	statusFacts := factsOf(sections, "Status")
	if statusFacts["Effort"] != "auto" || statusFacts["Providers"] != "test, other" || statusFacts["Ultrafast"] != "off" {
		t.Fatalf("status facts = %v", statusFacts)
	}
	usageFacts := factsOf(sections, "Usage · month")
	if usageFacts["Total tokens"] != "12.3k" || usageFacts["Status"] != "partial" || usageFacts["Spend"] != "$1.50" {
		t.Fatalf("usage facts = %v", usageFacts)
	}
	if usageFacts["Fetched"] == "" {
		t.Fatal("the fetched timestamp was not rendered")
	}
	// The grant row says what the GUI cannot do, instead of offering it.
	for _, section := range sections {
		if section.Title == "Runtime grants" && !strings.Contains(section.Text, "interactive shell") {
			t.Fatalf("grants section = %q", section.Text)
		}
	}
}

// factsOf flattens one section's rows so a test can name a field.
func factsOf(sections []chrome.DetailSection, title string) map[string]string {
	out := map[string]string{}
	for _, section := range sections {
		if section.Title != title {
			continue
		}
		for _, fact := range section.Facts {
			out[fact.Label] = fact.Value
		}
	}
	return out
}

// A saved conversation is trimmed for one frame: long turns are cut with the
// marker, and an absent detail is nil rather than an empty page.
func TestSessionDetailTrimsLongTurns(t *testing.T) {
	long := strings.Repeat("x", 2500)
	detail := &fxcli.SessionDetail{
		ID:                   "thread-1",
		CreatedAtMS:          1700000000000,
		UpdatedAtMS:          1700000100000,
		HistoryLen:           2,
		ConversationLanguage: "zh-Hans",
		History: []fxcli.SessionTurn{
			{Kind: "user", User: struct {
				Text   string            `json:"text"`
				Images []json.RawMessage `json:"images"`
			}{Text: "hi", Images: []json.RawMessage{json.RawMessage(`{}`)}}},
			{Kind: "assistant", Assistant: long, CompletedToolNames: []string{"read"}},
		},
	}
	view := SessionDetail(detail)
	if view == nil || view.ID != "thread-1" || view.Language != "zh-Hans" || view.HistoryLen != 2 {
		t.Fatalf("view = %+v", view)
	}
	if view.Created == "" || view.Updated == "" {
		t.Fatalf("timestamps not rendered: %+v", view)
	}
	if len(view.Turns) != 2 || view.Turns[0].Images != 1 || view.Turns[0].User != "hi" {
		t.Fatalf("turns = %+v", view.Turns)
	}
	if !strings.HasPrefix(view.Turns[1].Assistant, strings.Repeat("x", 2000)) ||
		!strings.Contains(view.Turns[1].Assistant, "truncated") || len(view.Turns[1].Tools) != 1 {
		t.Fatalf("long turn not trimmed: %q", view.Turns[1].Assistant)
	}
	if SessionDetail(nil) != nil {
		t.Fatal("a nil detail produced a view")
	}
}

// A count and a timestamp are display strings with their own boundaries.
func TestCountsAndTimes(t *testing.T) {
	for _, test := range []struct {
		in   int64
		want string
	}{{-1, "0"}, {0, "0"}, {999, "999"}, {1500, "1.5k"}, {2_400_000, "2.4M"}} {
		if got := CompactCount(test.in); got != test.want {
			t.Errorf("CompactCount(%d) = %q, want %q", test.in, got, test.want)
		}
	}
	now := time.UnixMilli(1_700_000_000_000)
	for _, test := range []struct {
		ms   int64
		want string
	}{{0, ""}, {now.UnixMilli() - 30_000, "just now"}, {now.UnixMilli() - 5*60_000, "5m ago"}, {now.UnixMilli() - 3*3600_000, "3h ago"}} {
		if got := RelTime(test.ms, now); got != test.want {
			t.Errorf("RelTime(%d) = %q, want %q", test.ms, got, test.want)
		}
	}
	if got := Elapsed(90 * time.Second); got != "01:30" {
		t.Fatalf("Elapsed = %q", got)
	}
	if got := Elapsed(3725 * time.Second); got != "1:02:05" {
		t.Fatalf("Elapsed = %q", got)
	}
	if got := Elapsed(-time.Second); got != "00:00" {
		t.Fatalf("negative elapsed = %q", got)
	}
}

// Free-form JSON fields are shown as compact text, and null is not a value.
func TestFreeFormValues(t *testing.T) {
	if got := APIText(json.RawMessage(`{ "a" : 1 }`)); got != `{"a":1}` {
		t.Fatalf("APIText = %q", got)
	}
	if got := APIText(nil); got != "" {
		t.Fatalf("APIText(nil) = %q", got)
	}
	if RawPresent(json.RawMessage(`null`)) || RawPresent(nil) || !RawPresent(json.RawMessage(`{}`)) {
		t.Fatal("RawPresent misreports a null field")
	}
	if got := ValueOr("  ", "fallback"); got != "fallback" {
		t.Fatalf("ValueOr = %q", got)
	}
}
