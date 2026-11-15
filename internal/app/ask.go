package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"fx-desktop/internal/app/chrome"
)

// askArgs builds the exact argv for a native Quick ask request. The prompt is
// one argv entry; a prompt beginning with a dash is protected with `--` so it
// cannot become an fx option.
func askArgs(req chrome.AskRequest) ([]string, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("Enter a prompt")
	}
	if strings.ContainsRune(prompt, 0) {
		return nil, fmt.Errorf("Prompt cannot contain NUL")
	}
	if req.NoAdditionalDirs && strings.TrimSpace(req.AddDirs) != "" {
		return nil, fmt.Errorf("Choose additional directories or ignore saved ones, not both")
	}
	globals, err := globalArgsFromForm(req.ContextLimits, req.AddDirs, "")
	if err != nil {
		return nil, err
	}
	if req.NoAdditionalDirs {
		globals = append(globals, "--no-additional-dirs")
	}
	args := append(globals, "ask")
	switch strings.TrimSpace(req.Access) {
	case "", "auto":
		args = append(args, "--auto")
	case "full-access":
		args = append(args, "--full-access")
	default:
		return nil, fmt.Errorf("Choose automatic review or full access")
	}
	value := func(raw, label string) (string, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return "", nil
		}
		if strings.HasPrefix(raw, "-") || strings.ContainsRune(raw, 0) || strings.ContainsAny(raw, "\r\n") {
			return "", fmt.Errorf("Enter a valid %s", label)
		}
		return raw, nil
	}
	if provider, err := value(req.Provider, "provider"); err != nil {
		return nil, err
	} else if provider != "" {
		args = append(args, "--provider", provider)
	}
	if model, err := value(req.Model, "model id"); err != nil {
		return nil, err
	} else if model != "" {
		args = append(args, "--model", model)
	}
	if effort, err := value(req.Effort, "reasoning effort"); err != nil {
		return nil, err
	} else if effort != "" && !strings.EqualFold(effort, "auto") {
		args = append(args, "--effort", effort)
	}
	switch strings.TrimSpace(req.Fast) {
	case "", "default":
	case "on":
		args = append(args, "--fast")
	case "off":
		args = append(args, "--no-fast")
	default:
		return nil, fmt.Errorf("Choose default, on or off for fast mode")
	}
	switch strings.TrimSpace(req.Ultra) {
	case "", "default":
	case "on":
		args = append(args, "--ultrafast")
	case "off":
		args = append(args, "--no-ultrafast")
	default:
		return nil, fmt.Errorf("Choose default, on or off for Ultra mode")
	}
	if order, err := value(req.ProviderOrder, "provider order"); err != nil {
		return nil, err
	} else if order != "" {
		args = append(args, "--provider-order", order)
		if req.Strict {
			args = append(args, "--provider-strict")
		}
	} else if req.Strict {
		return nil, fmt.Errorf("Provider strict mode requires a provider order")
	}
	if session, err := value(req.Session, "session id"); err != nil {
		return nil, err
	} else if session != "" {
		if req.NoSave {
			return nil, fmt.Errorf("A resumed session cannot be combined with --no-save")
		}
		args = append(args, "--resume", session)
	}
	if req.NoSave {
		args = append(args, "--no-save")
	}
	for _, line := range strings.Split(strings.ReplaceAll(req.Images, "\r\n", "\n"), "\n") {
		path := strings.TrimSpace(line)
		if path == "" {
			continue
		}
		if strings.ContainsRune(path, 0) {
			return nil, fmt.Errorf("Image paths cannot contain NUL")
		}
		args = append(args, "--image", path)
	}
	if timeout := strings.TrimSpace(req.Timeout); timeout != "" {
		n, err := strconv.Atoi(timeout)
		if err != nil || n < 1 || n > 86400 {
			return nil, fmt.Errorf("Enter an fx timeout in seconds")
		}
		args = append(args, "--timeout", strconv.Itoa(n))
	}
	configs, err := askConfigFlags(req.Config)
	if err != nil {
		return nil, err
	}
	args = append(args, configs...)
	if system := strings.TrimSpace(req.System); system != "" {
		if strings.ContainsRune(system, 0) {
			return nil, fmt.Errorf("System prompt cannot contain NUL")
		}
		args = append(args, "--system", system)
	}
	args = append(args, "--json")
	if strings.HasPrefix(prompt, "-") {
		args = append(args, "--")
	}
	args = append(args, prompt)
	return args, nil
}

