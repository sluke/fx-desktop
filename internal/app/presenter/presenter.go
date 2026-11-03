// Package presenter maps the app's business snapshots into the UI's models:
// rows, sections, labels and relative times. It performs no I/O and starts no
// process, so a window can build a frame without waiting for anything, and the
// mapping can be tested without a window or a native display (§3.1).
package presenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/conversation"
	"fx-desktop/internal/fxcli"
)

// APIText renders a JSON value fx returned in a free-form field as compact
// text, so a native row can show it without re-parsing it.
func APIText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	compact, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(compact)
}

func fact(label, value string) chrome.DetailFact {
	return chrome.DetailFact{Label: label, Value: value}
}

func Details(status *fxcli.Status, permissions *fxcli.Permissions, usage *fxcli.Usage, failures []string) []chrome.DetailSection {
	sections := make([]chrome.DetailSection, 0, 3)
	if status != nil {
		mode := status.Mode
		if mode == "" {
			mode = status.PermissionMode
		}
		ultra := "off"
		if status.Ultrafast {
			ultra = "on"
		}
		sections = append(sections, chrome.DetailSection{
			Title: "Status",
			Facts: []chrome.DetailFact{
				fact("Model", status.Model),
				fact("Model origin", status.ModelOrigin),
				fact("Effort", valueOr(status.Effort, "auto")),
				fact("Provider", status.ModelSource),
				fact("Provider endpoint", status.ProviderEndpoint),
				fact("Providers", strings.Join(status.ConnectedProviders, ", ")),
				fact("Auth", status.Auth),
				fact("Auth refreshable", yesNo(status.AuthRefreshable)),
				fact("Permission mode", mode),
				fact("Permission modes", modeIDs(status.Modes)),
				fact("Update channel", status.UpdateChannel),
				fact("Build channel", status.BuildChannel),
				fact("Build revision", status.BuildRevision),
				fact("Workspace", status.Workspace),
				fact("History turns", fmt.Sprintf("%d", status.HistoryTurns)),
				fact("Session grants", fmt.Sprintf("%d", status.SessionGrants)),
				fact("Agent step limit", fmt.Sprintf("%d", status.AgentStepLimit)),
				fact("Ultrafast", ultra),
				fact("MCP servers", fmt.Sprintf("%d", len(status.MCP.Servers))),
			},
		})
		if len(status.MCP.ConfigurationIssues) > 0 || RawPresent(status.MCP.InspectionError) {
			var lines []string
			lines = append(lines, status.MCP.ConfigurationIssues...)
			if RawPresent(status.MCP.InspectionError) {
				lines = append(lines, "Inspection: "+APIText(status.MCP.InspectionError))
			}
			sections = append(sections, chrome.DetailSection{Title: "MCP issues", Text: strings.Join(lines, "\n")})
		}
	}
	if permissions != nil {
		text := ""
		if len(permissions.Rules) > 0 {
			var lines []string
			for i, rule := range permissions.Rules {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, APIText(rule)))
			}
			text = strings.Join(lines, "\n")
		}
		sections = append(sections, chrome.DetailSection{
			Title: "Permissions",
			Facts: []chrome.DetailFact{
				fact("Mode", permissions.Mode),
				fact("Runtime grants", fmt.Sprintf("%d", permissions.GrantCount)),
				fact("Runtime grants available", yesNo(permissions.RuntimeGrantsAvailable)),
				fact("Grant scope", permissions.GrantScope),
				fact("Rules scope", permissions.RulesScope),
				fact("Persistent rules", fmt.Sprintf("%d", len(permissions.Rules))),
			},
			Text: text,
		})
		if len(permissions.Grants) > 0 {
			var grants []string
			for i, grant := range permissions.Grants {
				grants = append(grants, fmt.Sprintf("%d. %s", i+1, APIText(grant)))
			}
			sections = append(sections, chrome.DetailSection{
				Title: "Runtime grants",
				Text:  strings.Join(grants, "\n") + "\nRevoking runtime grants is only available in fx's interactive shell.",
			})
		}
	}
	if usage != nil {
		status := valueOr(usage.Status, usage.Completeness)
		if usage.Reason != "" {
			status += " (" + usage.Reason + ")"
		}
		sections = append(sections, chrome.DetailSection{
			Title: "Usage · " + valueOr(usage.Period, "month"),
			Facts: []chrome.DetailFact{
				fact("Source", usage.Source),
				fact("Status", status),
				fact("Fetched", unixMSLabel(usage.FetchedAtMS)),
				fact("Total tokens", CompactCount(usage.Totals.TotalTokens)),
				fact("Input", CompactCount(usage.Totals.InputTokens)),
				fact("Output", CompactCount(usage.Totals.OutputTokens)),
				fact("Reasoning", CompactCount(usage.Totals.ReasoningTokens)),
				fact("Cache read", CompactCount(usage.Totals.CacheReadTokens)),
				fact("Cache write", CompactCount(usage.Totals.CacheWriteTokens)),
				fact("Requests", fmt.Sprintf("%d", usage.Totals.RequestCount)),
				fact("Spend", fmt.Sprintf("$%.2f", usage.Totals.Spend)),
			},
		})
		var detail []string
		if RawPresent(usage.Budget) {
			detail = append(detail, "Budget: "+APIText(usage.Budget))
		}
		for i, model := range usage.Models {
			if RawPresent(model) {
				detail = append(detail, fmt.Sprintf("Model %d: %s", i+1, APIText(model)))
			}
		}
		if RawPresent(usage.Codex) {
			detail = append(detail, "Codex: "+APIText(usage.Codex))
		}
		if RawPresent(usage.Grok) {
			detail = append(detail, "Grok: "+APIText(usage.Grok))
		}
		if len(detail) > 0 {
			sections = append(sections, chrome.DetailSection{Title: "Usage detail", Text: strings.Join(detail, "\n")})
		}
	}
	if len(failures) > 0 {
		sections = append(sections, chrome.DetailSection{Title: "Unavailable", Text: strings.Join(failures, "\n")})
	}
	return sections
}

