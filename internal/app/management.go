package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
)

func managementCommand(action string, state chrome.ManagementState) ([]string, bool, error) {
	value := func(s, label string) (string, error) {
		s = strings.TrimSpace(s)
		if s == "" || strings.HasPrefix(s, "-") || strings.ContainsAny(s, "\x00\r\n") {
			return "", fmt.Errorf("Enter a valid %s", label)
		}
		return s, nil
	}
	provider := strings.TrimSpace(state.Provider)
	switch action {
	case "login", "logout":
		if provider != "" && provider != "vercel" && provider != "codex" && provider != "grok" {
			return nil, false, fmt.Errorf("Choose default, vercel, codex or grok")
		}
		args := []string{action}
		if provider != "" {
			args = append(args, provider)
		}
		return args, action == "logout", nil
	case "setup", "teams":
		return []string{action}, false, nil
	case "mcp-list":
		return []string{"mcp", "list", "--json"}, false, nil
	case "mcp-show":
		name, err := value(state.Name, "server name")
		if err != nil {
			return nil, false, err
		}
		return []string{"mcp", "show", name, "--json"}, false, nil
	case "mcp-add-slack":
		return []string{"mcp", "add", "slack"}, true, nil
	case "mcp-approve-all":
		return []string{"mcp", "approve", "--all"}, true, nil
	case "mcp-add", "mcp-login", "mcp-logout", "mcp-remove", "mcp-approve", "mcp-reject":
		name, err := value(state.Name, "server name")
		if err != nil {
			return nil, false, err
		}
		switch action {
		case "mcp-add":
			args := []string{"mcp", "add"}
			if state.MCPProject {
				args = append(args, "--project")
			}
			if state.Transport == "http" {
				u, err := url.Parse(strings.TrimSpace(state.URL))
				if err != nil || u.Host == "" || u.Scheme != "http" && u.Scheme != "https" {
					return nil, false, fmt.Errorf("Enter an http or https MCP URL")
				}
				headers, err := mcpPairs(state.Headers, "HTTP header")
				if err != nil {
					return nil, false, err
				}
				for _, header := range headers {
					args = append(args, "--header", header)
				}
				// fx 0.0.15+: `mcp add [--project] [--header K=V]... NAME URL`.
				return append(args, name, u.String()), false, nil
			}
			if state.Transport != "" && state.Transport != "stdio" {
				return nil, false, fmt.Errorf("Unsupported MCP transport")
			}
			command, err := value(state.Command, "server executable")
			if err != nil {
				return nil, false, err
			}
			env, err := mcpPairs(state.Environment, "environment variable")
			if err != nil {
				return nil, false, err
			}
			for _, item := range env {
				args = append(args, "--env", item)
			}
			// fx 0.0.15+: stdio arguments follow the `--` separator.
			args = append(args, name, "--", command)
			for _, arg := range strings.Split(strings.ReplaceAll(state.Arguments, "\r\n", "\n"), "\n") {
				if arg != "" {
					if strings.ContainsRune(arg, 0) {
						return nil, false, fmt.Errorf("Arguments cannot contain NUL")
					}
					args = append(args, arg)
				}
			}
			return args, false, nil
		case "mcp-approve":
			return []string{"mcp", "approve", name}, true, nil
		case "mcp-reject":
			return []string{"mcp", "reject", name}, true, nil
		case "mcp-login":
			return []string{"mcp", "login", name}, false, nil
		case "mcp-logout":
			return []string{"mcp", "logout", name}, true, nil
		default:
			return []string{"mcp", "remove", name}, true, nil
		}
	case "provider-set":
		name, err := value(state.ProviderDefault, "provider name")
		if err != nil {
			return nil, false, err
		}
		return []string{"provider", name}, true, nil
	case "upgrade-channel":
		channel := strings.TrimSpace(state.Channel)
		if channel != "stable" && channel != "dev" {
			return nil, false, fmt.Errorf("Choose stable or dev")
		}
		return []string{"upgrade", "--channel", channel}, true, nil
	case "models":
		return []string{"models", "--json"}, false, nil
	case "doctor":
		return []string{"doctor", "--json"}, false, nil
	case "credits":
		return []string{"credits", "--json"}, false, nil
	case "slack-status":
		return []string{"slack", "status", "--json"}, false, nil
	case "slack-install":
		return []string{"slack", "install", "--json"}, true, nil
	case "slack-refresh":
		return []string{"slack", "refresh", "--json"}, true, nil
	case "sessions-convert":
		// fx 0.0.15 removed --sessions-v2; conversion is now the default
		// store and `sessions convert` takes no global store flag.
		return []string{"sessions", "convert", "--json"}, true, nil
	case "workspace-list":
		return []string{"workspace", "list", "--json"}, false, nil
	case "workspace-clear":
		return []string{"workspace", "clear"}, true, nil
	case "workspace-add", "workspace-remove":
		dir := strings.TrimSpace(state.Directory)
		if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
			return nil, false, fmt.Errorf("Choose an absolute directory path")
		}
		if action == "workspace-add" {
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				return nil, false, fmt.Errorf("The additional directory must exist")
			}
		}
		return []string{"workspace", strings.TrimPrefix(action, "workspace-"), filepath.Clean(dir)}, action == "workspace-remove", nil
	case "session-inspect", "session-recover":
		id, err := value(state.SessionID, "session id")
		if err != nil {
			return nil, false, err
		}
		if action == "session-inspect" {
			return []string{"session", "--id", id, "--json"}, false, nil
		}
		// fx 0.0.15 removed `session migrate`; recovery is still available.
		return []string{"session", "recover", "--id", id}, true, nil
	case "launch":
		if state.NoAdditionalDirs && strings.TrimSpace(state.AddDirs) != "" {
			return nil, false, fmt.Errorf("Choose additional directories or ignore saved ones, not both")
		}
		var args []string
		globals, err := launchGlobalArgs(state)
		if err != nil {
			return nil, false, err
		}
		args = append(args, globals...)
		if strings.TrimSpace(state.ProviderOverride) != "" {
			provider, err := value(state.ProviderOverride, "provider name")
			if err != nil {
				return nil, false, err
			}
			args = append(args, "--provider", provider)
		}
		if strings.TrimSpace(state.Model) != "" {
			model, err := value(state.Model, "model id")
			if err != nil {
				return nil, false, err
			}
			args = append(args, "--model", model)
		}
		if strings.TrimSpace(state.Effort) != "" {
			effort, err := value(state.Effort, "reasoning effort")
			if err != nil {
				return nil, false, err
			}
			args = append(args, "--effort", effort)
		}
		switch state.Fast {
		case "", "default":
		case "on":
			args = append(args, "--fast")
		case "off":
			args = append(args, "--no-fast")
		default:
			return nil, false, fmt.Errorf("Choose default, on or off for fast mode")
		}
		switch state.Ultra {
		case "", "default":
		case "on":
			args = append(args, "--ultrafast")
		case "off":
			args = append(args, "--no-ultrafast")
		default:
			return nil, false, fmt.Errorf("Choose default, on or off for Ultra mode")
		}
		if state.NoAdditionalDirs {
			args = append(args, "--no-additional-dirs")
		}
		if strings.TrimSpace(state.LogPath) != "" {
			path := strings.TrimSpace(state.LogPath)
			if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
				return nil, false, fmt.Errorf("ACP log path must be absolute")
			}
			args = append(args, "--log-file", filepath.Clean(path))
		}
		return args, true, nil
	}
	return nil, false, fmt.Errorf("Unsupported operation: %s", action)
}

