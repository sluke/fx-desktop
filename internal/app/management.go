package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/app/presenter"
	"fx-desktop/internal/fxcli"
	"fx-desktop/internal/fxhome"
	"fx-desktop/internal/fxlive"
	"fx-desktop/internal/fxprofile"
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
		Close: func() {
			s.cancelFeature("skills")
			s.cancelFeature("management-read")
		},
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
				if c.quitting() {
					s.shell.Tools.Output = errQuitting.Error()
					s.invalidate()
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

func (c *Coordinator) runManagement(s *ProjectWindow, action string) {
	if c.quitting() {
		s.shell.Tools.Output = errQuitting.Error()
		s.invalidate()
		return
	}
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
	if action == "profiles" {
		c.readManagement(s, action, nil)
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
	case "mcp-list", "mcp-show", "workspace-list", "session-inspect", "sessions-live", "session-tail":
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

// managementOutput bounds one captured stream. Each stream keeps its own
// budget and its own truncation flag: stdout is the payload, stderr is the
// diagnostic, and concatenating them would make a warning look like an
// answer (A4).
//
// Capture and progress are separate budgets (O5). The capture is a bounded
// byte buffer; progress is a bounded *line* buffer that only ever sees the
// bytes of the newest Write. Once the capture is full, the pipe is still
// drained and status lines keep arriving — a full capture must not freeze
// the UI at an old retry message.
type managementOutput struct {
	buffer    bytes.Buffer
	truncated bool
	limit     int

	mu sync.Mutex
	// emit, when set, receives the newest non-empty line at most once per
	// progressInterval. It lets a long command report progress without one
	// layout pass per write.
	emit func(string)
	// last is the last publish time; current is the bounded partial line
	// (complete runes only); incomplete holds at most one rune split across
	// two Writes. latest is the newest completed line; pending is the newest
	// line not yet published; published is the last line handed to emit.
	last          time.Time
	current       []byte
	incomplete    [utf8.UTFMax]byte
	incompleteLen int
	lastLine      []byte
	latest        string
	pending       string
	published     string
}

const (
	progressInterval = 250 * time.Millisecond
	// maxProgressLineBytes bounds both the partial-line buffer and every
	// string handed to emit. A 16 MiB line without a newline costs one
	// bounded tail, not one unbounded allocation.
	maxProgressLineBytes = 4 << 10
)

// Write returns the full length of p: the pipe is always drained, even when
// the capture budget is spent. Progress parsing sees the whole chunk before
// the capture is truncated, so the cap cannot hide newer status (O5).
func (b *managementOutput) Write(p []byte) (int, error) {
	n := len(p)
	limit := b.limit
	if limit <= 0 {
		limit = 64 * 1024
	}
	b.mu.Lock()
	room := limit - b.buffer.Len()
	captured := p
	if room < len(p) {
		b.truncated = true
		captured = p[:max(0, room)]
	}
	_, _ = b.buffer.Write(captured)

	emit := b.emit
	var fire string
	if emit != nil {
		b.feed(p)
		// pending must always describe the newest unpublished fact (P6a).
		// When the newest line returns to what was already published, an
		// older throttled line is stale and must be withdrawn.
		switch {
		case b.latest == b.published:
			b.pending = ""
		case b.latest == "":
			// No completed line yet; keep the existing state.
		case time.Since(b.last) >= progressInterval:
			fire = b.latest
			b.published = fire
			b.last = time.Now()
			b.pending = ""
		default:
			// Keep only the newest pending line: a throttled run must
			// never queue one callback per status line.
			b.pending = b.latest
		}
	}
	b.mu.Unlock()
	if fire != "" {
		emit(fire)
	}
	return n, nil
}

// feed consumes only the new bytes, keeping at most one bounded line in
// memory. It handles \n, \r\n and lone \r (progress bars), cross-chunk lines
// and UTF-8 runes split between Writes (P6b): current holds complete runes
// only, while a partially received rune lives in incomplete.
func (b *managementOutput) feed(data []byte) {
	// Only the newest line of a chunk can be published. Keep it as bytes and
	// allocate one string per Write, not one per status line (O5).
	have := false
	for len(data) > 0 {
		i := indexLineEnd(data)
		if i < 0 {
			b.appendSegment(data)
			break
		}
		delimiter := data[i]
		b.appendSegment(data[:i])
		// A rune left unfinished at a line boundary is invalid source data,
		// not a character to publish. current is already valid UTF-8.
		b.incompleteLen = 0
		if trimmed := bytes.TrimSpace(b.current); len(trimmed) > 0 {
			b.lastLine = append(b.lastLine[:0], trimmed...)
			have = true
		}
		b.current = b.current[:0]
		data = data[i+1:]
		if delimiter == '\r' && len(data) > 0 && data[0] == '\n' {
			data = data[1:]
		}
	}
	if have {
		b.latest = string(b.lastLine)
	}
}

// indexLineEnd finds \n or \r without allocating. IndexAny takes a string
// and its general path can allocate per call, which a 2048-line chunk would
// turn into one allocation per status line (O5).
func indexLineEnd(data []byte) int {
	for i, c := range data {
		if c == '\n' || c == '\r' {
			return i
		}
	}
	return -1
}

// appendSegment adds one line fragment. The common all-ASCII case is a single
// bounded append. Otherwise the longest prefix of complete runes is appended
// in bulk, and only the trailing unfinished rune is carried to the next
// Write. Invalid source bytes become U+FFFD rather than leaking into the UI.
func (b *managementOutput) appendSegment(data []byte) {
	if b.incompleteLen == 0 && allASCII(data) {
		b.current = append(b.current, data...)
		b.trimCurrent()
		return
	}
	for len(data) > 0 {
		if b.incompleteLen > 0 {
			total := runeLength(b.incomplete[0])
			if total <= b.incompleteLen {
				b.appendReplacement()
				b.incompleteLen = 0
				continue
			}
			need := total - b.incompleteLen
			if len(data) < need {
				copy(b.incomplete[b.incompleteLen:], data)
				b.incompleteLen += len(data)
				return
			}
			var buf [utf8.UTFMax]byte
			copy(buf[:], b.incomplete[:b.incompleteLen])
			copy(buf[b.incompleteLen:], data[:need])
			r, size := utf8.DecodeRune(buf[:total])
			b.incompleteLen = 0
			if r == utf8.RuneError && size == 1 {
				// The carried bytes never formed a rune; do not consume the
				// new bytes, they belong to the next character.
				b.appendReplacement()
				continue
			}
			b.current = append(b.current, buf[:total]...)
			b.trimCurrent()
			data = data[need:]
			continue
		}
		if i := validRunePrefix(data); i > 0 {
			b.current = append(b.current, data[:i]...)
			b.trimCurrent()
			data = data[i:]
			continue
		}
		if !utf8.FullRune(data) {
			b.incompleteLen = copy(b.incomplete[:], data)
			return
		}
		b.appendReplacement()
		data = data[1:]
	}
}

func allASCII(data []byte) bool {
	for _, c := range data {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// validRunePrefix returns the length of the leading sequence of complete,
// valid runes. It stops before an unfinished or invalid rune so the caller
// can deal with that one explicitly.
func validRunePrefix(data []byte) int {
	i := 0
	for i < len(data) {
		if !utf8.FullRune(data[i:]) {
			break
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			break
		}
		i += size
	}
	return i
}

func runeLength(lead byte) int {
	switch {
	case lead < utf8.RuneSelf:
		return 1
	case lead&0xE0 == 0xC0:
		return 2
	case lead&0xF0 == 0xE0:
		return 3
	case lead&0xF8 == 0xF0:
		return 4
	default:
		return 1
	}
}

func (b *managementOutput) appendReplacement() {
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], utf8.RuneError)
	b.current = append(b.current, buf[:n]...)
	b.trimCurrent()
}

// trimCurrent drops whole runes from the head until the tail fits the budget.
// It runs once per appended segment, not once per byte, so a 16 MiB line is
// still linear work with a bounded live buffer (O5/P6b).
func (b *managementOutput) trimCurrent() {
	if len(b.current) <= maxProgressLineBytes {
		return
	}
	cut := len(b.current) - maxProgressLineBytes
	for cut < len(b.current) && !utf8.RuneStart(b.current[cut]) {
		cut++
	}
	if cut >= len(b.current) {
		b.current = b.current[:0]
		return
	}
	b.current = append(b.current[:0], b.current[cut:]...)
}

// flushPending publishes the newest status at the end of a run, including a
// final line that never ended with a newline. It starts no timers or
// goroutines and is idempotent. A callback string is a bounded copy, never a
// window onto the capture's backing buffer.
func (b *managementOutput) flushPending() string {
	b.mu.Lock()
	emit := b.emit
	if emit != nil && len(b.current) > 0 {
		// current is valid UTF-8 and already bounded; a rune still split at
		// the end of the stream is not a fact we can publish.
		if text := strings.TrimSpace(string(b.current)); text != "" {
			b.latest = text
			b.pending = text
		}
		b.current = b.current[:0]
		b.incompleteLen = 0
	}
	// pending must describe the newest unpublished fact (P6a).
	if b.pending != "" && b.pending != b.latest {
		b.pending = b.latest
	}
	if b.pending == b.published {
		b.pending = ""
	}
	line := b.pending
	if emit == nil || line == "" {
		b.pending = ""
		b.mu.Unlock()
		return ""
	}
	b.published = line
	b.pending = ""
	b.last = time.Now()
	b.mu.Unlock()
	emit(line)
	return line
}

// liveSummary says what the read established: fx's own count when it sent
// one, otherwise that the rows are merely what was listed, plus any entries
// that could not be read. A missing count must not become zero.
func liveSummary(report fxlive.Report) string {
	var b strings.Builder
	if report.Count != nil {
		fmt.Fprintf(&b, "%d live session(s)", *report.Count)
	} else {
		fmt.Fprintf(&b, "%d live session(s) listed · count not reported", len(report.Agents))
	}
	if report.Skipped > 0 {
		fmt.Fprintf(&b, " · %d not shown", report.Skipped)
	}
	if report.Partial {
		fmt.Fprintf(&b, " · list is partial")
	}
	if report.Ambiguous > 0 {
		fmt.Fprintf(&b, " · %d ambiguous (cannot be queried)", report.Ambiguous)
	}
	return b.String()
}

func liveAgents(in []fxlive.Agent) []chrome.LiveAgent {
	var out []chrome.LiveAgent
	var walk func(fxlive.Agent, int)
	walk = func(agent fxlive.Agent, depth int) {
		out = append(out, chrome.LiveAgent{
			CLIRef: agent.CLIRef, UIKey: agent.UIKey, Name: agent.Name, Title: agent.Title, State: agent.State,
			Model: agent.Model, Profile: agent.Profile, Workspace: agent.Workspace, Depth: depth,
			Ambiguous: agent.Ambiguous, Note: agent.Note,
		})
		for _, child := range agent.Agents {
			walk(child, depth+1)
		}
	}
	for _, agent := range in {
		walk(agent, 0)
	}
	return out
}

func decodeDoctor(text string, err error) (FxDoctor, bool) {
	return fxcli.ParseDoctor([]byte(text), err)
}

func managementRead(ctx context.Context, executable, dir string, args ...string) (string, error) {
	return managementReadLimit(ctx, executable, dir, 64*1024, args...)
}

// fxStreams is one finished fx invocation. The two streams stay separate,
// each with its own truncation flag, and the exit/cancel/timeout outcome is
// explicit rather than inferred from an empty string (A4).
type fxStreams struct {
	stdout, stderr  string
	stdoutTruncated bool
	stderrTruncated bool
	exitCode        int
	canceled        bool
	timedOut        bool
	err             error
}

func managementStreams(ctx context.Context, executable, dir string, limit int, args ...string) fxStreams {
	return runManagementStreams(ctx, executable, dir, limit, nil, args...)
}

// managementStreamsProgress is managementStreams with a throttled stderr
// sink. Quick Ask uses it so a long turn shows fx's latest line instead of
// nothing until the process exits.
func managementStreamsProgress(ctx context.Context, executable, dir string, limit int, progress func(string), args ...string) fxStreams {
	return runManagementStreams(ctx, executable, dir, limit, progress, args...)
}

func runManagementStreams(ctx context.Context, executable, dir string, limit int, progress func(string), args ...string) fxStreams {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.WaitDelay = 100 * time.Millisecond
	var stdout, stderr managementOutput
	stdout.limit = limit
	if limit <= 0 {
		stdout.limit = 64 * 1024
	}
	stderr.limit = 32 * 1024
	stderr.emit = progress
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	// The process is done, so a status line that was held back by the
	// throttle is now final. Publish it once, then never again (O5).
	stderr.flushPending()
	streams := fxStreams{
		stdout:          strings.TrimSpace(stdout.buffer.String()),
		stderr:          strings.TrimSpace(stderr.buffer.String()),
		stdoutTruncated: stdout.truncated,
		stderrTruncated: stderr.truncated,
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			streams.exitCode = exitErr.ExitCode()
		}
		streams.canceled = errors.Is(err, context.Canceled) || ctx.Err() == context.Canceled
		streams.timedOut = errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded
		streams.err = fmt.Errorf("fx %s: %w", strings.Join(args, " "), err)
		if streams.stderr != "" {
			streams.err = fmt.Errorf("%w: %s", streams.err, streams.stderr)
		}
		if streams.stderrTruncated {
			streams.err = fmt.Errorf("%w [stderr truncated]", streams.err)
		}
	}
	return streams
}

func managementReadLimit(ctx context.Context, executable, dir string, limit int, args ...string) (string, error) {
	streams := managementStreams(ctx, executable, dir, limit, args...)
	if streams.err != nil {
		// stdout stays the command's stdout; stderr is already part of the
		// error, never a replacement answer.
		return streams.stdout, streams.err
	}
	if streams.stdoutTruncated {
		// A cut-off JSON document is not a smaller result. Report the cap
		// here, where the caller cannot mistake the prefix for the whole
		// answer (A4).
		return streams.stdout, fmt.Errorf("fx %s: output exceeded %d KiB", strings.Join(args, " "), limit/1024)
	}
	var formatted bytes.Buffer
	if json.Indent(&formatted, []byte(streams.stdout), "", "  ") == nil {
		return formatted.String(), nil
	}
	return streams.stdout, nil
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
	live           []chrome.LiveAgent
	liveProblem    string
	profiles       []chrome.ProfileInfo
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
			doctor, ok := decodeDoctor(text, err)
			if ok {
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
		case "sessions-live":
			text, err := managementRead(ctx, executable, dir, args...)
			report, parseErr := fxlive.Parse([]byte(text))
			if err == nil && parseErr == nil {
				result.live = liveAgents(report.Agents)
				result.output = liveSummary(report)
			} else {
				result.output, result.problem = text, err
				if parseErr != nil {
					result.liveProblem = parseErr.Error()
				} else if err != nil {
					result.liveProblem = valueOr(text, err.Error())
				}
			}
		case "profiles":
			root, rootErr := fxhome.Root()
			if rootErr != nil {
				root = ""
			}
			scan := fxprofile.ScanContext(ctx, root, dir)
			for _, profile := range scan.Profiles {
				result.profiles = append(result.profiles, chrome.ProfileInfo{
					Name: profile.Name, Source: profile.Source, Path: profile.Path,
					Description: profile.Description, Permission: profile.Permission,
					Model: profile.Model, Effort: profile.Effort, PromptMode: profile.PromptMode,
					Tools: profile.Tools, Ignored: profile.Ignored,
					RepoPermissionIgnored: profile.RepoPermissionIgnored,
					Problem:               profile.Problem, PromptPreview: profile.PromptPreview,
				})
			}
			notes := append([]string(nil), scan.Problems...)
			if rootErr != nil {
				notes = append([]string{rootErr.Error()}, notes...)
			}
			if scan.Partial {
				notes = append(notes, "Profile scan stopped early.")
			}
			if len(notes) > 0 {
				// An incomplete scan is not "no profiles": say what was
				// skipped and why (P4).
				result.problem = errors.New(strings.Join(notes, "; "))
			}
			result.output = fmt.Sprintf("%d profile(s)", len(result.profiles))
		case "credits":
			text, err := managementRead(ctx, executable, dir, "credits", "--json")
			result.credits, result.problem = presentCredits(text, err)
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
			if action == "sessions-live" {
				state.Live = result.live
				state.LiveProblem = result.liveProblem
			}
			if action == "profiles" {
				state.Profiles = result.profiles
			}
			s.invalidate()
		})
	}()
}
