package fxlive

// L5 ref tests. TestRefSuffixCollision was migrated from the r5 review probe
// TestReviewR5RefSuffixCollision.

import (
	"testing"

	"fx-desktop/internal/management"
)

// Names that already look like a deduplication suffix must not make the
// deduplicator produce a key it had already handed out.
func TestRefSuffixCollision(t *testing.T) {
	r, err := Parse([]byte(`{"sessions":[{"id":"root","agents":[{"name":"reviewer"},{"name":"reviewer"},{"name":"reviewer#2"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, agent := range r.Agents[0].Agents {
		if seen[agent.UIKey] || agent.UIKey == "" {
			t.Fatalf("deduplication produced a colliding or empty UI key: %q", agent.UIKey)
		}
		seen[agent.UIKey] = true
	}
	// "reviewer#2" is not an id fx accepts, so it gets no CLI ref at all;
	// it is a display-only row that must not be queried.
	third := r.Agents[0].Agents[2]
	if third.CLIRef != "" || !third.Ambiguous {
		t.Fatalf("an unverified name gained a CLI address: %+v", third)
	}
}

// Every row's CLI ref stays a path the read adapter accepts; the UI key may
// differ but is never sent to fx.
func TestLiveRefsAreReadableAfterDedup(t *testing.T) {
	r, err := Parse([]byte(`{"sessions":[{"id":"session1","agents":[{"name":"reviewer"},{"name":"reviewer"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range r.Agents[0].Agents {
		if agent.CLIRef != "session1/reviewer" {
			t.Fatalf("dedup rewrote a CLI ref: %q", agent.CLIRef)
		}
		if _, _, err := management.Command("session-tail", management.Form{SessionID: agent.CLIRef, Tail: "20"}); err != nil {
			t.Fatalf("CLI ref %q is not readable: %v", agent.CLIRef, err)
		}
		if !agent.Ambiguous {
			t.Fatal("a duplicated CLI address was not marked ambiguous")
		}
	}
}

// Same name under different real IDs is not ambiguous, and UI keys stay
// stable across two parses of the same tree.
func TestLiveUIKeysAreStableAndDistinctPerSession(t *testing.T) {
	doc := []byte(`{"sessions":[{"id":"aaa","agents":[{"name":"reviewer"},{"name":"reviewer"}]},{"id":"bbb","agents":[{"name":"reviewer"}]}]}`)
	first, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if first.Ambiguous != 2 || second.Ambiguous != 2 {
		t.Fatalf("ambiguity count drifted: %d %d", first.Ambiguous, second.Ambiguous)
	}
	if first.Agents[0].CLIRef == first.Agents[1].CLIRef || first.Agents[1].Ambiguous {
		t.Fatal("different session ids were treated as one address")
	}
	var walk func([]Agent, []Agent)
	walk = func(a, b []Agent) {
		if len(a) != len(b) {
			t.Fatalf("tree shape drifted: %d vs %d", len(a), len(b))
		}
		for i := range a {
			if a[i].UIKey != b[i].UIKey || a[i].CLIRef != b[i].CLIRef {
				t.Fatalf("row %d identity drifted: %+v vs %+v", i, a[i], b[i])
			}
			walk(a[i].Agents, b[i].Agents)
		}
	}
	walk(first.Agents, second.Agents)
}

// A row without an id has no verified CLI address and must be disabled.
func TestLiveMissingIDIsAmbiguous(t *testing.T) {
	r, err := Parse([]byte(`{"sessions":[{"title":"unnamed","state":"working"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Agents) != 1 || r.Agents[0].CLIRef != "" || !r.Agents[0].Ambiguous || r.Ambiguous != 1 {
		t.Fatalf("missing id became queryable: %+v", r.Agents)
	}
}