// mcpPairs validates the repeatable K=V flags fx accepts for MCP headers and
// environment variables. Blank lines are ignored; each nonblank line must
// have a nonempty key. Values stay one argv entry, exactly as typed.
func mcpPairs(raw, label string) ([]string, error) {
	var pairs []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.ContainsRune(line, 0) {
			return nil, fmt.Errorf("%ss cannot contain NUL", label)
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("Each %s must use K=V", strings.ToLower(label))
		}
		pairs = append(pairs, line)
	}
	return pairs, nil
}

// launchGlobalArgs parses the flags fx accepts before its subcommand. Keeping
// them out of the ACP argv is what lets `fx --context-limit ... acp` start
// instead of rejecting the option as an ACP flag.
func launchGlobalArgs(state chrome.ManagementState) ([]string, error) {
	return globalArgsFromForm(state.ContextLimits, state.AddDirs, state.Project)
}

func globalArgsFromForm(contextLimits, addDirs, project string) ([]string, error) {
	var args []string
	for _, line := range strings.Split(strings.ReplaceAll(contextLimits, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("Context limits use NAME=BYTES or NAME=off")
		}
		for _, r := range name {
			if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
				return nil, fmt.Errorf("Context limit names use lowercase letters, digits and underscores")
			}
		}
		if value != "off" {
			if value == "" {
				return nil, fmt.Errorf("Context limit values must be a byte count or off")
			}
			for _, r := range value {
				if r < '0' || r > '9' {
					return nil, fmt.Errorf("Context limit values must be a byte count or off")
				}
			}
		}
		args = append(args, "--context-limit", name+"="+value)
	}
	for _, line := range strings.Split(strings.ReplaceAll(addDirs, "\r\n", "\n"), "\n") {
		dir := strings.TrimSpace(line)
		if dir == "" {
			continue
		}
		if !filepath.IsAbs(dir) || strings.ContainsRune(dir, 0) {
			return nil, fmt.Errorf("Additional directories must be absolute paths")
		}
		dir = filepath.Clean(dir)
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("Additional directory does not exist: %s", dir)
		}
		if project != "" && filepath.Clean(project) == dir {
			return nil, fmt.Errorf("The primary workspace cannot also be an additional directory")
		}
		args = append(args, "--add-dir", dir)
	}
	return args, nil
}