func askConfigFlags(text string) ([]string, error) {
	last := map[string]string{}
	var order []string
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok || key == "" || strings.HasPrefix(key, "-") || strings.ContainsAny(key, " \t") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("Enter config overrides as key=value")
		}
		switch key {
		case "permission", "permission_mode", "model", "provider", "providers", "models", "credential_source":
			return nil, fmt.Errorf("Use the dedicated field for %s", key)
		}
		if strings.HasPrefix(key, "permission.") || strings.HasPrefix(key, "providers.") || strings.HasPrefix(key, "models.") {
			return nil, fmt.Errorf("Use the dedicated field for %s", key)
		}
		if _, seen := last[key]; !seen {
			order = append(order, key)
		}
		last[key] = value
	}
	var args []string
	for _, key := range order {
		args = append(args, "--config", key+"="+last[key])
	}
	return args, nil
}

func askWait(req chrome.AskRequest) (time.Duration, error) {
	wait := 10 * time.Minute
	if raw := strings.TrimSpace(req.WaitMinutes); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 180 {
			return 0, fmt.Errorf("Enter a wait from 1 to 180 minutes")
		}
		wait = time.Duration(n) * time.Minute
	}
	if raw := strings.TrimSpace(req.Timeout); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("Enter an fx timeout in seconds")
		}
		floor := time.Duration(n)*time.Second + 15*time.Second
		if floor > wait {
			wait = floor
		}
	}
	return wait, nil
}

type fxAskUsage struct {
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens"`
	CacheWriteTokens *int64 `json:"cache_write_tokens"`
}

type fxAskRecovery struct {
	State   string `json:"state"`
	Attempt int    `json:"attempt"`
	Message string `json:"message"`
	// Need is what fx says the user must do, e.g. "quota" or "sign_in".
	Need string `json:"need"`
}

// fxAskAuthFailure is the documented authentication failure object. It used
// to be modelled as a string, which made the whole result fail to decode and
// threw away the partial answer (A4).
type fxAskAuthFailure struct {
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	HTTPStatus int    `json:"http_status"`
}

// Label is the one-line summary shown beside the partial answer.
func (a *fxAskAuthFailure) Label() string {
	if a == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if a.Source != "" {
		parts = append(parts, a.Source)
	}
	if a.Reason != "" {
		parts = append(parts, a.Reason)
	} else if len(parts) == 0 {
		parts = append(parts, "authentication required")
	}
	if a.HTTPStatus != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", a.HTTPStatus))
	}
	return strings.Join(parts, " · ")
}

type fxAskResult struct {
	Output           string            `json:"output"`
	FinalOutput      string            `json:"final_output"`
	ExitCode         int               `json:"exit_code"`
	Model            string            `json:"model"`
	ResolvedProvider string            `json:"resolved_provider"`
	SessionID        string            `json:"session_id"`
	Steps            int               `json:"steps"`
	ToolCalls        []json.RawMessage `json:"tool_calls"`
	Usage            *fxAskUsage       `json:"usage"`
	Error            string            `json:"error"`
	// AuthFailure stays raw so a documented object decodes as one, while a
	// string or other type becomes an explicit protocol error instead of
	// failing the whole result.
	AuthFailure json.RawMessage `json:"auth_failure"`
	Recovery    *fxAskRecovery  `json:"recovery"`
}

// authFailure decodes the object form. A non-empty value of another type is
// reported as protocol drift; an empty string or null means fx named no
// authentication failure.
func (r fxAskResult) authFailure() (*fxAskAuthFailure, error) {
	raw := bytes.TrimSpace(r.AuthFailure)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == `""` {
		return nil, nil
	}
	var failure fxAskAuthFailure
	if err := json.Unmarshal(raw, &failure); err != nil {
		return nil, fmt.Errorf("fx ask: auth_failure is not an object")
	}
	return &failure, nil
}

