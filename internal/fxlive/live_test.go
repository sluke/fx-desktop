package fxlive

import "testing"

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
	if report.Agents[0].Ref != "abc" || report.Agents[0].Agents[0].Ref != "abc/reviewer" || report.Agents[0].Agents[0].Agents[0].Ref != "abc/reviewer/helper" {
		t.Fatalf("refs=%s %s %s", report.Agents[0].Ref, report.Agents[0].Agents[0].Ref, report.Agents[0].Agents[0].Agents[0].Ref)
	}
	if report.Agents[0].Agents[0].Agents[0].State != "" {
		t.Fatalf("null state became %q", report.Agents[0].Agents[0].Agents[0].State)
	}
	if report.Agents[1].Agents[0].Ref != "def/reviewer" {
		t.Fatalf("second reviewer ref=%s", report.Agents[1].Agents[0].Ref)
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
