package fxlive

import (
	"os"
	"strconv"
	"testing"
)

func TestParseLiveTreeKeepsDistinctAgents(t *testing.T) {
	raw := []byte(`{"kind":"live_sessions","count":2,"sessions":[
		{"id":"abc","title":"Fix","state":"working","model":"opus","agents":[
			{"name":"reviewer","state":"idle","profile":"reviewer","agents":[{"name":"helper","state":null}]}
		]},
		{"session_id":"def","title":"Other","state":"idle","agents":[{"name":"reviewer","state":"working"}]}
	]}`)
	report, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if report.Count == nil || *report.Count != 2 || len(report.Agents) != 2 {
		t.Fatalf("report=%+v", report)
	}
	if report.Agents[0].CLIRef != "abc" || report.Agents[0].Agents[0].CLIRef != "abc/reviewer" || report.Agents[0].Agents[0].Agents[0].CLIRef != "abc/reviewer/helper" {
		t.Fatalf("refs=%s %s %s", report.Agents[0].CLIRef, report.Agents[0].Agents[0].CLIRef, report.Agents[0].Agents[0].Agents[0].CLIRef)
	}
	if report.Agents[0].Agents[0].Agents[0].State != "" {
		t.Fatalf("null state became %q", report.Agents[0].Agents[0].Agents[0].State)
	}
	if report.Agents[1].Agents[0].CLIRef != "def/reviewer" {
		t.Fatalf("second reviewer ref=%s", report.Agents[1].Agents[0].CLIRef)
	}
}

