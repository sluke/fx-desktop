package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/app/chrome"
)

// Session is one fx terminal window.
type Session struct {
	conversationKey uint64
	coord           *Coordinator
	appearance      terminalAppearance
	// win is assigned by the app loop and read by terminal callbacks that
	// run on their own goroutine, so it carries a lock of its own. It is
	// deliberately not guarded by mu: sessionData holds mu while it reads
	// the window list, which would deadlock.
	winMu sync.Mutex
	win   *mygo.Window

	chat    *chatSession
	chatUI  chrome.ConversationState
	term    *terminal.Terminal
	project string
	// attachmentSeq names the composer's attachments. It is only touched on
	// the app loop, alongside chatUI.
	attachmentSeq uint64
	// consoles are the command runs this window's console drawer holds, in
	// the order they started, and consoleSeq names them (§4.7). App loop
	// only, like chatUI.
	consoles   []*consoleRun
	consoleSeq uint64
	// title is the command session's window title (fx doctor, fx ask …). A
	// project session leaves it empty and derives its name from the path.
	title string
	shell chrome.ShellState

	started time.Time
	model   string

	mu        sync.Mutex
	git       *GitInfo
	fx        *FxStatus
	usage     *FxUsage
	perms     *FxPermissions
	saved     []FxSession
	attention bool
	exited    bool
	exitCode  int
	// reviewedAt is when the user last opened the review drawer. With the
	// last commit time it forms the "已审水位" §5 defines: changes newer than
	// both are what the rail raises.
	reviewedAt time.Time
	// review is the diff the drawer has loaded: the path it belongs to, the
	// classified lines, what the read cap left out, and why it may be empty.
	reviewPath string
	reviewDiff []chrome.DiffLine
	reviewMore int
	reviewNote string
	reviewBusy bool
	// gitErr records that the last read failed, so the panel can say so
	// instead of showing "Reading workspace…" forever.
	gitErr error
	// files is the right rail's Files tab: the listings it has read and the
	// folders the user opened. Reads finish off the app loop, so it is
	// guarded by mu like the other data they produce.
	files *fileBrowser
	// mentionFiles are the project's files the @ picker offers, read when the
	// picker opens; mentionErr is why they could not be.
	mentionFiles   []chrome.MentionFile
	mentionLoading bool
	mentionErr     error

	stop chan struct{}
}

