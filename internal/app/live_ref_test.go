package app

// Live-selection chain tests. TestDuplicateLiveRefCanBeRead was migrated from
// the r5 review probe TestReviewR5DuplicateLiveRefCanBeRead.

import (
	"testing"

	"fx-desktop/internal/fxlive"
	"fx-desktop/internal/management"
)

// A row the GUI offers for selection must name a real CLI address. Display
// deduplication may never rewrite a verified ID into a path fx rejects.
func TestDuplicateLiveRefCanBeRead(t *testing.T) {
	report, err := fxlive.Parse([]byte(`{"sessions":[{"id":"session1","agents":[{"name":"reviewer"},{"name":"reviewer"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := liveAgents(report.Agents)
	if len(rows) != 3 {
		t.Fatalf("expected the session and both subagents, got %d rows", len(rows))
	}
	duplicate := rows[2]
	if duplicate.CLIRef != "session1/reviewer" {
		t.Fatalf("duplicate row has a rewritten CLI ref: %q", duplicate.CLIRef)
	}
	if _, _, err := management.Command("session-tail", management.Form{SessionID: duplicate.CLIRef, Tail: "20"}); err != nil {
		t.Fatalf("GUI selectable ref %q rejected by command adapter: %v", duplicate.CLIRef, err)
	}
	if duplicate.UIKey == rows[1].UIKey || duplicate.UIKey == "" {
		t.Fatalf("duplicate rows share a UI key: %q", duplicate.UIKey)
	}
	if !duplicate.Ambiguous || !rows[1].Ambiguous || report.Ambiguous < 2 {
		t.Fatalf("duplicate rows were not marked ambiguous: %+v %+v", rows[1], duplicate)
	}
	if rows[0].Ambiguous || rows[0].CLIRef != "session1" {
		t.Fatalf("unambiguous session was disabled: %+v", rows[0])
	}
}

// The full chain parser -> mapping -> command argv must hold for the clean
// tree too, and a UIKey is never sent to fx as a session id.
func TestLiveSelectionChainUsesOnlyCLIRefs(t *testing.T) {
	report, err := fxlive.Parse([]byte(`{"sessions":[{"id":"abc","agents":[{"name":"reviewer"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range liveAgents(report.Agents) {
		if row.CLIRef == "" || row.UIKey == "" {
			t.Fatalf("row lost an identity: %+v", row)
		}
		if _, _, err := management.Command("session-tail", management.Form{SessionID: row.CLIRef, Tail: "20"}); err != nil {
			t.Fatalf("CLI ref %q rejected: %v", row.CLIRef, err)
		}
	}
}
