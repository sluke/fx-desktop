// Package fxcli owns the fx JSON command line: the argv each inspection
// builds, the wire shape it decodes, and the timeout that bounds it. It is an
// adapter, so it may run processes; the app's business packages never import
// it, and nothing here knows about windows, palettes or MyGo.
package fxcli

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Timeout bounds every read-only fx query the app makes. Interactive sessions
// are unaffected: these are the short --json inspections.
const Timeout = 20 * time.Second

// Status is the machine-readable output of `fx status --json`.
type Status struct {
	Model              string   `json:"model"`
	ModelOrigin        string   `json:"model_origin"`
	ModelSource        string   `json:"model_source"`
	Effort             string   `json:"effort"`
	ProviderEndpoint   string   `json:"provider_endpoint"`
	UpdateChannel      string   `json:"update_channel"`
	BuildChannel       string   `json:"build_channel"`
	BuildRevision      string   `json:"build_revision"`
	Auth               string   `json:"auth"`
	AuthHelp           string   `json:"auth_help"`
	AuthRefreshable    bool     `json:"auth_refreshable"`
	ConnectedProviders []string `json:"connected_providers"`
	PermissionMode     string   `json:"permission_mode"`
	Mode               string   `json:"mode"`
	Modes              []Mode   `json:"modes"`
	Workspace          string   `json:"workspace"`
	HistoryTurns       int      `json:"history_turns"`
	SessionGrants      int      `json:"session_permission_grants"`
	AgentStepLimit     int      `json:"agent_step_limit"`
	Ultrafast          bool     `json:"ultrafast_requested"`
	MCP                MCP      `json:"mcp"`
}

// Mode is one permission mode from `fx status --json`.
type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Doctor is the machine-readable output of `fx doctor --json`.
type Doctor struct {
	OKCount        int           `json:"ok_count"`
	WarnCount      int           `json:"warn_count"`
	FailCount      int           `json:"fail_count"`
	Workspace      string        `json:"workspace"`
	Model          string        `json:"model"`
	ModelSource    string        `json:"model_source"`
	Auth           string        `json:"auth"`
	PermissionMode string        `json:"permission_mode"`
	AgentStepLimit int           `json:"agent_step_limit"`
	Checks         []DoctorCheck `json:"checks"`
	MCP            MCP           `json:"mcp"`
}

// DoctorCheck is one preflight row.
type DoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// SlackStatus is `fx slack status --json`.
type SlackStatus struct {
	Action           string `json:"action"`
	Installed        bool   `json:"installed"`
	AppID            string `json:"app_id"`
	TeamID           string `json:"team_id"`
	BotUserID        string `json:"bot_user_id"`
	ExpiresAtMS      *int64 `json:"expires_at_ms"`
	RefreshExpiresAt *int64 `json:"refresh_expires_at_ms"`
}

// SessionsConvert is `fx sessions convert --json`.
type SessionsConvert struct {
	Converted  int      `json:"converted"`
	Busy       int      `json:"busy"`
	Unreadable int      `json:"unreadable"`
	Skipped    []string `json:"skipped"`
	LeftOut    []string `json:"left_out"`
	Kept       []string `json:"kept"`
}

// Models is `fx models --json`. The IDs are the launch choices; Models
// additionally carries the provider source for the native list.
type Models struct {
	Kind                string   `json:"kind"`
	Count               int      `json:"count"`
	ShownCount          int      `json:"shown_count"`
	MoreCount           int      `json:"more_count"`
	PrivateModelsHidden bool     `json:"private_models_hidden"`
	IDs                 []string `json:"ids"`
	Models              []Model  `json:"models"`
}

// Model is one model in `fx models --json`.
type Model struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

// MCP counts configured servers without assuming their shape: the server
// entries vary by transport, so they stay raw and only their number is read.
type MCP struct {
	ConnectionCheck     string            `json:"connection_check"`
	Servers             []json.RawMessage `json:"servers"`
	ConfigurationIssues []string          `json:"configuration_issues"`
	InspectionError     json.RawMessage   `json:"inspection_error"`
}

// MCPList is `fx mcp list --json`.
type MCPList struct {
	Servers []MCPServer `json:"servers"`
}

// MCPServer is one configured MCP server. The same shape is embedded in
// `fx mcp show NAME --json`.
type MCPServer struct {
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	File      string    `json:"file"`
	Transport string    `json:"transport"`
	Status    string    `json:"status"`
	Error     string    `json:"error"`
	Tools     []MCPTool `json:"tools"`
}

// MCPTool is one tool reported by a connected server.
type MCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// MCPShow is `fx mcp show NAME --json`.
type MCPShow struct {
	MCPServer
	Version string `json:"version"`
	URL     string `json:"url"`
	Command string `json:"command"`
}

// Usage reads totals from `fx usage --json`. The CLI version determines
// the source: v0.0.13 used local records, while v0.0.14 may require sign-in.
type Usage struct {
	SchemaVersion int               `json:"schema_version"`
	Period        string            `json:"period"`
	Completeness  string            `json:"completeness"`
	Source        string            `json:"source"`
	Status        string            `json:"status"`
	Reason        string            `json:"reason"`
	FetchedAtMS   *int64            `json:"fetched_at_ms"`
	Budget        json.RawMessage   `json:"budget"`
	Models        []json.RawMessage `json:"models"`
	Codex         json.RawMessage   `json:"codex"`
	Grok          json.RawMessage   `json:"grok"`
	Totals        struct {
		TotalTokens      int64   `json:"total_tokens"`
		InputTokens      int64   `json:"input_tokens"`
		OutputTokens     int64   `json:"output_tokens"`
		ReasoningTokens  int64   `json:"reasoning_tokens"`
		CacheReadTokens  int64   `json:"cache_read_tokens"`
		CacheWriteTokens int64   `json:"cache_write_tokens"`
		RequestCount     int64   `json:"request_count"`
		Spend            float64 `json:"spend"`
	} `json:"totals"`
}