func (s *Session) view(ctx *ui.Context) {
	term := s.term
	c := s.coord
	agent := s.agentState()
	// The rail is computed here anyway, so the Dock badge and the "someone
	// needs you" notifications ride on this pass rather than on a poll of
	// their own. It also puts them on the app loop, where they belong.
	summaries := c.sessionSummaries()
	c.notifyAttention(summaries)
	chrome.Frame(ctx, chrome.Host{
		Title:        sessionTitle(s.project),
		Conversation: s.chat != nil,
		Appearance:   string(c.prefs.Appearance),
		Subtitle:     chrome.ProjectSubtitle(s.project),
		StatusLeft:   s.statusLeft(c.fxVersionSnapshot()),
		StatusRight:  s.statusRight(),
		StatusHi:     s.statusHi(),
		Actions:      c.sessionActions(s),
		Data:         c.sessionData(s),
		Agent:        agent,
		SessionBar: chrome.SessionBar{
			Name:    filepath.Base(s.project),
			Branch:  agent.Branch,
			Count:   c.sessionCount(),
			Running: agent.Status == "Running",
		},
		Sessions:   summaries,
		Active:     s.railID(),
		Shell:      &s.shell,
		Review:     c.sessionReview(s),
		Console:    c.sessionConsole(s),
		Files:      c.sessionFiles(s),
		Management: c.sessionManagement(s),
		History:    c.sessionHistory(s),
		Main: func(c *ui.Context) {
			if s.chat != nil {
				snapshot := s.chat.snapshotForView()
				mentionFiles, mentionLoading, mentionProblem := s.coord.sessionMentionFiles(s)
				c.Key(s.conversationKey)
				chrome.Conversation(c, &s.chatUI, chrome.ConversationHost{
					Project: filepath.Base(s.project), Model: snapshot.Model, Problem: snapshot.Problem, Messages: snapshot.Messages, Notices: snapshot.Notices,
					Ready: snapshot.Ready, Busy: snapshot.Busy, Stopping: snapshot.Stopping, ConfigChanging: snapshot.ConfigChanging, Permission: snapshot.Permission,
					CanSteer: snapshot.Steering, Queued: snapshot.Queued,
					Configs: snapshot.Configs, Modes: snapshot.Modes, Mode: snapshot.Mode,
					Effort: snapshot.Effort, Fast: snapshot.Fast, Ultra: snapshot.Ultra, AdvancedProblem: snapshot.AdvancedProblem,
					SetAdvanced:     s.chat.setAdvanced,
					SwitchWorkspace: func() { s.coord.pickFolder(s.winSnapshot()) },
					Auth:            s.authNotice(), SignIn: func() { mygo.App.WhenReady(func() { s.coord.signIn(s) }) },
					// Images ride the same send path as text, so a failure
					// that refuses one refuses the message it was meant for.
					CanAttach:        snapshot.Images,
					AttachFiles:      func(paths []string) { s.coord.attachImageFiles(s, paths) },
					RemoveAttachment: func(id uint64) { s.coord.removeAttachment(s, id) },
					// The @ picker reads the project's own files, so the app
					// holds them; the picker asks for them when it opens.
					MentionFiles: mentionFiles, MentionLoading: mentionLoading, MentionProblem: mentionProblem,
					CanMention:   snapshot.Files,
					OpenMentions: func() { s.coord.openMentions(s) },
					// A mention names a file in the message, so it is read
					// here: what cannot be attached refuses the message, with
					// the reason, rather than being sent as a name fx cannot
					// open.
					Send: func(text string, attachments []chrome.Attachment) bool {
						var mentions []promptMention
						if snapshot.Files {
							var problem string
							mentions, problem = resolveMentions(s.project, text)
							if problem != "" {
								s.coord.setAttachError(s, problem)
								return false
							}
						}
						if s.chatUI.AttachError != "" {
							s.chatUI.AttachError = ""
							s.invalidate()
						}
						return s.chat.submit(text, toPromptImages(attachments), mentions)
					},
					Stop: s.chat.stopTurn, ChoosePermission: s.chat.choosePermission, SetConfig: s.chat.setConfig,
					// The copy-feedback revert fires from a timer goroutine;
					// Invalidate is documented goroutine-safe.
					Changed: s.invalidate,
				})
				return
			}
			terminal.View(c, term).Fill().AutoFocus()
		},
	})
	// Every terminal this window shows — the window's own, and each console
	// run's — is told whether the window is dark: fx reads that report to
	// match the chrome it sits in. It is hoisted out of Main because a
	// project window's Main is the conversation, while its console drawer
	// still holds terminals of its own.
	if s.project != "" {
		s.syncTerminalAppearance(ctx.Theme().Dark)
	}
}

// syncTerminalAppearance tells the window's terminals which scheme is showing.
// fx asks with CSI ?997 and answers fx's OSC 11 probe with its own colors; the
// send is deferred to the end of the frame so the terminal sees the colors the
// frame just applied.
func (s *Session) syncTerminalAppearance(dark bool) {
	changed, generation := s.appearance.observe(dark)
	if !changed {
		return
	}
	report := appearanceReport(dark)
	mygo.App.WhenReady(func() {
		if s.appearance.generation != generation {
			return
		}
		if s.term != nil {
			s.term.Send(report)
		}
		for _, run := range s.consoles {
			if run.term != nil {
				run.term.Send(report)
			}
		}
	})
}

// statusLeft is the left group of the ink strip: the fx version, then the
// working-tree state. The branch and the permission mode are deliberately
// left out — they already appear, closer to the work, in the session bar
// and the agent strip.
func (s *Session) statusLeft(version string) []string {
	if version == "" {
		return nil
	}
	return []string{version}
}

// statusHi highlights the working-tree state, or nothing when clean.
func (s *Session) statusHi() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	if s.git != nil {
		n = len(s.git.Changes)
	}
	if n == 0 {
		return ""
	}
	return "● " + itoa3(n) + " modified"
}

// statusRight is the right group: where the session is rooted. Terminal
// font and encoding used to sit here too; they are IDE habits that no one
// reads in an agent client, so they were dropped rather than repeated.
func (s *Session) statusRight() []string {
	if s.project == "" {
		return nil
	}
	return []string{chrome.ProjectSubtitle(s.project)}
}