// githubCommand builds `fx pr` or `fx issue`. The context is a single argv
// entry, never a shell string. `--create` is the external publishing step, so
// the UI asks for confirmation before it runs.
func githubCommand(state chrome.GitHubState) ([]string, bool, error) {
	kind := strings.TrimSpace(state.Kind)
	if kind != "pr" && kind != "issue" {
		return nil, false, fmt.Errorf("Choose a pull request or an issue")
	}
	args := []string{kind}
	if state.Auto {
		args = append(args, "--auto")
	}
	if state.Create {
		args = append(args, "--create")
	}
	context := strings.TrimSpace(state.Context)
	if strings.ContainsRune(context, 0) {
		return nil, false, fmt.Errorf("Context cannot contain NUL")
	}
	if context != "" {
		// Keep a context that begins with a dash from becoming an fx flag.
		if strings.HasPrefix(context, "-") {
			args = append(args, "--")
		}
		args = append(args, context)
	}
	return args, state.Create, nil
}

func (c *Coordinator) openManagement(s *Session) {
	if s.shell.Tools.Busy {
		s.shell.Tools.Output = ""
	}
	s.shell.Tools.Open = true
	s.shell.Tools.Busy = false
	s.shell.Tools.Generation++
	s.shell.Tools.Project = s.project
	if s.shell.Tools.Page == "" {
		s.shell.Tools.Page = "Account"
	}
	if s.shell.Tools.Transport == "" {
		s.shell.Tools.Transport = "stdio"
	}
	if s.shell.Tools.Period == "" {
		s.shell.Tools.Period = "month"
	}
	if s.shell.Tools.Ultra == "" {
		s.shell.Tools.Ultra = "default"
	}
	if s.shell.Tools.Fast == "" {
		s.shell.Tools.Fast = "default"
	}
	if s.shell.Tools.Channel == "" {
		s.shell.Tools.Channel = "stable"
	}
	if s.shell.Tools.GitHub.Kind == "" {
		s.shell.Tools.GitHub.Kind = "pr"
		s.shell.Tools.GitHub.Auto = true
	}
	if s.shell.Tools.AllowScope == "" {
		s.shell.Tools.AllowScope = "local"
	}
	if s.shell.Tools.AllowKind == "" {
		s.shell.Tools.AllowKind = "tool"
	}
	if s.shell.Tools.AllowTool == "" {
		s.shell.Tools.AllowTool = "read"
	}
	s.invalidate()
}

func (c *Coordinator) openManagementPage(s *Session, page string) {
	c.openManagement(s)
	s.shell.Tools.Page = page
	s.invalidate()
}

