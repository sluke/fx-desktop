// Package fxlive decodes `fx sessions --live --json`. It does not start,
// stop, or delete a session. A missing field stays missing; a count of zero
// is only used when fx sent that count.
package fxlive

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"fx-desktop/internal/fxsessions"
)

const (
	// maxLiveNodes and maxLiveDepth bound one document. A response that is
	// only 20 KiB can still describe thousands of agents, so the input byte
	// cap alone is not a work bound (L6).
	maxLiveNodes = 10000
	maxLiveDepth = 4
	// maxUIKeyBytes keeps one display key bounded even when fx sends a very
	// long name. The tail is kept, cut on a rune boundary.
	maxUIKeyBytes = 256
)

// Agent is one live conversation or a subagent under it.
//
// CLIRef and UIKey are deliberately different things (L5). CLIRef is the
// address fx understands: a verified session id, optionally followed by
// verified agent path segments. It is never rewritten to make a row unique,
// because a made-up path is not something fx can read. UIKey is a stable
// identity for selection and layout; it may carry a display suffix and is
// never passed to fx. A row whose CLIRef cannot address exactly one agent is
// Ambiguous, and the UI disables querying it rather than sending a guess.
type Agent struct {
	ID, Name, Title, State, Model, Profile, Workspace, Updated string
	CLIRef                                                    string
	UIKey                                                     string
	Ambiguous                                                 bool
	Note                                                      string
	Agents                                                    []Agent

	// path is the display path used to derive UIKey. It is kept even when
	// the same spelling is not a valid CLI address. refProblem carries the
	// reason a row cannot be queried, for the UI to show.
	path       string
	refProblem string
}

// Report is the live list. Count is nil when fx omitted it or sent null;
// those are not zero. Skipped counts entries fx sent that could not be read,
// so a partial list is never presented as the whole one. Ambiguous counts
// rows that cannot be addressed individually through the CLI.
type Report struct {
	Count     *int
	Agents    []Agent
	Skipped   int
	Ambiguous int
	// Partial is true when the node/depth budget dropped rows. Skipped says
	// how many; the list must not be presented as everything fx reported.
	Partial bool
}

// Parse decodes a live-sessions document. Sessions and nested agents keep
// the order fx sent.
func Parse(raw []byte) (Report, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Report{}, fmt.Errorf("fx returned unreadable live sessions")
	}
	if kind := jsonString(doc, "kind"); kind != "" && kind != "live_sessions" {
		return Report{}, fmt.Errorf("fx returned %s, not live sessions", kind)
	}
	sessions, ok := doc["sessions"]
	if !ok {
		return Report{}, fmt.Errorf("fx returned live sessions without a session list")
	}
	// encoding/json writes a nil slice as null, so null is an empty list from
	// a real fx run, not a missing field.
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(sessions, &rows); err != nil {
		return Report{}, fmt.Errorf("fx returned unreadable live sessions")
	}
	var report Report
	if rawCount, ok := doc["count"]; ok && string(rawCount) != "null" {
		var n int
		if err := json.Unmarshal(rawCount, &n); err != nil {
			return Report{}, fmt.Errorf("fx returned an unreadable live session count")
		}
		if n < 0 {
			return Report{}, fmt.Errorf("fx returned a negative live session count")
		}
		report.Count = &n
	}
	budget := parseBudget{}
	for _, row := range rows {
		agent, ok := agentFrom(row, "", 0, &budget)
		if !ok {
			continue
		}
		report.Agents = append(report.Agents, agent)
	}
	report.Skipped = budget.skipped
	report.Partial = budget.partial
	report.Ambiguous = assignKeys(report.Agents)
	return report, nil
}

// parseBudget carries the per-document work limits and the reason rows were
// dropped. Skipped is surfaced in the UI; Partial says the list is a prefix.
type parseBudget struct {
	nodes   int
	skipped int
	partial bool
}