// setWin publishes the window; winSnapshot is how every other goroutine
// reads it, including the terminal's own title and exit callbacks.
func (s *Session) setWin(w *mygo.Window) {
	s.winMu.Lock()
	s.win = w
	s.winMu.Unlock()
}

func (s *Session) winSnapshot() *mygo.Window {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	return s.win
}

func (s *Session) invalidate() {
	if w := s.winSnapshot(); w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
}

// agentState reports what fx itself reports: model and permission mode from
// `fx status --json`, tokens and spend from `fx usage --json`, plus the
// process state, git branch and session uptime.
// authNotice is fx's sign-in state for this session, read the same way
// agentState reads its fx info.
func (s *Session) authNotice() chrome.AuthNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fx == nil {
		return chrome.AuthNotice{}
	}
	return chrome.AuthNotice{Auth: s.fx.Auth, Help: s.fx.AuthHelp, Providers: s.fx.ConnectedProviders}
}

func (s *Session) agentState() chrome.Agent {
	a := chrome.Agent{
		Model:   s.model,
		Elapsed: formatElapsed(time.Since(s.started)),
		Status:  "Running",
	}
	s.mu.Lock()
	if s.fx != nil {
		// FX_DESKTOP_MODEL is what this session was launched with, so it
		// wins over the configured model `fx status` reports.
		if s.model == "" && s.fx.Model != "" {
			a.Model = s.fx.Model
		}
		a.Permission = s.fx.PermissionMode
	}
	if s.perms != nil && s.perms.Mode != "" {
		a.Permission = s.perms.Mode
	}
	if s.usage != nil && (s.usage.Totals.TotalTokens > 0 || s.usage.Totals.Spend > 0) {
		// `fx usage` is an AI Gateway account total, not this conversation's
		// work. It is still the only source for Spend, but a chat window
		// replaces the token count below with the session's own usage, so
		// the strip never mixes a monthly number with per-run context.
		a.Tokens = compactCount(s.usage.Totals.TotalTokens)
		a.Spend = fmt.Sprintf("$%.2f", s.usage.Totals.Spend)
	}
	if s.exited {
		// A failed run and a clean one are different facts: keep the code.
		a.Status = "Exited"
		if s.exitCode != 0 {
			a.Status = "Exited " + strconv.Itoa(s.exitCode)
		}
	}
	if s.attention {
		a.Status = "Attention"
	}
	if s.git != nil {
		a.Branch = s.git.Branch
	}
	s.mu.Unlock()
	if s.chat != nil {
		st := s.chat.status()
		if st.Model != "" {
			a.Model = st.Model
		}
		if st.Mode != "" {
			a.Permission = st.Mode
		}
		a.Status = "Connecting"
		if st.Ready {
			a.Status = "Ready"
		}
		if st.Busy {
			a.Status = "Working"
		}
		if st.Permission {
			a.Status = "Attention"
		}
		if st.Problem != "" {
			a.Status = "Attention"
		}
		// Provider-confirmed context usage, sent as usage_update once the
		// window is known. Until then the strip stays quiet rather than
		// showing a guess.
		a.ContextUsed = st.ContextUsed
		a.ContextTotal = st.ContextTotal
		// Tokens is what this conversation has processed: the input and
		// output counts fx reports with each prompt result. It is empty
		// before the first completed turn, when there is genuinely nothing
		// to report, rather than showing the account's monthly total in its
		// place.
		a.Tokens = ""
		if total := st.InputTokens + st.OutputTokens; total > 0 {
			a.Tokens = compactCount(int64(total))
		}
	}
	return a
}

// refreshGit re-reads the working tree off the UI thread and repaints. It
// records what came back, including "this is not a repository": a panel that
// keeps saying "Reading workspace…" after an answer is lying to the user.
func (s *Session) refreshGit() {
	info, err := gitInfo(context.Background(), s.project)
	s.mu.Lock()
	s.git = info
	s.gitErr = err
	s.mu.Unlock()
	s.invalidate()
}

