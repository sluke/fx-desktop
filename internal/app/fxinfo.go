package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// fxTimeout bounds every read-only fx query the GUI makes. Interactive
// sessions are unaffected: these are the short --json inspections.
const fxTimeout = 20 * time.Second

// FxStatus is the machine-readable output of `fx status --json`.
type FxStatus struct {
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
	Modes              []FxMode `json:"modes"`
	Workspace          string   `json:"workspace"`
	HistoryTurns       int      `json:"history_turns"`
	SessionGrants      int      `json:"session_permission_grants"`
	AgentStepLimit     int      `json:"agent_step_limit"`
	Ultrafast          bool     `json:"ultrafast_requested"`
	MCP                FxMCP    `json:"mcp"`
}

// FxMode is one permission mode from `fx status --json`.
type FxMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// FxDoctor is the machine-readable output of `fx doctor --json`.
type FxDoctor struct {
	OKCount        int             `json:"ok_count"`
	WarnCount      int             `json:"warn_count"`
	FailCount      int             `json:"fail_count"`
	Workspace      string          `json:"workspace"`
	Model          string          `json:"model"`
	ModelSource    string          `json:"model_source"`
	Auth           string          `json:"auth"`
	PermissionMode string          `json:"permission_mode"`
	AgentStepLimit int             `json:"agent_step_limit"`
	Checks         []FxDoctorCheck `json:"checks"`
	MCP            FxMCP           `json:"mcp"`
}

// FxDoctorCheck is one preflight row.
type FxDoctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// FxSlackStatus is `fx slack status --json`.
type FxSlackStatus struct {
	Action           string `json:"action"`
	Installed        bool   `json:"installed"`
	AppID            string `json:"app_id"`
	TeamID           string `json:"team_id"`
	BotUserID        string `json:"bot_user_id"`
	ExpiresAtMS      *int64 `json:"expires_at_ms"`
	RefreshExpiresAt *int64 `json:"refresh_expires_at_ms"`
}

// FxSessionsConvert is `fx sessions convert --json`.
type FxSessionsConvert struct {
	Converted  int      `json:"converted"`
	Busy       int      `json:"busy"`
	Unreadable int      `json:"unreadable"`
	Skipped    []string `json:"skipped"`
	LeftOut    []string `json:"left_out"`
	Kept       []string `json:"kept"`
}

// FxModels is `fx models --json`. The IDs are the launch choices; Models
// additionally carries the provider source for the native list.
type FxModels struct {
	Kind                string    `json:"kind"`
	Count               int       `json:"count"`
	ShownCount          int       `json:"shown_count"`
	MoreCount           int       `json:"more_count"`
	PrivateModelsHidden bool      `json:"private_models_hidden"`
	IDs                 []string  `json:"ids"`
	Models              []FxModel `json:"models"`
}

// FxModel is one model in `fx models --json`.
type FxModel struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

// FxMCP counts configured servers without assuming their shape: the server
// entries vary by transport, so they stay raw and only their number is read.
type FxMCP struct {
	ConnectionCheck     string            `json:"connection_check"`
	Servers             []json.RawMessage `json:"servers"`
	ConfigurationIssues []string          `json:"configuration_issues"`
	InspectionError     json.RawMessage   `json:"inspection_error"`
}

// FxMCPList is `fx mcp list --json`.
type FxMCPList struct {
	Servers []FxMCPServer `json:"servers"`
}

// FxMCPServer is one configured MCP server. The same shape is embedded in
// `fx mcp show NAME --json`.
type FxMCPServer struct {
	Name      string      `json:"name"`
	Source    string      `json:"source"`
	File      string      `json:"file"`
	Transport string      `json:"transport"`
	Status    string      `json:"status"`
	Error     string      `json:"error"`
	Tools     []FxMCPTool `json:"tools"`
}

// FxMCPTool is one tool reported by a connected server.
type FxMCPTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// FxMCPShow is `fx mcp show NAME --json`.
type FxMCPShow struct {
	FxMCPServer
	Version string `json:"version"`
	URL     string `json:"url"`
	Command string `json:"command"`
}

// FxUsage reads totals from `fx usage --json`. The CLI version determines
// the source: v0.0.13 used local records, while v0.0.14 may require sign-in.
type FxUsage struct {
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

// FxSession is one saved conversation of `fx sessions --json`.
type FxSession struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Preview       string `json:"preview"`
	WorkspaceRoot string `json:"workspace_root"`
	CreatedAtMS   int64  `json:"created_at_ms"`
	UpdatedAtMS   int64  `json:"updated_at_ms"`
	HistoryLen    int    `json:"history_len"`
}

// FxSessionDetail is `fx session --id ID --json`.
type FxSessionDetail struct {
	ID                   string          `json:"id"`
	CreatedAtMS          int64           `json:"created_at_ms"`
	UpdatedAtMS          int64           `json:"updated_at_ms"`
	HistoryLen           int             `json:"history_len"`
	ConversationLanguage string          `json:"conversation_language"`
	History              []FxSessionTurn `json:"history"`
}

// FxSessionTurn is one saved turn. fx uses different kind values for
// interrupted, assistant and tool-assisted turns; the native view keeps the
// kind as fx named it rather than inventing a state.
type FxSessionTurn struct {
	Kind string `json:"kind"`
	User struct {
		Text   string            `json:"text"`
		Images []json.RawMessage `json:"images"`
	} `json:"user"`
	Assistant          string          `json:"assistant"`
	ToolCall           json.RawMessage `json:"tool_call"`
	CompletedToolNames []string        `json:"completed_tool_names"`
}