// ValueOr is a display fallback: an empty value shows as the fallback rather
// than as a blank cell that reads like a missing field.
func ValueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func modeIDs(modes []fxcli.Mode) string {
	ids := make([]string, 0, len(modes))
	for _, mode := range modes {
		if mode.ID != "" {
			ids = append(ids, mode.ID)
		}
	}
	return strings.Join(ids, ", ")
}

// RawPresent reports whether fx actually sent a field, rather than null.
func RawPresent(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

func unixMSLabel(ms *int64) string {
	if ms == nil || *ms <= 0 {
		return ""
	}
	return time.UnixMilli(*ms).Format("2006-01-02 15:04 MST")
}

// SessionDetail turns `fx session --json` into the compact, read-only
// rows the native Sessions page can render. Long assistant text is trimmed
// here so a large saved conversation cannot dominate one frame.
func SessionDetail(d *fxcli.SessionDetail) *chrome.SessionDetail {
	if d == nil {
		return nil
	}
	view := &chrome.SessionDetail{
		ID:         d.ID,
		Language:   d.ConversationLanguage,
		HistoryLen: d.HistoryLen,
	}
	if d.CreatedAtMS > 0 {
		view.Created = time.UnixMilli(d.CreatedAtMS).Format("2006-01-02 15:04 MST")
	}
	if d.UpdatedAtMS > 0 {
		view.Updated = time.UnixMilli(d.UpdatedAtMS).Format("2006-01-02 15:04 MST")
	}
	for _, turn := range d.History {
		view.Turns = append(view.Turns, chrome.SessionTurn{
			Kind:      turn.Kind,
			User:      conversation.Truncate(turn.User.Text, 2000),
			Images:    len(turn.User.Images),
			Assistant: conversation.Truncate(turn.Assistant, 2000),
			Tools:     turn.CompletedToolNames,
		})
	}
	return view
}

func SlackStatus(s *fxcli.SlackStatus) chrome.SlackStatus {
	if s == nil {
		return chrome.SlackStatus{}
	}
	view := chrome.SlackStatus{Installed: s.Installed, AppID: s.AppID, TeamID: s.TeamID, BotUserID: s.BotUserID}
	if s.ExpiresAtMS != nil && *s.ExpiresAtMS > 0 {
		view.Expires = time.UnixMilli(*s.ExpiresAtMS).Format("2006-01-02 15:04 MST")
	}
	if s.RefreshExpiresAt != nil && *s.RefreshExpiresAt > 0 {
		view.RefreshExpires = time.UnixMilli(*s.RefreshExpiresAt).Format("2006-01-02 15:04 MST")
	}
	return view
}

// CompactCount renders a token count the way fx does: 1.2k, 34k, 1.8M.
func CompactCount(n int64) string {
	switch {
	case n < 0:
		return "0"
	case n < 1000:
		return digits(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

func digits(n int64) string {
	// Negative input would otherwise yield the empty string, which reads
	// as "no value" everywhere it is shown.
	if n <= 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// RelTime renders a Unix millisecond timestamp as a short relative label.
func RelTime(ms int64, now time.Time) string {
	if ms <= 0 {
		return ""
	}
	d := now.Sub(time.UnixMilli(ms))
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return digits(int64(d/time.Minute)) + "m ago"
	case d < 24*time.Hour:
		return digits(int64(d/time.Hour)) + "h ago"
	case d < 30*24*time.Hour:
		return digits(int64(d/(24*time.Hour))) + "d ago"
	default:
		return time.UnixMilli(ms).Format("2006-01-02")
	}
}

// Elapsed renders an uptime as m:ss or h:mm:ss.
func Elapsed(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 0 {
		sec = 0
	}
	h, rest := sec/3600, sec%3600
	m, s := rest/60, rest%60
	if h > 0 {
		return clockPart(h) + ":" + pad2(m) + ":" + pad2(s)
	}
	return pad2(m) + ":" + pad2(s)
}

func clockPart(n int) string {
	if n > 999 {
		n = 999
	}
	var b [3]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}

func pad2(n int) string {
	var b [2]byte
	b[0] = byte('0' + (n/10)%10)
	b[1] = byte('0' + n%10)
	return string(b[:])
}