// workspaceState maps a git read into the vocabulary the rail and the drawer
// share: a finished read is a repository, errNoRepo is the workspace's own
// fact that it has none, anything else is a failure, and neither is reading.
func workspaceState(git *GitInfo, err error) chrome.WorkspaceState {
	switch {
	case git != nil:
		return chrome.WorkspaceRepo
	case errors.Is(err, errNoRepo):
		return chrome.WorkspaceNotRepo
	case err != nil:
		return chrome.WorkspaceFailed
	default:
		return chrome.WorkspaceReading
	}
}

// workspace is this session's git state, read under the lock the render pass
// has to respect.
func (s *Session) workspace() chrome.WorkspaceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return workspaceState(s.git, s.gitErr)
}

// refreshFxInfo re-reads fx's own state off the UI thread and repaints.
// Command sessions have no workspace, so they are skipped.
func (s *Session) refreshFxInfo() {
	if s.project == "" {
		return
	}
	ctx := context.Background()
	fxPath := s.coord.fxPathSnapshot()
	st, _ := fxStatus(ctx, fxPath, s.project)
	us, _ := fxUsage(ctx, fxPath, s.project)
	pm, _ := fxPermissions(ctx, fxPath, s.project)
	sv, svErr := fxSessions(ctx, fxPath, s.project)
	s.mu.Lock()
	if st != nil {
		s.fx = st
	}
	if us != nil {
		s.usage = us
	}
	if pm != nil {
		s.perms = pm
	}
	// A failed query is not an empty result: keep what we already have
	// rather than blanking the panel.
	if svErr == nil {
		s.saved = sv
	}
	s.mu.Unlock()
	s.invalidate()
}

// refreshSavedList rereads the five most recent saved conversations without
// the status, usage and permission queries refreshFxInfo also makes. Deleting
// history changes only this list, and every open window may show the removed
// row in its sidebar.
func (s *Session) refreshSavedList() {
	if s.project == "" {
		return
	}
	saved, err := fxSessions(context.Background(), s.coord.fxPathSnapshot(), s.project)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.saved = saved
	s.mu.Unlock()
	s.invalidate()
}

// refreshSavedLists updates the sidebar history of every open window after a
// deletion. The windows are independent; one slow fx query must not delay the
// others.
func (c *Coordinator) refreshSavedLists() {
	for _, s := range c.sessionsSnapshot() {
		if s.project != "" {
			go s.refreshSavedList()
		}
	}
}

func (s *Session) setAttention() {
	s.mu.Lock()
	s.attention = true
	s.mu.Unlock()
	s.invalidate()
}

// setExited records the process end and, with it, the exit status. The
// agent strip shows "Exited 1" rather than a bare "Exited": a failed run
// and a clean one are different facts.
func (s *Session) setExited(code int) {
	s.mu.Lock()
	s.exited = true
	s.exitCode = code
	s.mu.Unlock()
	s.invalidate()
}

func (c *Coordinator) openSession(project string) *Session {
	return c.openSessionWithFlags(project)
}

// openSessionWithFlags opens a session, passing extra launch flags such as
// --model <id>, -c or --resume <id> straight through to fx.
func (c *Coordinator) openSessionWithFlags(project string, args ...string) *Session {
	project = filepath.Clean(project)
	modelFlag := os.Getenv("FX_DESKTOP_MODEL")
	if modelFlag != "" {
		args = append([]string{"--model", modelFlag}, args...)
	}
	s := &Session{coord: c, project: project, shell: chrome.DefaultShell(), started: time.Now(), model: modelFlag, stop: make(chan struct{})}
	p := c.prefs
	s.chat = newChatSession(c.fxPathSnapshot(), project, args, s.invalidate)
	s.chat.stats = c.stats

	s.setWin(mygo.NewWindow(mygo.WindowOptions{
		Title:                sessionTitle(project),
		Width:                1280,
		Height:               800,
		MinWidth:             720,
		MinHeight:            480,
		StateKey:             "fx-session",
		TitleBarStyle:        mygo.TitleBarHidden,
		TitleBarHeight:       chrome.TitleBarHeight,
		TrafficLightPosition: windowTrafficLights(),
		Content:              ui.View(s.view),
	}))
	if w := s.winSnapshot(); w != nil {
		w.SetRepresentedFile(project)
		w.OnClosed(func() {
			close(s.stop)
			s.chat.Close()
			s.closeConsoles()
			c.removeSession(s)
		})
		if p.CloseToTray {
			w.OnClose(func(e *mygo.CloseEvent) {
				e.PreventDefault()
				w.Hide()
			})
		}
	}
	c.addSession(s)
	go s.chat.connect()

	go s.refreshGit()
	go s.refreshFxInfo()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				// Re-read, not merely repaint: a ticker that only
				// invalidates leaves the panel showing its startup
				// snapshot forever.
				s.refreshGit()
				s.refreshFxInfo()
			}
		}
	}()
	return s
}