func (c *Coordinator) sessionManagement(s *Session) chrome.ManagementHost {
	return chrome.ManagementHost{
		Run: func(action string) { mygo.App.WhenReady(func() { c.runManagement(s, action) }) },
		PickDirectory: func() {
			c.pickDirectory(s.winSnapshot(), "Choose additional workspace directory", func(dir string) { s.shell.Tools.Directory = dir; s.invalidate() })
		},
		Allowlist: func(action, scope, kind, key, pattern string) {
			mygo.App.WhenReady(func() { c.runAllowlist(s, action, scope, kind, key, pattern) })
		},
		OpenPath: func(path string) {
			if path == "" {
				return
			}
			if _, err := os.Stat(path); err != nil {
				// Opening a missing workspace skills folder should still give
				// the user a useful place: the project root.
				path = filepath.Dir(filepath.Dir(path))
			}
			_ = mygo.Shell.OpenPath(path)
		},
		Resume: func(id, workspace string) {
			mygo.App.WhenReady(func() {
				if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
					return
				}
				if workspace == "" {
					workspace = s.project
				}
				if st, err := os.Stat(workspace); err != nil || !st.IsDir() {
					s.shell.Tools.Output = "The saved workspace no longer exists: " + workspace
					s.shell.History.ResumeProblem = s.shell.Tools.Output
					s.invalidate()
					return
				}
				if target := c.sessionFor(workspace); target != nil && target.chat != nil && target.chat.status().SessionID == id {
					s.shell.History.Open = false
					s.shell.Tools.Open = false
					if w := target.winSnapshot(); w != nil && !w.IsDestroyed() {
						w.Show()
						w.Focus()
					}
					s.invalidate()
					return
				}
				if target := c.sessionFor(workspace); target != nil && target.chat != nil && (target.chat.status().Busy || strings.TrimSpace(target.chatUI.Draft) != "") {
					result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: s.winSnapshot(), Type: mygo.MessageQuestion, Title: "Resume saved session", Message: "The target project has an active turn or an unsent draft. Close its current connection and resume this saved session?", Buttons: []string{"Cancel", "Resume"}, DefaultButton: 0, CancelButton: 0})
					if err != nil || result.Button != 1 {
						return
					}
				}
				if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
					return
				}
				s.shell.Tools.Open = false
				s.shell.History.Open = false
				c.openProjectWithFlags(workspace, "--resume", id)
			})
		},
	}
}

func (c *Coordinator) runManagement(s *Session, action string) {
	if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
		return
	}
	state := &s.shell.Tools
	if state.Busy {
		return
	}
	if action == "sessions" {
		c.openHistory(s)
		return
	}
	if action == "github-run" {
		c.runGitHubAction(s)
		return
	}
	if action == "models-clear" {
		state.ModelQuery = ""
		s.invalidate()
		return
	}
	if action == "allowlist-load" {
		c.runAllowlist(s, "load", "", "", "", "")
		return
	}
	if action == "skills-load" {
		c.runSkills(s)
		return
	}
	if action == "details" || action == "doctor" || action == "credits" || action == "slack-status" || action == "models" {
		c.readManagement(s, action, nil)
		return
	}
	args, confirm, err := managementCommand(action, *state)
	if err != nil {
		state.Output = err.Error()
		s.invalidate()
		return
	}
	switch action {
	case "mcp-list", "mcp-show", "workspace-list", "session-inspect":
		c.readManagement(s, action, args)
		return
	}
	if action == "workspace-add" && filepath.Clean(state.Directory) == filepath.Clean(s.project) {
		state.Output = "This is already the primary workspace."
		s.invalidate()
		return
	}
	if confirm {
		message := "Run fx " + strings.Join(args, " ") + "?"
		if action == "launch" {
			message = "Start a new conversation with these launch options? The current connection will close; fx keeps its saved history."
		}
		if action == "sessions-convert" {
			message = "Convert every session saved in the previous format to the v2 store? The conversion is additive and fx leaves sessions it cannot read in place."
		}
		if action == "upgrade-channel" {
			message = "Download and install the latest fx release from the " + state.Channel + " channel?"
		}
		if action == "mcp-add-slack" || action == "slack-install" {
			message = "Configure Slack through fx? fx opens the Slack authorization flow in your browser."
		}
		result, err := mygo.Dialog.Message(mygo.MessageOptions{Parent: s.winSnapshot(), Type: mygo.MessageQuestion, Title: "Confirm workspace action", Message: message, Buttons: []string{"Cancel", "Continue"}, DefaultButton: 0, CancelButton: 0})
		if err != nil || result.Button != 1 {
			return
		}
	}
	if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
		return
	}
	if action == "launch" {
		state.Open = false
		s.replaceConversation(args)
		return
	}
	// Interactive account setup still belongs to the in-window console, but
	// every noninteractive management operation reports through the native
	// page instead of leaving the sheet for a terminal.
	switch action {
	case "login", "setup", "teams":
		run, err := c.startConsoleRun(s, "fx "+strings.Join(args, " "), args...)
		if err != nil {
			state.Output = err.Error()
			s.invalidate()
			return
		}
		state.Open = false
		run.done = func(code int) {
			if code == 0 {
				go s.refreshFxInfo()
			}
		}
		s.invalidate()
		return
	}
	c.runManagementJob(s, action, args)
}

func managementJobTimeout(action string) time.Duration {
	switch action {
	case "upgrade-channel":
		return 10 * time.Minute
	case "slack-install", "slack-refresh", "mcp-add-slack":
		return 5 * time.Minute
	case "sessions-convert", "session-recover":
		return 2 * time.Minute
	default:
		return 60 * time.Second
	}
}