func TestParseLiveRejectsTheWrongDocument(t *testing.T) {
	if _, err := Parse([]byte(`{"kind":"sessions","count":0,"sessions":[]}`)); err == nil {
		t.Fatal("saved-session list was accepted as live")
	}
	report, err := Parse([]byte(`{"kind":"live_sessions","sessions":[]}`))
	if err != nil || report.Count != nil || len(report.Agents) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

// A count fx did not send stays unknown. Null and a missing key are the same
// fact; zero is a count fx actually sent, and invalid values are protocol
// errors rather than a silent zero.
func TestParseLiveCountKeepsUnknownDistinct(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		want    *int
		wantErr bool
	}{
		{name: "missing", doc: `{"kind":"live_sessions","sessions":[]}`},
		{name: "null", doc: `{"kind":"live_sessions","count":null,"sessions":[]}`},
		{name: "zero", doc: `{"kind":"live_sessions","count":0,"sessions":[]}`, want: intPtr(0)},
		{name: "positive", doc: `{"kind":"live_sessions","count":3,"sessions":[]}`, want: intPtr(3)},
		{name: "negative", doc: `{"kind":"live_sessions","count":-1,"sessions":[]}`, wantErr: true},
		{name: "string", doc: `{"kind":"live_sessions","count":"3","sessions":[]}`, wantErr: true},
		{name: "fraction", doc: `{"kind":"live_sessions","count":1.5,"sessions":[]}`, wantErr: true},
		{name: "wrong type", doc: `{"kind":"live_sessions","count":{"n":3},"sessions":[]}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := Parse([]byte(tt.doc))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("accepted %s, report=%+v", tt.name, report)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case tt.want == nil && report.Count != nil:
				t.Fatalf("count became %d", *report.Count)
			case tt.want != nil && (report.Count == nil || *report.Count != *tt.want):
				t.Fatalf("count = %v, want %d", report.Count, *tt.want)
			}
		})
	}
}

func intPtr(n int) *int { return &n }

// encoding/json writes a nil slice as null, so an empty live list may arrive
// as sessions:null. That is an empty list, not a malformed document.
func TestParseLiveTreatsNullSessionsAsEmpty(t *testing.T) {
	report, err := Parse([]byte(`{"kind":"live_sessions","count":0,"sessions":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Agents) != 0 || report.Count == nil || *report.Count != 0 || report.Skipped != 0 {
		t.Fatalf("report=%+v", report)
	}
}

// A real (synthetic, non-empty) tree with unknown fields, repeated sibling
// names, and a subagent child keeps fx's CLI refs and hierarchy. Repeated
// names get distinct UI keys, never made-up CLI paths (L5).
func TestParseLiveFixtureKeepsTreeAndUniqueRefs(t *testing.T) {
	raw, err := os.ReadFile("testdata/live-tree.json")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != 0 {
		t.Fatalf("readable fixture skipped %d entries", report.Skipped)
	}
	if report.Count == nil || *report.Count != 2 || len(report.Agents) != 2 {
		t.Fatalf("report=%+v", report)
	}
	first := report.Agents[0]
	if first.CLIRef != "01J9ZQ7K3M4N5P6Q7R8S9T0V1W" || first.Workspace != "/Users/dev/project" || first.Updated == "" {
		t.Fatalf("first agent=%+v", first)
	}
	if len(first.Agents) != 2 {
		t.Fatalf("first agent children=%+v", first.Agents)
	}
	a, b := first.Agents[0], first.Agents[1]
	if a.CLIRef != first.CLIRef+"/reviewer" || b.CLIRef != a.CLIRef {
		t.Fatalf("display deduplication rewrote a CLI path: %q %q", a.CLIRef, b.CLIRef)
	}
	if a.UIKey == b.UIKey || !a.Ambiguous || !b.Ambiguous || report.Ambiguous < 2 {
		t.Fatalf("repeated names need distinct UI keys and an ambiguity note: a=%+v b=%+v report=%d", a, b, report.Ambiguous)
	}
	if len(a.Agents) != 1 || a.Agents[0].CLIRef != a.CLIRef+"/helper" || a.Agents[0].State != "working" {
		t.Fatalf("grandchild=%+v", a.Agents)
	}
	if b.Agents != nil {
		t.Fatalf("childless agent gained children: %+v", b.Agents)
	}
	second := report.Agents[1]
	if second.CLIRef != "01J9ZQ8B4C5D6E7F8G9H0J1K2M" || second.State != "idle" {
		t.Fatalf("second agent=%+v", second)
	}
}

// Entries fx sent that cannot be read are counted, not silently dropped: the
// list is partial and the caller can say so.
func TestParseLiveCountsSkippedEntries(t *testing.T) {
	t.Run("empty row", func(t *testing.T) {
		report, err := Parse([]byte(`{"sessions":[{},{"id":"a"}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if report.Skipped != 1 || len(report.Agents) != 1 {
			t.Fatalf("report=%+v", report)
		}
	})
	t.Run("unreadable child list", func(t *testing.T) {
		report, err := Parse([]byte(`{"sessions":[{"id":"a","agents":5}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if report.Skipped != 1 || len(report.Agents) != 1 || report.Agents[0].CLIRef != "a" {
			t.Fatalf("report=%+v", report)
		}
	})
	t.Run("beyond supported depth", func(t *testing.T) {
		// Five levels are read (depth 0..4); the sixth is reported.
		leaf := `{"name":"a5"}`
		for i := 4; i >= 0; i-- {
			leaf = `{"name":"a` + strconv.Itoa(i) + `","agents":[` + leaf + `]}`
		}
		report, err := Parse([]byte(`{"sessions":[` + leaf + `]}`))
		if err != nil {
			t.Fatal(err)
		}
		if report.Skipped != 1 {
			t.Fatalf("skipped=%d, want the unreadable leaf", report.Skipped)
		}
		depth := 0
		for cur := report.Agents; len(cur) == 1; cur = cur[0].Agents {
			depth++
			if depth > 10 {
				t.Fatal("depth walk did not terminate")
			}
		}
		if depth != 5 {
			t.Fatalf("read %d levels, want 5", depth)
		}
	})
	t.Run("null child list is empty", func(t *testing.T) {
		report, err := Parse([]byte(`{"sessions":[{"id":"a","agents":null,"subagents":[]}]}`))
		if err != nil {
			t.Fatal(err)
		}
		if report.Skipped != 0 || len(report.Agents) != 1 {
			t.Fatalf("report=%+v", report)
		}
	})
}
