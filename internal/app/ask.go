package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"

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

type fxAskUsage struct {
	InputTokens      *int64 `json:"input_tokens"`
	OutputTokens     *int64 `json:"output_tokens"`
	CacheReadTokens  *int64 `json:"cache_read_tokens"`
	CacheWriteTokens *int64 `json:"cache_write_tokens"`
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
	return strings.Join(parts, " · ")
}

func (c *Coordinator) runAsk(s *Session, req chrome.AskRequest) {
	args, err := askArgs(req)
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
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		text, runErr := managementReadLimit(ctx, executable, dir, 2*1024*1024, args...)
		var result fxAskResult
		parsed := json.Unmarshal([]byte(text), &result) == nil
		mygo.App.WhenReady(func() {
			if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
				return
			}
			if s.shell.AskGeneration != generation {
				return
			}
			s.shell.AskBusy = false
			s.shell.AskProblem = ""
			switch {
			case parsed:
				answer := strings.TrimSpace(result.FinalOutput)
				if answer == "" {
					answer = result.Output
				}
				s.shell.AskOutput = answer
				s.shell.AskMeta = askResultMeta(result)
				if result.Error != "" {
					s.shell.AskProblem = "fx ask: " + result.Error
				} else if strings.TrimSpace(answer) == "" {
					s.shell.AskProblem = "fx returned no assistant output"
				}
			case strings.TrimSpace(text) != "":
				s.shell.AskOutput = text
			default:
				s.shell.AskProblem = "fx returned no output"
			}
			if runErr != nil && s.shell.AskProblem == "" {
				s.shell.AskProblem = runErr.Error()
			}
			s.invalidate()
		})
	}()
}
