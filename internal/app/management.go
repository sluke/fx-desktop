package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/app/presenter"
	"fx-desktop/internal/management"
)

// The management request rules live in the management package. These
// adapters keep the app's call sites and the panel's form type, and are the
// only place the two meet.
func managementCommand(action string, state chrome.ManagementState) ([]string, bool, error) {
	return management.Command(action, state.Form)
}

// githubCommand builds `fx pr` or `fx issue` from the panel's form.
func githubCommand(state chrome.GitHubState) ([]string, bool, error) {
	return management.GitHub(management.GitHubForm{Kind: state.Kind, Context: state.Context, Auto: state.Auto, Create: state.Create})
}

// globalArgsFromForm parses the flags fx accepts before its subcommand.
func globalArgsFromForm(contextLimits, addDirs, project string) ([]string, error) {
	return management.GlobalArgs(contextLimits, addDirs, project)
}

func (c *Coordinator) openManagement(s *ProjectWindow) {
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

func (c *Coordinator) openManagementPage(s *ProjectWindow, page string) {
	c.openManagement(s)
	s.shell.Tools.Page = page
	s.invalidate()
}

func (c *Coordinator) sessionManagement(s *ProjectWindow) chrome.ManagementHost {
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
			s.dispatch(func() {
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

func (c *Coordinator) runManagement(s *ProjectWindow, action string) {
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

func managementJobTimeout(action string) time.Duration { return management.JobTimeout(action) }

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

func (c *Coordinator) runGitHubAction(s *ProjectWindow) {
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
	scope, stop := s.scope("github")
	go func() {
		defer stop()
		ctx, cancel := context.WithTimeout(scope, 10*time.Minute)
		defer cancel()
		text, runErr := managementRead(ctx, executable, dir, args...)
		s.dispatch(func() {
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

func (c *Coordinator) runManagementJob(s *ProjectWindow, action string, args []string) {
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
	scope, stop := s.scope("management")
	go func() {
		defer stop()
		ctx, cancel := context.WithTimeout(scope, timeout)
		defer cancel()
		text, runErr := managementRead(ctx, executable, dir, args...)
		s.dispatch(func() {
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

// The management pages' models are the presenter's: it maps fx's JSON into
// rows, sections and labels. These adapters keep the app's names for them.
func detailsSections(status *FxStatus, permissions *FxPermissions, usage *FxUsage, failures []string) []chrome.DetailSection {
	return presenter.Details(status, permissions, usage, failures)
}

func sessionDetailView(d *FxSessionDetail) *chrome.SessionDetail { return presenter.SessionDetail(d) }

// valueOr keeps a blank form value from rendering as an empty cell.
func valueOr(value, fallback string) string { return presenter.ValueOr(value, fallback) }

// apiText renders a JSON value as compact text.
func apiText(raw json.RawMessage) string { return presenter.APIText(raw) }

// rawPresent reports whether fx sent a field rather than null.
func rawPresent(raw json.RawMessage) bool { return presenter.RawPresent(raw) }

func slackStatusView(s *FxSlackStatus) chrome.SlackStatus { return presenter.SlackStatus(s) }

func (c *Coordinator) readManagement(s *ProjectWindow, action string, args []string) {
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
	scope, stop := s.scope("management-read")
	go func() {
		defer stop()
		ctx, cancel := context.WithTimeout(scope, timeout)
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
		s.dispatch(func() {
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
