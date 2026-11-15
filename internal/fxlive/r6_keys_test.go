package fxlive

// L6: the parser's unique keys must be near-linear, and every row it offers
// for querying must satisfy the very same address rule as the command
// adapter. TestLiveDeepRefIsUnqueryable was migrated from the r6 app probe
// TestReviewR6LiveDeepRefAdapter.

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"fx-desktop/internal/fxsessions"
	"fx-desktop/internal/management"
)

func TestLiveDeepRefIsUnqueryable(t *testing.T) {
	raw := []byte(`{"sessions":[{"id":"root","agents":[{"name":"a","agents":[{"name":"b","agents":[{"name":"c","agents":[{"name":"d"}]}]}]}]}]}`)
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	deep := r.Agents[0].Agents[0].Agents[0].Agents[0].Agents[0]
	if deep.CLIRef != "" || !deep.Ambiguous {
		t.Fatalf("a 5-segment address was offered as queryable: %+v", deep)
	}
	if !strings.Contains(deep.Note, "deeper") {
		t.Fatalf("the row does not explain why it is disabled: %q", deep.Note)
	}
	if !r.Partial && r.Skipped != 0 {
		t.Fatalf("depth accounting drifted: %+v", r)
	}
}

// For every UI-selectable row, parser -> mapping -> command argv must hold.
// The argv carries the CLI ref, never a display key or a made-up suffix.
func TestLiveEveryQueryableRefIsAcceptedByAdapter(t *testing.T) {
	raw := []byte(`{"sessions":[
		{"id":"abc","agents":[{"name":"reviewer"},{"name":"reviewer"},{"name":"bad#name"}]},
		{"id":"def","agents":[{"name":"child","agents":[{"name":"leaf"}]}]},
		{"title":"unnamed","state":"working"}
	]}`)
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	queryable := 0
	var walk func([]Agent)
	walk = func(rows []Agent) {
		for _, agent := range rows {
			if !agent.Ambiguous && agent.CLIRef != "" {
				queryable++
				args, _, err := management.Command("session-tail", management.Form{SessionID: agent.CLIRef, Tail: "20"})
				if err != nil {
					t.Fatalf("queryable ref %q rejected by adapter: %v", agent.CLIRef, err)
				}
				if len(args) < 2 || args[1] != agent.CLIRef {
					t.Fatalf("argv used something other than the CLI ref: %q", args)
				}
				if strings.Contains(agent.CLIRef, "#") {
					t.Fatalf("a display suffix leaked into the CLI ref: %q", agent.CLIRef)
				}
			}
			walk(agent.Agents)
		}
	}
	walk(r.Agents)
	if queryable == 0 {
		t.Fatal("no queryable rows were produced")
	}
}

// The r6 red benchmark input: 1500 same-named rows in only ~20 KiB. Keys
// must stay unique without quadratic candidate generation.
func TestLiveDuplicateKeysAreUniqueAtScale(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"sessions":[{"id":"root","agents":[`)
	for i := 0; i < 1500; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"name":"a"}`)
	}
	b.WriteString(`]}]}`)
	r, err := Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, agent := range r.Agents[0].Agents {
		if agent.UIKey == "" || seen[agent.UIKey] {
			t.Fatalf("duplicate or empty UI key: %q", agent.UIKey)
		}
		seen[agent.UIKey] = true
		if strings.Contains(agent.CLIRef, "#") || !fxsessions.ValidRef(agent.CLIRef) {
			t.Fatalf("CLI ref is not a plain verified address: %q", agent.CLIRef)
		}
	}
}

func TestLiveInterleavedBasesAndSuffixNames(t *testing.T) {
	raw := []byte(`{"sessions":[{"id":"root","agents":[
		{"name":"a"},{"name":"b"},{"name":"a"},{"name":"a#2"},{"name":"b"},{"name":"a"},{"name":"a#3"}
	]}]}`)
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, agent := range r.Agents[0].Agents {
		if seen[agent.UIKey] {
			t.Fatalf("interleaved bases produced a collision: %q", agent.UIKey)
		}
		seen[agent.UIKey] = true
	}
}

func TestLiveNestedSubtreeKeysAreDistinct(t *testing.T) {
	raw := []byte(`{"sessions":[{"id":"s","agents":[
		{"name":"x","agents":[{"name":"x"},{"name":"x"}]},
		{"name":"x","agents":[{"name":"x"}]}
	]}]}`)
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var walk func([]Agent)
	walk = func(rows []Agent) {
		for _, agent := range rows {
			if seen[agent.UIKey] {
				t.Fatalf("nested key collision: %q", agent.UIKey)
			}
			seen[agent.UIKey] = true
			walk(agent.Agents)
		}
	}
	walk(r.Agents)
}

// A real id is a stable identity; two parses with rows reordered must keep
// the same key for the same id (a repeated name alone cannot promise that).
func TestLiveReorderedRealIDsKeepUIKey(t *testing.T) {
	first, err := Parse([]byte(`{"sessions":[{"id":"s","agents":[{"id":"id1","name":"a"},{"id":"id2","name":"b"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Parse([]byte(`{"sessions":[{"id":"s","agents":[{"id":"id2","name":"b"},{"id":"id1","name":"a"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	byID := func(r Report) map[string]string {
		out := map[string]string{}
		for _, agent := range r.Agents[0].Agents {
			out[agent.ID] = agent.UIKey
		}
		return out
	}
	a, b := byID(first), byID(second)
	if len(a) != 2 {
		t.Fatalf("expected two identified rows, got %d", len(a))
	}
	for id, key := range a {
		if b[id] != key {
			t.Fatalf("id %q changed key under reorder: %q -> %q", id, key, b[id])
		}
	}
}

func TestLiveNodeBudgetIsPartial(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"sessions":[{"id":"root","agents":[`)
	for i := 0; i < maxLiveNodes+10; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":"id%d","name":"a%d"}`, i, i)
	}
	b.WriteString(`]}]}`)
	r, err := Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Partial || r.Skipped == 0 {
		t.Fatalf("node budget did not report a partial list: partial=%v skipped=%d", r.Partial, r.Skipped)
	}
	if len(r.Agents[0].Agents) > maxLiveNodes {
		t.Fatalf("node budget was exceeded: %d rows", len(r.Agents[0].Agents))
	}
}

func TestLiveKeyLengthIsBounded(t *testing.T) {
	raw := []byte(`{"sessions":[{"id":"` + strings.Repeat("长", 400) + `","name":"x"}]}`)
	r, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	key := r.Agents[0].UIKey
	if len(key) > maxUIKeyBytes || !utf8.ValidString(key) {
		t.Fatalf("unbounded or invalid UI key: len=%d valid=%v", len(key), utf8.ValidString(key))
	}
}