// askResultMeta summarizes the machine-readable fields fx adds around the
// answer. It deliberately stays a single muted line: the answer is still the
// only thing that should dominate the result panel.
func askResultMeta(result fxAskResult) string {
	var parts []string
	if result.Model != "" {
		parts = append(parts, "model "+result.Model)
	}
	if result.ResolvedProvider != "" {
		parts = append(parts, "provider "+result.ResolvedProvider)
	}
	if result.Steps > 0 {
		parts = append(parts, fmt.Sprintf("%d step(s)", result.Steps))
	}
	if len(result.ToolCalls) > 0 {
		parts = append(parts, fmt.Sprintf("%d tool call(s)", len(result.ToolCalls)))
	}
	if usage := result.Usage; usage != nil {
		var tokens []string
		if usage.InputTokens != nil {
			tokens = append(tokens, "in "+compactCount(*usage.InputTokens))
		}
		if usage.OutputTokens != nil {
			tokens = append(tokens, "out "+compactCount(*usage.OutputTokens))
		}
		if usage.CacheReadTokens != nil {
			tokens = append(tokens, "cache read "+compactCount(*usage.CacheReadTokens))
		}
		if usage.CacheWriteTokens != nil {
			tokens = append(tokens, "cache write "+compactCount(*usage.CacheWriteTokens))
		}
		if len(tokens) > 0 {
			parts = append(parts, strings.Join(tokens, ", "))
		}
	}
	if result.SessionID != "" {
		parts = append(parts, "session "+result.SessionID)
	}
	if result.FinalOutput == "" && strings.TrimSpace(result.Output) != "" && (result.Model != "" || result.Recovery != nil || result.Error != "" || len(bytes.TrimSpace(result.AuthFailure)) > 0) {
		parts = append(parts, "partial output")
	}
	if result.Recovery != nil && result.Recovery.State != "" {
		label := result.Recovery.State
		if result.Recovery.Attempt > 0 {
			label = fmt.Sprintf("%s attempt %d", label, result.Recovery.Attempt)
		}
		parts = append(parts, "recovery "+label)
	}
	return strings.Join(parts, " · ")
}

// maxAskDiagnosticBytes bounds the raw output shown when fx did not return
// the --json document it was asked for. A protocol failure must be visible,
// but it must not dump a 2 MiB JSON prefix into the result panel (A5).
const maxAskDiagnosticBytes = 512

// askOutcome is the Ask domain result of one fx invocation. It is the single
// boundary between command streams and the UI model, so truncation, decode
// status, cancellation and process outcome cannot be lost on the way there
// (A5). Quick Ask no longer decodes stdout on its own.
type askOutcome struct {
	result  fxAskResult
	parsed  bool
	text    string
	stderr  string
	runErr  error
	decode  error
	notes   []string
	truncated bool
}

// resolveAskOutcome turns a complete fxStreams into an Ask result. A leading
// JSON document is decoded with a streaming decoder so that valid JSON
// followed by junk is reported as extra output rather than silently guessed
// at. Every kind of incompleteness becomes an explicit note.
func resolveAskOutcome(streams fxStreams) askOutcome {
	o := askOutcome{text: streams.stdout, stderr: streams.stderr, runErr: streams.err}
	decoder := json.NewDecoder(strings.NewReader(streams.stdout))
	var result fxAskResult
	if err := decoder.Decode(&result); err == nil {
		o.parsed = true
		o.result = result
		var extra json.RawMessage
		if trailing := decoder.Decode(&extra); !errors.Is(trailing, io.EOF) {
			o.notes = append(o.notes, "fx ask sent extra output after the JSON document; the result is incomplete.")
		}
	} else {
		o.decode = err
	}
	if streams.stdoutTruncated {
		o.truncated = true
		o.notes = append(o.notes, "fx ask output exceeded the capture limit; the answer is incomplete.")
	}
	if streams.stderrTruncated {
		o.notes = append(o.notes, "fx ask diagnostics were truncated.")
	}
	if streams.canceled {
		o.notes = append(o.notes, "fx ask was cancelled before it finished.")
	}
	if streams.timedOut {
		o.notes = append(o.notes, "fx ask timed out before it finished.")
	}
	return o
}

// boundedDiagnostic keeps a short, valid-UTF-8 prefix of raw output for the
// problem text. It is deliberately never used as the assistant answer.
func boundedDiagnostic(text string) string {
	if len(text) > maxAskDiagnosticBytes {
		text = text[:maxAskDiagnosticBytes]
	}
	return strings.TrimSpace(strings.ToValidUTF8(text, ""))
}