// FxPermissions is `fx permissions --json`.
type FxPermissions struct {
	Mode                   string            `json:"mode"`
	GrantCount             int               `json:"grant_count"`
	GrantScope             string            `json:"grant_scope"`
	RuntimeGrantsAvailable bool              `json:"runtime_grants_available"`
	RulesScope             string            `json:"rules_scope"`
	Rules                  []json.RawMessage `json:"rules"`
	Grants                 []json.RawMessage `json:"grants"`
}

// fxJSON runs fx with args plus --json in dir and decodes the result.
func fxJSON(ctx context.Context, fxPath, dir string, dst any, args ...string) error {
	if fxPath == "" {
		return fmt.Errorf("fx path is not set")
	}
	cmd := exec.CommandContext(ctx, fxPath, append(args, "--json")...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dst)
}

// withTimeout gives a query its own deadline.
func withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, fxTimeout)
}

func fxStatus(ctx context.Context, fxPath, dir string) (*FxStatus, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var s FxStatus
	if err := fxJSON(ctx, fxPath, dir, &s, "status"); err != nil {
		return nil, err
	}
	return &s, nil
}

func fxUsage(ctx context.Context, fxPath, dir string) (*FxUsage, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var u FxUsage
	if err := fxJSON(ctx, fxPath, dir, &u, "usage"); err != nil {
		return nil, err
	}
	return &u, nil
}

func fxPermissions(ctx context.Context, fxPath, dir string) (*FxPermissions, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var p FxPermissions
	if err := fxJSON(ctx, fxPath, dir, &p, "permissions"); err != nil {
		return nil, err
	}
	return &p, nil
}

func fxModels(ctx context.Context, fxPath, dir string) (*FxModels, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var m FxModels
	if err := fxJSON(ctx, fxPath, dir, &m, "models"); err != nil {
		return nil, err
	}
	return &m, nil
}

func fxDoctor(ctx context.Context, fxPath, dir string) (*FxDoctor, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var d FxDoctor
	if err := fxJSON(ctx, fxPath, dir, &d, "doctor"); err != nil {
		return nil, err
	}
	return &d, nil
}

func fxSlackStatus(ctx context.Context, fxPath, dir string) (*FxSlackStatus, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var s FxSlackStatus
	if err := fxJSON(ctx, fxPath, dir, &s, "slack", "status"); err != nil {
		return nil, err
	}
	return &s, nil
}

// fxSessionsConvert bulk-converts sessions saved in the previous format.
// fx 0.0.15 removed the --sessions-v2 store flag; conversion is the default
// store's own command.
func fxSessionsConvert(ctx context.Context, fxPath, dir string) (*FxSessionsConvert, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var s FxSessionsConvert
	if err := fxJSON(ctx, fxPath, dir, &s, "sessions", "convert"); err != nil {
		return nil, err
	}
	return &s, nil
}

func fxSessions(ctx context.Context, fxPath, dir string) ([]FxSession, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	page, err := readSessionPage(ctx, fxPath, dir, false, 5, "")
	if err != nil {
		return nil, err
	}
	return page.Sessions[:min(5, len(page.Sessions))], nil
}

// FxAuth is the slice of `fx status --json` that answers "can fx reach a
// provider at all?". It belongs to the machine rather than to a session, so
// the Coordinator holds one copy that every window reads (§4.3).
//
// Measured shapes (fx 0.0.14): signed in gives `auth:"configured provider"`
// with `connected_providers`; signed out gives `auth:"missing"` plus
// `auth_help`, fx's own sentence about what to run. Help is therefore the
// signal — a state the GUI does not know about still arrives with words.
type FxAuth struct {
	Auth      string
	Help      string
	Providers []string
}

// readFxAuth asks fx itself. A failure returns the zero value, which renders
// as "nothing known" rather than as "signed out".
func readFxAuth(fxPath, dir string) FxAuth {
	ctx, cancel := withTimeout(context.Background())
	defer cancel()
	var s FxStatus
	if err := fxJSON(ctx, fxPath, dir, &s, "status"); err != nil {
		return FxAuth{}
	}
	return FxAuth{Auth: s.Auth, Help: s.AuthHelp, Providers: s.ConnectedProviders}
}

// fxClearHistory was removed with the "Clear history" button: fx 0.0.14 has no
// way to delete saved sessions (`fx sessions` has no clear flag and ACP
// `session/remove` answers -32601), so the GUI stops offering the action
// (see the design ledger, §A.6.2).

// compactCount renders a token count the way fx does: 1.2k, 34k, 1.8M.
func compactCount(n int64) string {
	switch {
	case n < 0:
		return "0"
	case n < 1000:
		return itoaInt(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

func itoaInt(n int64) string {
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

// relTime renders a Unix millisecond timestamp as a short relative label.
func relTime(ms int64, now time.Time) string {
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
		return itoaInt(int64(d/time.Minute)) + "m ago"
	case d < 24*time.Hour:
		return itoaInt(int64(d/time.Hour)) + "h ago"
	case d < 30*24*time.Hour:
		return itoaInt(int64(d/(24*time.Hour))) + "d ago"
	default:
		return time.UnixMilli(ms).Format("2006-01-02")
	}
}