// assignKeys gives every row a unique UIKey and marks rows whose CLIRef
// cannot address exactly one agent. All base keys are reserved before any
// suffix is handed out: a real agent called "reviewer#2" must not collide
// with a generated #2 suffix, and a generated suffix must not be reused.
func assignKeys(rows []Agent) int {
	reserved := map[string]bool{}
	refCount := map[string]int{}
	var collect func([]Agent)
	collect = func(list []Agent) {
		for _, agent := range list {
			reserved[uiKeyBase(agent)] = true
			if agent.CLIRef != "" {
				refCount[agent.CLIRef]++
			}
			collect(agent.Agents)
		}
	}
	collect(rows)

	used := map[string]bool{}
	// nextSuffix remembers where each base stopped looking. Starting at #2
	// again for every duplicate made N same-named rows cost O(N^2) candidate
	// strings and allocations (L6a).
	nextSuffix := map[string]int{}
	ambiguous := 0
	var assign func([]Agent)
	assign = func(list []Agent) {
		for i := range list {
			agent := &list[i]
			base := uiKeyBase(*agent)
			key := base
			if used[key] {
				n := nextSuffix[base]
				if n < 2 {
					n = 2
				}
				for {
					candidate := base + "#" + strconv.Itoa(n)
					n++
					if !reserved[candidate] && !used[candidate] {
						key = candidate
						break
					}
				}
				nextSuffix[base] = n
			}
			used[key] = true
			agent.UIKey = key
			switch {
			case agent.refProblem != "":
				agent.Ambiguous = true
				agent.Note = agent.refProblem
			case agent.CLIRef == "":
				agent.Ambiguous = true
				agent.Note = "no verified session id"
			case refCount[agent.CLIRef] > 1:
				agent.Ambiguous = true
				agent.Note = "several agents share this session path"
			}
			if agent.Ambiguous {
				ambiguous++
			}
			assign(agent.Agents)
		}
	}
	assign(rows)
	return ambiguous
}

// uiKeyBase prefers a real id when fx sent one: an id stays the same when
// rows are reordered, while a display path built from a repeated name does
// not. Keys are bounded to a rune-aligned tail; assignKeys still guarantees
// uniqueness, so truncation cannot create a collision.
func uiKeyBase(agent Agent) string {
	switch {
	case agent.ID != "":
		return boundedKey(agent.ID)
	case agent.path != "":
		return boundedKey(agent.path)
	case agent.CLIRef != "":
		return boundedKey(agent.CLIRef)
	default:
		return "agent"
	}
}

func boundedKey(s string) string {
	if len(s) <= maxUIKeyBytes {
		return s
	}
	cut := len(s) - maxUIKeyBytes
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	if cut >= len(s) {
		return s[len(s)-1:]
	}
	return s[cut:]
}

// cliRef returns the address fx can read and, when there is none, the reason
// the UI should show. The shape is exactly the one the command adapter
// accepts: an address deeper than the adapter's ceiling is unqueryable, not
// an invitation to loosen the adapter without real CLI evidence (L6b).
func cliRef(path string) (string, string) {
	if path == "" {
		return "", "no verified session id"
	}
	if fxsessions.ValidRef(path) {
		return path, ""
	}
	if len(strings.Split(path, "/")) > fxsessions.MaxRefSegments {
		return "", "session path is deeper than the CLI read adapter accepts"
	}
	return "", "session path has characters the CLI cannot address"
}

func agentFrom(row map[string]json.RawMessage, parent string, depth int, b *parseBudget) (Agent, bool) {
	if depth > maxLiveDepth || b.nodes >= maxLiveNodes {
		b.skipped++
		b.partial = true
		return Agent{}, false
	}
	b.nodes++
	agent := Agent{
		ID:        jsonString(row, "id", "session_id"),
		Name:      jsonString(row, "name"),
		Title:     jsonString(row, "title"),
		State:     jsonString(row, "state", "status"),
		Model:     jsonString(row, "model"),
		Profile:   jsonString(row, "profile", "agent_profile"),
		Workspace: jsonString(row, "workspace", "workspace_root"),
		Updated:   jsonString(row, "updated", "updated_at", "updated_at_ms"),
	}
	path := agent.ID
	if parent != "" {
		name := agent.Name
		if name == "" {
			name = agent.ID
		}
		if name != "" {
			path = parent + "/" + name
		} else {
			path = parent
		}
	}
	agent.path = path
	agent.CLIRef, agent.refProblem = cliRef(path)
	var children []Agent
	for _, key := range []string{"agents", "subagents", "children"} {
		raw, ok := row[key]
		if !ok || string(raw) == "null" {
			continue
		}
		var nested []map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) != nil {
			b.skipped++
			break
		}
		base := path
		if base == "" {
			base = parent
		}
		for _, child := range nested {
			next, ok := agentFrom(child, base, depth+1, b)
			if !ok {
				continue
			}
			children = append(children, next)
		}
		break
	}
	agent.Agents = children
	if agent.ID == "" && agent.Name == "" && agent.Title == "" && len(agent.Agents) == 0 {
		b.skipped++
		return Agent{}, false
	}
	return agent, true
}

func jsonString(row map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := row[key]
		if !ok || string(raw) == "null" {
			continue
		}
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return strings.TrimSpace(text)
		}
		var n json.Number
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if dec.Decode(&n) == nil {
			return n.String()
		}
		var i int64
		if json.Unmarshal(raw, &i) == nil {
			return strconv.FormatInt(i, 10)
		}
	}
	return ""
}