// managementJobSummary turns machine-readable mutation output into a native
// sentence where fx provides enough structure. Everything else stays as fx's
// own text, which is more honest than guessing at a schema.
func managementJobSummary(action, text string, runErr error) string {
	if runErr != nil {
		if strings.TrimSpace(text) == "" {
			return runErr.Error()
		}
		return text + "\n\n" + runErr.Error()
	}
	switch action {
	case "sessions-convert":
		var out FxSessionsConvert
		if json.Unmarshal([]byte(text), &out) == nil {
			summary := fmt.Sprintf("Converted %d session(s). Busy %d, unreadable %d.", out.Converted, out.Busy, out.Unreadable)
			if len(out.Skipped) > 0 {
				summary += fmt.Sprintf(" Skipped %d.", len(out.Skipped))
			}
			if len(out.LeftOut) > 0 {
				summary += fmt.Sprintf(" Left out %d.", len(out.LeftOut))
			}
			if len(out.Kept) > 0 {
				summary += fmt.Sprintf(" Kept %d.", len(out.Kept))
			}
			return summary
		}
	case "provider-set":
		return "Default provider saved by fx. New conversations use it."
	case "upgrade-channel":
		return "fx update finished.\n\n" + text
	case "mcp-show":
		return text
	case "mcp-add-slack":
		return "Slack MCP configuration finished.\n\n" + text
	case "slack-install":
		return "Slack installation finished.\n\n" + text
	case "slack-refresh":
		return "Slack tokens refreshed.\n\n" + text
	case "session-recover":
		return "Session recovery finished.\n\n" + text
	case "mcp-approve-all":
		return "Project server approvals updated by fx.\n\n" + text
	}
	if strings.HasPrefix(action, "mcp-") || strings.HasPrefix(action, "workspace-") {
		return "Saved by fx. Start a new conversation to load changed servers or additional directories.\n\n" + text
	}
	return text
}

func (c *Coordinator) runGitHubAction(s *Session) {
	state := &s.shell.Tools
	if state.Busy || state.GitHub.Busy {
		return
	}
	args, confirm, err := githubCommand(state.GitHub)
	if err != nil {
		state.GitHub.Problem = err.Error()
		s.invalidate()
		return
	}
	if confirm {
		kind := "pull request"
		if state.GitHub.Kind == "issue" {
			kind = "issue"
		}
		result, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:        s.winSnapshot(),
			Type:          mygo.MessageWarning,
			Title:         "Publish " + kind,
			Message:       "Publish this " + kind + " to GitHub? This creates a real GitHub item through fx and GitHub CLI.",
			Buttons:       []string{"Cancel", "Publish"},
			DefaultButton: 0,
			CancelButton:  0,
		})
		if err != nil || result.Button != 1 {
			return
		}
	}
	state.GitHub.Generation++
	generation := state.GitHub.Generation
	state.GitHub.Busy = true
	state.GitHub.Problem = ""
	state.GitHub.Output = "Running fx " + strings.Join(args, " ") + "…"
	s.invalidate()
	executable, dir := c.fxPathSnapshot(), s.project
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		text, runErr := managementRead(ctx, executable, dir, args...)
		mygo.App.WhenReady(func() {
			if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
				return
			}
			if state.GitHub.Generation != generation {
				return
			}
			state.GitHub.Busy = false
			state.GitHub.Output = text
			if runErr != nil {
				if strings.TrimSpace(text) == "" {
					state.GitHub.Problem = runErr.Error()
				} else {
					state.GitHub.Problem = runErr.Error()
				}
			}
			s.invalidate()
			if runErr == nil {
				go s.refreshFxInfo()
			}
		})
	}()
}

func (c *Coordinator) runManagementJob(s *Session, action string, args []string) {
	state := &s.shell.Tools
	if state.Busy {
		return
	}
	state.Generation++
	generation := state.Generation
	state.Busy = true
	state.Output = "Running fx " + strings.Join(args, " ") + "…"
	s.invalidate()
	executable, dir := c.fxPathSnapshot(), s.project
	timeout := managementJobTimeout(action)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		text, runErr := managementRead(ctx, executable, dir, args...)
		mygo.App.WhenReady(func() {
			if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
				return
			}
			state := &s.shell.Tools
			if state.Generation != generation || !state.Open {
				return
			}
			state.Busy = false
			state.Output = managementJobSummary(action, text, runErr)
			s.invalidate()
			if runErr == nil {
				go s.refreshFxInfo()
			}
		})
	}()
}

