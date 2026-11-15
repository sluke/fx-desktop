package app

import (
	"context"
	"encoding/json"
	"fmt"
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
	AuthFailure      string            `json:"auth_failure"`
	Recovery         *fxAskRecovery    `json:"recovery"`
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
	if result.FinalOutput == "" && strings.TrimSpace(result.Output) != "" && (result.Model != "" || result.Recovery != nil || result.Error != "" || result.AuthFailure != "") {
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

func askPresentation(result fxAskResult, parsed bool, text, stderr string, runErr error) (output, meta, problem string) {
	switch {
	case parsed:
		output = strings.TrimSpace(result.FinalOutput)
		if output == "" {
			output = strings.TrimSpace(result.Output)
		}
		meta = askResultMeta(result)
		switch {
		case result.AuthFailure != "":
			problem = "fx ask: " + result.AuthFailure
		case result.Error != "":
			problem = "fx ask: " + result.Error
		case result.Recovery != nil && (result.Recovery.State == "need" || result.Recovery.State == "need_user"):
			problem = "fx ask: " + recoveryLabel(result.Recovery.State, result.Recovery.Attempt, result.Recovery.Message)
		case strings.TrimSpace(output) == "":
			problem = "fx returned no assistant output"
		}
	case strings.TrimSpace(text) != "":
		output = text
	default:
		problem = "fx returned no output"
	}
	if note := strings.TrimSpace(stderr); note != "" {
		if parsed {
			if meta != "" {
				meta += " · "
			}
			meta += note
		} else if problem == "" {
			problem = note
		}
	}
	if runErr != nil && problem == "" {
		problem = runErr.Error()
	}
	return output, meta, problem
}

func (c *Coordinator) cancelAsk(s *ProjectWindow) {
	s.shell.AskGeneration++
	s.cancelFeature("ask")
	s.shell.AskBusy = false
	s.shell.AskProblem = "Cancelled"
	s.invalidate()
}

func (c *Coordinator) runAsk(s *ProjectWindow, req chrome.AskRequest) {
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
		streams := managementStreams(ctx, executable, dir, 2*1024*1024, args...)
		var result fxAskResult
		parsed := json.Unmarshal([]byte(streams.stdout), &result) == nil
		s.dispatch(func() {
			if s.shell.AskGeneration != generation {
				return
			}
			s.shell.AskBusy = false
			s.shell.AskOutput, s.shell.AskMeta, s.shell.AskProblem = askPresentation(result, parsed, streams.stdout, streams.stderr, streams.err)
			s.invalidate()
		})
	}()
}