// Session is one saved conversation of `fx sessions --json`.
type Session struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Preview       string `json:"preview"`
	WorkspaceRoot string `json:"workspace_root"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	UpdatedAtMS   int64  `json:"updated_at_ms"`
	HistoryLen    int    `json:"history_len"`
}

// SessionDetail is `fx session --id ID --json`.
type SessionDetail struct {
	ID                   string        `json:"id"`
	CreatedAtMS          int64         `json:"created_at_ms"`
	UpdatedAtMS          int64         `json:"updated_at_ms"`
	HistoryLen           int           `json:"history_len"`
	ConversationLanguage string        `json:"conversation_language"`
	History              []SessionTurn `json:"history"`
}

// SessionTurn is one saved turn. fx uses different kind values for
// interrupted, assistant and tool-assisted turns; the native view keeps the
// kind as fx named it rather than inventing a state.
type SessionTurn struct {
	Kind string `json:"kind"`
	User struct {
		Text   string            `json:"text"`
		Images []json.RawMessage `json:"images"`
	} `json:"user"`
	Assistant          string          `json:"assistant"`
	ToolCall           json.RawMessage `json:"tool_call"`
	CompletedToolNames []string        `json:"completed_tool_names"`
}

// Permissions is `fx permissions --json`.
type Permissions struct {
	Mode                   string            `json:"mode"`
	GrantCount             int               `json:"grant_count"`
	GrantScope             string            `json:"grant_scope"`
	RuntimeGrantsAvailable bool              `json:"runtime_grants_available"`
	RulesScope             string            `json:"rules_scope"`
	Rules                  []json.RawMessage `json:"rules"`
	Grants                 []json.RawMessage `json:"grants"`
}

// Run executes fx with args plus --json in dir and decodes the result into
// dst. An empty path is a configuration error, reported rather than run.
func Run(ctx context.Context, fxPath, dir string, dst any, args ...string) error {
	if fxPath == "" {
		return fmt.Errorf("fx path is not set")
	}
	cmd := exec.CommandContext(ctx, fxPath, append(args, "--json")...)
	cmd.Dir = dir
	// Bound inherited output pipes on shutdown: a cancelled query must not
	// wait for a grandchild that still holds stdout.
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dst)
}

// query runs one inspection under its own deadline.
func query[T any](ctx context.Context, fxPath, dir string, args ...string) (*T, error) {
	ctx, cancel := WithTimeout(ctx)
	defer cancel()
	var out T
	if err := Run(ctx, fxPath, dir, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}

// WithTimeout gives a query its own deadline.
func WithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, Timeout)
}

// ReadStatus reads `fx status --json`.
func ReadStatus(ctx context.Context, fxPath, dir string) (*Status, error) {
	return query[Status](ctx, fxPath, dir, "status")
}

// ReadUsage reads `fx usage --json`.
func ReadUsage(ctx context.Context, fxPath, dir string) (*Usage, error) {
	return query[Usage](ctx, fxPath, dir, "usage")
}

// ReadPermissions reads `fx permissions --json`.
func ReadPermissions(ctx context.Context, fxPath, dir string) (*Permissions, error) {
	return query[Permissions](ctx, fxPath, dir, "permissions")
}

// ReadModels reads `fx models --json`.
func ReadModels(ctx context.Context, fxPath, dir string) (*Models, error) {
	return query[Models](ctx, fxPath, dir, "models")
}

// ReadDoctor reads `fx doctor --json`.
func ReadDoctor(ctx context.Context, fxPath, dir string) (*Doctor, error) {
	return query[Doctor](ctx, fxPath, dir, "doctor")
}

// ReadSlackStatus reads `fx slack status --json`.
func ReadSlackStatus(ctx context.Context, fxPath, dir string) (*SlackStatus, error) {
	return query[SlackStatus](ctx, fxPath, dir, "slack", "status")
}

// ReadSessionsConvert bulk-converts sessions saved in the previous format. fx
// 0.0.15 removed the --sessions-v2 store flag; conversion is the default
// store's own command.
func ReadSessionsConvert(ctx context.Context, fxPath, dir string) (*SessionsConvert, error) {
	return query[SessionsConvert](ctx, fxPath, dir, "sessions", "convert")
}

// Auth reads the slice of `fx status --json` that answers "can fx reach a
// provider at all?". It belongs to the machine rather than to a session.
//
// Measured shapes (fx 0.0.14): signed in gives `auth:"configured provider"`
// with `connected_providers`; signed out gives `auth:"missing"` plus
// `auth_help`, fx's own sentence about what to run. Help is therefore the
// signal — a state the GUI does not know about still arrives with words.
type Auth struct {
	Auth      string
	Help      string
	Providers []string
}

// ReadAuth asks fx itself, without a caller-supplied scope. A failure returns
// the zero value, which renders as "nothing known" rather than "signed out".
func ReadAuth(fxPath, dir string) Auth {
	ctx, cancel := WithTimeout(context.Background())
	defer cancel()
	var s Status
	if err := Run(ctx, fxPath, dir, &s, "status"); err != nil {
		return Auth{}
	}
	return Auth{Auth: s.Auth, Help: s.AuthHelp, Providers: s.ConnectedProviders}
}