func (o askOutcome) presentation() (output, meta, problem string) {
	switch {
	case o.parsed:
		output = strings.TrimSpace(o.result.FinalOutput)
		if output == "" {
			output = strings.TrimSpace(o.result.Output)
		}
		meta = askResultMeta(o.result)
		auth, authErr := o.result.authFailure()
		switch {
		case authErr != nil:
			// The answer already received stays in output; only the reason
			// is reported as a protocol problem, never as the answer (A4).
			problem = authErr.Error()
		case auth != nil:
			problem = "fx ask: " + auth.Label()
		case o.result.Error != "":
			problem = "fx ask: " + o.result.Error
		case o.result.Recovery != nil && (o.result.Recovery.State == "need" || o.result.Recovery.State == "need_user"):
			detail := o.result.Recovery.Message
			if strings.TrimSpace(detail) == "" {
				detail = o.result.Recovery.Need
			}
			problem = "fx ask: " + recoveryLabel(o.result.Recovery.State, o.result.Recovery.Attempt, detail)
		case strings.TrimSpace(output) == "" && len(o.notes) == 0:
			problem = "fx returned no assistant output"
		}
	case strings.TrimSpace(o.text) != "":
		// The command asked for --json. An arbitrary prefix of a cut-off
		// document is neither an answer nor a trustworthy partial one.
		if o.truncated {
			problem = "fx ask: --json output was cut off before it could be decoded."
		} else {
			problem = "fx ask: fx did not return a readable --json result."
		}
		if diagnostic := boundedDiagnostic(o.text); diagnostic != "" {
			problem += "\n\nRaw output (bounded): " + diagnostic
		}
	default:
		problem = "fx returned no output"
	}
	if note := strings.TrimSpace(o.stderr); note != "" {
		if o.parsed {
			if meta != "" {
				meta += " · "
			}
			meta += note
		} else if problem != "" {
			problem += "\n\n" + note
		} else {
			problem = note
		}
	}
	if o.runErr != nil && problem == "" {
		problem = o.runErr.Error()
	}
	// Incompleteness is never downgraded to a normal success: it is shown
	// even when a partial answer was decoded.
	var parts []string
	if problem != "" {
		parts = append(parts, problem)
	}
	parts = append(parts, o.notes...)
	return output, meta, strings.Join(parts, "\n\n")
}

// askPresentation is the legacy entry point for callers that already hold a
// decoded result. New code must go through resolveAskOutcome so no stream
// metadata is skipped.
func askPresentation(result fxAskResult, parsed bool, text, stderr string, runErr error) (output, meta, problem string) {
	return askOutcome{result: result, parsed: parsed, text: text, stderr: stderr, runErr: runErr}.presentation()
}

func (c *Coordinator) cancelAsk(s *ProjectWindow) {
	s.shell.AskGeneration++
	s.cancelFeature("ask")
	s.shell.AskBusy = false
	s.shell.AskProblem = "Cancelled"
	s.invalidate()
}

func (c *Coordinator) runAsk(s *ProjectWindow, req chrome.AskRequest) {
	if c.quitting() {
		s.shell.AskBusy = false
		s.shell.AskOutput = ""
		s.shell.AskMeta = ""
		s.shell.AskProblem = errQuitting.Error()
		s.invalidate()
		return
	}
	args, err := askArgs(req)
	if err != nil {
		s.shell.AskBusy = false
		s.shell.AskProblem = err.Error()
		s.invalidate()
		return
	}
	wait, err := askWait(req)
	if err != nil {
		s.shell.AskBusy = false
		s.shell.AskProblem = err.Error()
		s.invalidate()
		return
	}
	s.shell.AskGeneration++
	generation := s.shell.AskGeneration
	s.shell.AskBusy = true
	s.shell.AskProblem = ""
	s.shell.AskOutput = ""
	s.shell.AskMeta = ""
	s.invalidate()
	executable, dir := c.fxPathSnapshot(), s.project
	scope, stop := s.scope("ask")
	go func() {
		defer stop()
		ctx, cancel := context.WithTimeout(scope, wait)
		defer cancel()
		streams := managementStreamsProgress(ctx, executable, dir, 2*1024*1024, func(line string) {
			s.dispatch(func() {
				// A progress line belongs to the request that produced it; a
				// cancelled run must not paint over the next one.
				if s.shell.AskGeneration != generation {
					return
				}
				s.shell.AskMeta = "fx: " + line
				s.invalidate()
			})
		}, args...)
		outcome := resolveAskOutcome(streams)
		s.dispatch(func() {
			if s.shell.AskGeneration != generation {
				return
			}
			s.shell.AskBusy = false
			s.shell.AskOutput, s.shell.AskMeta, s.shell.AskProblem = outcome.presentation()
			s.invalidate()
		})
	}()
}
