package app

// L6b: parser -> app mapping -> command argv must agree. Migrated from the
// r6 app probe TestReviewR6LiveDeepRefAdapter.

import (
	"strings"
	"testing"

	"fx-desktop/internal/fxlive"
	"fx-desktop/internal/management"
)

func TestLiveDeepRefAdapterConsistency(t *testing.T) {
	raw := []byte(`{"sessions":[{"id":"root","agents":[{"name":"a","agents":[{"name":"b","agents":[{"name":"c","agents":[{"name":"d"}]}]}]}]}]}`)
	report, err := fxlive.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	rows := liveAgents(report.Agents)
	if len(rows) != 5 {
		t.Fatalf("expected five rows, got %d", len(rows))
	}
	queryable := 0
	for _, row := range rows {
		if row.CLIRef == "" || row.Ambiguous {
			// The GUI disables these; they must never reach the adapter.
			continue
		}
		queryable++
		args, _, err := management.Command("session-tail", management.Form{SessionID: row.CLIRef, Tail: "20"})
		if err != nil {
			t.Fatalf("enabled CLIRef %q rejected by read adapter: %v", row.CLIRef, err)
		}
		if len(args) < 2 || args[1] != row.CLIRef {
			t.Fatalf("argv did not carry the CLI ref: %q", args)
		}
	}
	if queryable == 0 {
		t.Fatal("the whole tree became unqueryable")
	}
	deep := rows[len(rows)-1]
	if !deep.Ambiguous || deep.CLIRef != "" {
		t.Fatalf("the over-deep row is still queryable: %+v", deep)
	}
}

func TestLiveSummaryMarksPartialList(t *testing.T) {
	got := liveSummary(fxlive.Report{Partial: true, Skipped: 3, Agents: []fxlive.Agent{{}}})
	if !strings.Contains(got, "not shown") || !strings.Contains(got, "partial") {
		t.Fatalf("partial list was presented as complete: %q", got)
	}
}
