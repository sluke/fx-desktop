// Package fxlive decodes `fx sessions --live --json`. It does not start,
// stop, or delete a session. A missing field stays missing; a count of zero
// is only used when fx sent that count.
package fxlive

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Agent is one live conversation or a subagent under it.
type Agent struct {
	ID, Name, Title, State, Model, Profile, Workspace, Updated, Ref string
	Agents                                                          []Agent
}

// Report is the live list. Count is nil when fx omitted it.
type Report struct {
	Count  *int
	Agents []Agent
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
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(sessions, &rows); err != nil {
		return Report{}, fmt.Errorf("fx returned unreadable live sessions")
	}
	var report Report
	if rawCount, ok := doc["count"]; ok {
		var n int
		if err := json.Unmarshal(rawCount, &n); err != nil {
			return Report{}, fmt.Errorf("fx returned an unreadable live session count")
		}
		report.Count = &n
	}
	for _, row := range rows {
		agent, ok := agentFrom(row, "", 0)
		if ok {
			report.Agents = append(report.Agents, agent)
		}
	}
	return report, nil
}

func agentFrom(row map[string]json.RawMessage, parent string, depth int) (Agent, bool) {
	if depth > 4 {
		return Agent{}, false
	}
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
	ref := agent.ID
	if parent != "" {
		name := agent.Name
		if name == "" {
			name = agent.ID
		}
		if name != "" {
			ref = parent + "/" + name
		}
	}
	agent.Ref = ref
	var children []Agent
	for _, key := range []string{"agents", "subagents", "children"} {
		raw, ok := row[key]
		if !ok || string(raw) == "null" {
			continue
		}
		var nested []map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) != nil {
			continue
		}
		base := ref
		if base == "" {
			base = parent
		}
		for _, child := range nested {
			if next, ok := agentFrom(child, base, depth+1); ok {
				children = append(children, next)
			}
		}
		break
	}
	agent.Agents = children
	if agent.ID == "" && agent.Name == "" && agent.Title == "" && len(agent.Agents) == 0 {
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