type managementOutput struct {
	buffer    bytes.Buffer
	truncated bool
	limit     int
}

func (b *managementOutput) Write(p []byte) (int, error) {
	n := len(p)
	limit := b.limit
	if limit <= 0 {
		limit = 64 * 1024
	}
	room := limit - b.buffer.Len()
	if room < len(p) {
		b.truncated = true
		p = p[:max(0, room)]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func managementRead(ctx context.Context, executable, dir string, args ...string) (string, error) {
	return managementReadLimit(ctx, executable, dir, 64*1024, args...)
}

// managementReadLimit is managementRead with an explicit cap. Quick Ask uses
// a larger cap because a useful one-shot answer can exceed the management
// panel's 64 KiB budget.
func managementReadLimit(ctx context.Context, executable, dir string, limit int, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 100 * time.Millisecond
	var out managementOutput
	out.limit = limit
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := strings.TrimSpace(out.buffer.String())
	if out.truncated {
		text += fmt.Sprintf("\n[Output truncated at %d KiB]", limit/1024)
	}
	if err != nil {
		return text, fmt.Errorf("fx %s: %w", strings.Join(args, " "), err)
	}
	var formatted bytes.Buffer
	if json.Indent(&formatted, []byte(text), "", "  ") == nil {
		text = formatted.String()
	}
	return text, nil
}

type managementReadResult struct {
	output         string
	problem        error
	details        []chrome.DetailSection
	doctor         []chrome.DoctorCheck
	credits        string
	slack          chrome.SlackStatus
	models         []chrome.ModelInfo
	modelsProblem  string
	mcps           []chrome.MCPServerInfo
	mcpDetail      *chrome.MCPServerDetail
	mcpProblem     string
	session        *chrome.SessionDetail
	sessionProblem string
}

func apiText(raw json.RawMessage) string {
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

func detailsSections(status *FxStatus, permissions *FxPermissions, usage *FxUsage, failures []string) []chrome.DetailSection {
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
		if len(status.MCP.ConfigurationIssues) > 0 || rawPresent(status.MCP.InspectionError) {
			var lines []string
			lines = append(lines, status.MCP.ConfigurationIssues...)
			if rawPresent(status.MCP.InspectionError) {
				lines = append(lines, "Inspection: "+apiText(status.MCP.InspectionError))
			}
			sections = append(sections, chrome.DetailSection{Title: "MCP issues", Text: strings.Join(lines, "\n")})
		}
	}
	if permissions != nil {
		text := ""
		if len(permissions.Rules) > 0 {
			var lines []string
			for i, rule := range permissions.Rules {
				lines = append(lines, fmt.Sprintf("%d. %s", i+1, apiText(rule)))
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
				grants = append(grants, fmt.Sprintf("%d. %s", i+1, apiText(grant)))
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
				fact("Total tokens", compactCount(usage.Totals.TotalTokens)),
				fact("Input", compactCount(usage.Totals.InputTokens)),
				fact("Output", compactCount(usage.Totals.OutputTokens)),
				fact("Reasoning", compactCount(usage.Totals.ReasoningTokens)),
				fact("Cache read", compactCount(usage.Totals.CacheReadTokens)),
				fact("Cache write", compactCount(usage.Totals.CacheWriteTokens)),
				fact("Requests", fmt.Sprintf("%d", usage.Totals.RequestCount)),
				fact("Spend", fmt.Sprintf("$%.2f", usage.Totals.Spend)),
			},
		})
		var detail []string
		if rawPresent(usage.Budget) {
			detail = append(detail, "Budget: "+apiText(usage.Budget))
		}
		for i, model := range usage.Models {
			if rawPresent(model) {
				detail = append(detail, fmt.Sprintf("Model %d: %s", i+1, apiText(model)))
			}
		}
		if rawPresent(usage.Codex) {
			detail = append(detail, "Codex: "+apiText(usage.Codex))
		}
		if rawPresent(usage.Grok) {
			detail = append(detail, "Grok: "+apiText(usage.Grok))
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

func modeIDs(modes []FxMode) string {
	ids := make([]string, 0, len(modes))
	for _, mode := range modes {
		if mode.ID != "" {
			ids = append(ids, mode.ID)
		}
	}
	return strings.Join(ids, ", ")
}

func rawPresent(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

func unixMSLabel(ms *int64) string {
	if ms == nil || *ms <= 0 {
		return ""
	}
	return time.UnixMilli(*ms).Format("2006-01-02 15:04 MST")
}

// sessionDetailView turns `fx session --json` into the compact, read-only
// rows the native Sessions page can render. Long assistant text is trimmed
// here so a large saved conversation cannot dominate one frame.
func sessionDetailView(d *FxSessionDetail) *chrome.SessionDetail {
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
			User:      truncateText(turn.User.Text, 2000),
			Images:    len(turn.User.Images),
			Assistant: truncateText(turn.Assistant, 2000),
			Tools:     turn.CompletedToolNames,
		})
	}
	return view
}

func slackStatusView(s *FxSlackStatus) chrome.SlackStatus {
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

func (c *Coordinator) readManagement(s *Session, action string, args []string) {
	state := &s.shell.Tools
	period := state.Period
	if period == "" {
		period = "month"
	}
	if action == "details" && period != "today" && period != "7d" && period != "month" {
		state.Output = "Choose today, 7d or month"
		s.invalidate()
		return
	}
	state.Generation++
	generation := state.Generation
	state.Busy = true
	state.Output = "Reading fx…"
	s.invalidate()
	executable, dir := c.fxPathSnapshot(), s.project
	timeout := fxTimeout
	if action == "mcp-show" {
		// `mcp show` connects to the server to report status and tools.
		timeout = 60 * time.Second
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		result := managementReadResult{}
		switch action {
		case "models":
			text, err := managementRead(ctx, executable, dir, "models", "--json")
			var models FxModels
			if err == nil && json.Unmarshal([]byte(text), &models) == nil {
				for _, model := range models.Models {
					result.models = append(result.models, chrome.ModelInfo{ID: model.ID, Source: model.Source})
				}
				if len(result.models) == 0 {
					for _, id := range models.IDs {
						result.models = append(result.models, chrome.ModelInfo{ID: id})
					}
				}
				result.output = fmt.Sprintf("%d model(s) available", len(result.models))
				if models.MoreCount > 0 {
					result.output += fmt.Sprintf(" · %d more hidden", models.MoreCount)
				}
				if models.PrivateModelsHidden {
					result.output += " · private models hidden"
				}
			} else {
				result.output, result.problem = text, err
				if err != nil {
					result.modelsProblem = valueOr(text, err.Error())
				} else {
					result.modelsProblem = valueOr(text, "fx returned unreadable model JSON")
				}
			}
		case "mcp-list":
			text, err := managementRead(ctx, executable, dir, args...)
			var list FxMCPList
			if err == nil && json.Unmarshal([]byte(text), &list) == nil {
				for _, server := range list.Servers {
					result.mcps = append(result.mcps, chrome.MCPServerInfo{
						Name: server.Name, Source: server.Source, File: server.File,
						Transport: server.Transport, Status: server.Status, Error: server.Error,
					})
				}
				result.output = fmt.Sprintf("%d MCP server(s)", len(result.mcps))
			} else {
				result.output, result.problem = text, err
				result.mcpProblem = valueOr(text, "fx returned unreadable MCP JSON")
			}
		case "mcp-show":
			text, err := managementRead(ctx, executable, dir, args...)
			var show FxMCPShow
			if err == nil && json.Unmarshal([]byte(text), &show) == nil {
				detail := &chrome.MCPServerDetail{
					MCPServerInfo: chrome.MCPServerInfo{
						Name: show.Name, Source: show.Source, File: show.File,
						Transport: show.Transport, Status: show.Status, Error: show.Error,
					},
					Version: show.Version, URL: show.URL, Command: show.Command,
				}
				for _, tool := range show.Tools {
					detail.Tools = append(detail.Tools, chrome.MCPToolInfo{Name: tool.Name, Description: tool.Description})
				}
				result.mcpDetail = detail
				result.output = fmt.Sprintf("%s · %s", valueOr(show.Name, "MCP server"), valueOr(show.Status, "unknown status"))
			} else {
				result.output, result.problem = text, err
				result.mcpProblem = valueOr(text, "fx returned unreadable MCP JSON")
			}
		case "session-inspect":
			// A saved conversation can be large; the native view only keeps
			// excerpts, but the JSON must parse before those can be built.
			text, err := managementReadLimit(ctx, executable, dir, 512*1024, args...)
			var detail FxSessionDetail
			if err == nil && json.Unmarshal([]byte(text), &detail) == nil {
				result.session = sessionDetailView(&detail)
				result.output = fmt.Sprintf("Session %s · %d turn(s)", detail.ID, len(detail.History))
			} else {
				result.output, result.problem = text, err
				result.sessionProblem = valueOr(text, "fx returned unreadable session JSON")
			}
		case "details":
			var failures []string
			var status FxStatus
			var permissions FxPermissions
			var usage FxUsage
			if text, err := managementRead(ctx, executable, dir, "status", "--json"); err != nil {
				failures = append(failures, "Status: "+valueOr(text, err.Error()))
			} else if json.Unmarshal([]byte(text), &status) != nil {
				failures = append(failures, "Status: fx returned unreadable JSON")
			}
			if text, err := managementRead(ctx, executable, dir, "permissions", "--json"); err != nil {
				failures = append(failures, "Permissions: "+valueOr(text, err.Error()))
			} else if json.Unmarshal([]byte(text), &permissions) != nil {
				failures = append(failures, "Permissions: fx returned unreadable JSON")
			}
			if text, err := managementRead(ctx, executable, dir, "usage", "--period", period, "--json"); err != nil {
				failures = append(failures, "Usage: "+valueOr(text, err.Error()))
			} else if json.Unmarshal([]byte(text), &usage) != nil {
				failures = append(failures, "Usage: fx returned unreadable JSON")
			}
			var statusPtr *FxStatus
			var permissionsPtr *FxPermissions
			var usagePtr *FxUsage
			if status.Model != "" || status.Workspace != "" {
				statusPtr = &status
			}
			if permissions.Mode != "" || permissions.Rules != nil {
				permissionsPtr = &permissions
			}
			if usage.Period != "" || usage.Source != "" {
				usagePtr = &usage
			}
			result.details = detailsSections(statusPtr, permissionsPtr, usagePtr, failures)
		case "doctor":
			text, err := managementRead(ctx, executable, dir, "doctor", "--json")
			var doctor FxDoctor
			if err == nil && json.Unmarshal([]byte(text), &doctor) == nil {
				for _, check := range doctor.Checks {
					result.doctor = append(result.doctor, chrome.DoctorCheck{Name: check.Name, Status: check.Status, Detail: check.Detail})
				}
				for _, issue := range doctor.MCP.ConfigurationIssues {
					result.doctor = append(result.doctor, chrome.DoctorCheck{Name: "mcp", Status: "warn", Detail: issue})
				}
				if rawPresent(doctor.MCP.InspectionError) {
					result.doctor = append(result.doctor, chrome.DoctorCheck{Name: "mcp", Status: "warn", Detail: apiText(doctor.MCP.InspectionError)})
				}
				result.output = fmt.Sprintf("%d ok · %d warning · %d failed", doctor.OKCount, doctor.WarnCount, doctor.FailCount)
			} else {
				result.output, result.problem = text, err
			}
		case "credits":
			result.credits, result.problem = managementRead(ctx, executable, dir, "credits", "--json")
		case "slack-status":
			text, err := managementRead(ctx, executable, dir, "slack", "status", "--json")
			var slack FxSlackStatus
			if err == nil && json.Unmarshal([]byte(text), &slack) == nil {
				result.slack = slackStatusView(&slack)
				result.output = "Slack status read from fx."
			} else {
				result.problem = err
				result.output = text
			}
		default:
			result.output, result.problem = managementRead(ctx, executable, dir, args...)
		}
		if result.problem != nil && action != "details" && action != "doctor" && action != "slack-status" {
			if result.output != "" {
				result.output += "\n"
			}
			result.output += result.problem.Error()
		}
		mygo.App.WhenReady(func() {
			if w := s.winSnapshot(); w == nil || w.IsDestroyed() {
				return
			}
			state := &s.shell.Tools
			if state.Generation != generation || !state.Open {
				return
			}
			state.Busy = false
			if result.output != "" {
				state.Output = result.output
			}
			if action == "details" {
				state.Details = result.details
				if result.output == "" {
					state.Output = ""
				}
			}
			if action == "doctor" {
				state.Doctor = result.doctor
			}
			if action == "credits" {
				state.Credits = result.credits
			}
			if action == "slack-status" {
				state.Slack = result.slack
				state.SlackProblem = ""
				if result.problem != nil {
					state.SlackProblem = result.problem.Error()
				}
			}
			if action == "models" {
				state.Models = result.models
				state.ModelsProblem = result.modelsProblem
			}
			if action == "mcp-list" {
				state.MCPServers = result.mcps
				state.MCPProblem = result.mcpProblem
			}
			if action == "mcp-show" {
				state.MCPDetail = result.mcpDetail
				state.MCPProblem = result.mcpProblem
			}
			if action == "session-inspect" {
				state.SessionView = result.session
				state.SessionProblem = result.sessionProblem
			}
			s.invalidate()
		})
	}()
}