func (c *Coordinator) openCommandSession(title, dir string, args ...string) {
	cmd := append([]string{c.fxPathSnapshot()}, args...)
	s := &Session{
		coord:   c,
		title:   title,
		shell:   chrome.DefaultShell(),
		started: time.Now(),
		stop:    make(chan struct{}),
	}
	// A command session has no workspace, so it opens without the rail; the
	// user can still bring it up from the title bar if they want context.
	s.shell.SidebarOpen = false
	lightTheme, darkTheme := terminalThemes()
	term, err := terminal.New(terminal.Options{
		Command:   cmd,
		Font:      terminal.Font{Family: c.prefs.FontFamily, Size: float32(c.prefs.FontSize)},
		Theme:     lightTheme,
		DarkTheme: darkTheme,
		// `fx pr`, `fx issue` and friends act on a repository: without a
		// working directory they run against whatever the app was launched
		// from, which is never the project the user is looking at.
		Dir: dir,
		OnTitle: func(t string) {
			if w := s.winSnapshot(); w != nil && !w.IsDestroyed() {
				w.SetTitle(t)
			}
		},
		// OnExit runs on the terminal's own goroutine. Touching a window
		// from anywhere but the app loop is unsafe, so hop back to it.
		OnExit: func(code int) {
			s.setExited(code)
			mygo.App.WhenReady(func() {
				if w := s.winSnapshot(); w != nil && !w.IsDestroyed() {
					w.Close()
				}
			})
		},
	})
	if err != nil {
		mygo.Dialog.Error("Terminal", err.Error())
		return
	}
	s.term = term
	s.setWin(mygo.NewWindow(mygo.WindowOptions{
		Title:                title,
		Width:                960,
		Height:               560,
		TitleBarStyle:        mygo.TitleBarHidden,
		TitleBarHeight:       chrome.TitleBarHeight,
		TrafficLightPosition: windowTrafficLights(),
		Content:              ui.View(s.view),
	}))
	if w := s.winSnapshot(); w != nil {
		w.OnClosed(func() {
			close(s.stop)
			term.Close()
			c.removeSession(s)
		})
	}
	c.addSession(s)
}

func (c *Coordinator) removeSession(s *Session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.sessions {
		if x == s {
			c.sessions = append(c.sessions[:i], c.sessions[i+1:]...)
			break
		}
	}
}

// formatElapsed renders an uptime as m:ss or h:mm:ss.
func formatElapsed(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 0 {
		sec = 0
	}
	h, rest := sec/3600, sec%3600
	m, s := rest/60, rest%60
	if h > 0 {
		return itoa3(h) + ":" + pad2(m) + ":" + pad2(s)
	}
	return pad2(m) + ":" + pad2(s)
}

func itoa3(n int) string {
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

// replaceConversation keeps one project window while fx owns each saved thread.
// The prior process finishes shutdown before its replacement is started.
func (s *Session) replaceConversation(args []string) {
	previous := s.chat
	model := os.Getenv("FX_DESKTOP_MODEL")
	inheritModel := true
	for _, arg := range args {
		if arg == "--model" || arg == "--resume" || arg == "-c" || arg == "--continue" {
			inheritModel = false
		}
	}
	if previous != nil && inheritModel && model == "" {
		model = previous.status().Model
	}
	if model != "" {
		args = append([]string{"--model", model}, args...)
	}
	next := newChatSession(s.coord.fxPathSnapshot(), s.project, args, s.invalidate)
	next.stats = s.coord.stats
	s.chat = next
	s.conversationKey++
	s.chatUI = chrome.ConversationState{}
	if previous != nil {
		previous.Close()
	}
	s.invalidate()
	go func() {
		if previous != nil {
			<-previous.closedDone
		}
		if w := s.winSnapshot(); w != nil && w.IsDestroyed() {
			next.Close()
			return
		}
		next.connect()
	}()
	go s.refreshFxInfo()
}
