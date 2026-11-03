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
	"fx-desktop/internal/app/presenter"
)

// ProjectWindow is one window of the app. It owns a window handle, a workspace and
// (for a project window) one live conversation; each of those has its own
// generation, so a late result from an older workspace, conversation or
// history query can never be published as if it were current (§4.4).
type ProjectWindow struct {
	// windowID names this window for the lifetime of the app. It is assigned
	// on the app loop and never reused, so a queued closure that names a
	// window cannot reach a different one.
	windowID uint64
	// ctx is the window's lifetime. Every background task — history, git,
	// files, fx queries, submit preparation — derives from it, so closing
	// the window cancels them all instead of leaving each to invent its own
	// scope.
	ctx    context.Context
	cancel context.CancelFunc
	// swap serializes conversation replacement and owns the conversation
	// generation and the process barrier (§B1, §B4).
	swap conversationSwap
	// loop is the app loop a test owns. When nil, dispatch uses the toolkit's
	// loop (or runs inline for a window that has none); a test sets it to a
	// queue it drains itself, so "UI work happens on one goroutine" is a
	// property the test can actually observe rather than inherit from the
	// inline fallback (§2.1).
	loop func(func())
	// draftVersion names submissions; draftRevision counts composer edits and
	// draftSeen is the text the last count was taken at (§B5). App loop only,
	// like chatUI.
	draftVersion    uint64
	draftRevision   uint64
	draftSeen       string
	preparing       *submitRequest
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
	// images are the attachment reads this window started and has not
	// published yet. A conversation swap or a close retires them, so a result
	// that arrives later is not attached to another draft (N1).
	images []*imageBatch
	// imageBudget is how much decoded image memory this window holds at once
	// and how many images it decodes in parallel (R5). Every batch, the
	// clipboard paste included, shares this one budget: per-batch accounting
	// cannot bound the window.
	imageBudget imageDecodeBudget
	// imagePending is the window's admission for the compressed bytes of
	// images that are being added: the files a batch is reading, the clipboard
	// bytes a paste holds, and the reads waiting to be adopted. It sits in
	// front of imageBudget, so several drops share one ceiling instead of
	// each taking a batch's worth (S5).
	imagePending imagePendingBudget
	// console is the command drawer this window owns (§4.7). It is app-loop
	// state, like chatUI: a terminal's goroutine reports to it by asking the
	// app to run the change.
	console consoleController
	// scopes holds one cancellable child of ctx per feature (git, history,
	// fx queries, review, …), so a newer query retires the older one.
	scopes featureScopes
	// title is the command session's window title (fx doctor, fx ask …). A
	// project session leaves it empty and derives its name from the path.
	title string
	shell chrome.ShellState

	started time.Time
	model   string

	mu    sync.Mutex
	fx    *FxStatus
	usage *FxUsage
	perms *FxPermissions
	saved []FxSession
	// fxGen names the newest fx status/usage/permission read; savedGen names
	// the newest read that may write `saved`, whichever feature started it.
	// A feature's cancel retires the command, but a result already computed
	// still arrives — the generation is the admission check, and it lives
	// beside the state it publishes (N4).
	fxGen     uint64
	savedGen  uint64
	attention bool
	exited    bool
	exitCode  int
	// workspace is everything read about this window's directory. It has its
	// own lock; the fields above are the window's own facts.
	workspace workspaceController

	stop chan struct{}
}

func (s *ProjectWindow) view(ctx *ui.Context) {
	// Every pass sees the draft the composer will render, so an edit since
	// the last frame is counted even if the user submits before it repaints
	// (§B5).
	s.observeDraft()
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
					// Submitting never does file I/O on this thread: the draft
					// is handed to a preparation step whose result comes back
					// through dispatch, checked against the draft and
					// conversation generations (§4.2).
					Preparing: s.preparingSubmit(),
					Send:      s.sendDraft,
					// An edit is an event, not a difference the next frame
					// happens to notice: the draft's revision follows the
					// control's own changes (§B5, N2).
					DraftEdited: s.draftEdited,
					Stop:        s.chat.stopTurn, ChoosePermission: s.chat.choosePermission, SetConfig: s.chat.setConfig,
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
func (s *ProjectWindow) syncTerminalAppearance(dark bool) {
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
		for _, run := range s.console.runs {
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
func (s *ProjectWindow) statusLeft(version string) []string {
	if version == "" {
		return nil
	}
	return []string{version}
}

// statusHi highlights the working-tree state, or nothing when clean.
func (s *ProjectWindow) statusHi() string {
	n := s.workspace.changeCount()
	if n == 0 {
		return ""
	}
	return "● " + itoa3(n) + " modified"
}

// statusRight is the right group: where the session is rooted. Terminal
// font and encoding used to sit here too; they are IDE habits that no one
// reads in an agent client, so they were dropped rather than repeated.
func (s *ProjectWindow) statusRight() []string {
	if s.project == "" {
		return nil
	}
	return []string{chrome.ProjectSubtitle(s.project)}
}

// setWin publishes the window; winSnapshot is how every other goroutine
// reads it, including the terminal's own title and exit callbacks.
func (s *ProjectWindow) setWin(w *mygo.Window) {
	s.winMu.Lock()
	s.win = w
	s.winMu.Unlock()
}

func (s *ProjectWindow) winSnapshot() *mygo.Window {
	s.winMu.Lock()
	defer s.winMu.Unlock()
	return s.win
}

func (s *ProjectWindow) invalidate() {
	if w := s.winSnapshot(); w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
}

// agentState reports what fx itself reports: model and permission mode from
// `fx status --json`, tokens and spend from `fx usage --json`, plus the
// process state, git branch and session uptime.
// authNotice is fx's sign-in state for this session, read the same way
// agentState reads its fx info.
func (s *ProjectWindow) authNotice() chrome.AuthNotice {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fx == nil {
		return chrome.AuthNotice{}
	}
	return chrome.AuthNotice{Auth: s.fx.Auth, Help: s.fx.AuthHelp, Providers: s.fx.ConnectedProviders}
}

func (s *ProjectWindow) agentState() chrome.Agent {
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
	s.mu.Unlock()
	// Read outside the window lock: the workspace has its own, and the two
	// are never nested (§4.4).
	if branch := s.workspace.branch(); branch != "" {
		a.Branch = branch
	}
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

// startGitRefresh starts a working-tree read off the UI thread. The
// generation is allocated here — where the refresh is asked for — rather than
// inside the goroutine, so a read that starts late cannot claim to be newer
// than one requested after it (N4).
func (s *ProjectWindow) startGitRefresh() {
	ctx, stop := s.scope("git")
	gen := s.workspace.beginGitRead()
	go func() {
		defer stop()
		s.readGit(ctx, gen)
	}()
}

// refreshGit is the synchronous form: the caller already runs it on a worker.
func (s *ProjectWindow) refreshGit() {
	ctx, stop := s.scope("git")
	defer stop()
	s.readGit(ctx, s.workspace.beginGitRead())
}

// readGit performs one read and publishes it only while it is still the
// newest. A result that lost the race is dropped instead of repainting a
// snapshot that a newer read already replaced.
func (s *ProjectWindow) readGit(ctx context.Context, gen uint64) {
	info, err := gitInfo(ctx, s.project)
	if ctx.Err() != nil {
		return
	}
	if s.workspace.publishGit(gen, info, err) {
		s.invalidate()
	}
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

// startFxRefresh starts fx's own state read off the UI thread. Command
// sessions have no workspace, so they are skipped. The generations are
// allocated at the request, before the goroutine runs (N4).
func (s *ProjectWindow) startFxRefresh() {
	if s.project == "" {
		return
	}
	ctx, stop := s.scope("fx")
	fxGen, savedGen := s.beginFxRead()
	go func() {
		defer stop()
		s.readFxInfo(ctx, fxGen, savedGen)
	}()
}

// refreshFxInfo is the synchronous form. It publishes nothing that is no
// longer the newest read.
func (s *ProjectWindow) refreshFxInfo() {
	if s.project == "" {
		return
	}
	ctx, stop := s.scope("fx")
	defer stop()
	fxGen, savedGen := s.beginFxRead()
	s.readFxInfo(ctx, fxGen, savedGen)
}

// beginFxRead tickets one fx read. It shares savedGen with beginSavedRead
// because both features write the same `saved` field: whichever was asked for
// last is the one whose answer stands (N4).
func (s *ProjectWindow) beginFxRead() (fxGen, savedGen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fxGen++
	s.savedGen++
	return s.fxGen, s.savedGen
}

func (s *ProjectWindow) readFxInfo(ctx context.Context, fxGen, savedGen uint64) {
	fxPath := s.coord.fxPathSnapshot()
	st, _ := fxStatus(ctx, fxPath, s.project)
	us, _ := fxUsage(ctx, fxPath, s.project)
	pm, _ := fxPermissions(ctx, fxPath, s.project)
	sv, svErr := fxSessions(ctx, fxPath, s.project)
	if ctx.Err() != nil {
		return
	}
	if s.publishFx(fxGen, savedGen, st, us, pm, sv, svErr) {
		s.invalidate()
	}
}

// publishFx replaces as much of fx's state as this read may still own. The
// generation check and the writes are one critical section.
func (s *ProjectWindow) publishFx(fxGen, savedGen uint64, st *FxStatus, us *FxUsage, pm *FxPermissions, sv []FxSession, svErr error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fxGen != s.fxGen {
		return false
	}
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
	if svErr == nil && savedGen == s.savedGen {
		s.saved = sv
	}
	return true
}

// startSavedRefresh rereads the five most recent saved conversations without
// the status, usage and permission queries refreshFxInfo also makes. Deleting
// history changes only this list, and every open window may show the removed
// row in its sidebar.
func (s *ProjectWindow) startSavedRefresh() {
	if s.project == "" {
		return
	}
	ctx, stop := s.scope("fx-saved")
	gen := s.beginSavedRead()
	go func() {
		defer stop()
		s.readSavedList(ctx, gen)
	}()
}

// refreshSavedList is the synchronous form.
func (s *ProjectWindow) refreshSavedList() {
	if s.project == "" {
		return
	}
	ctx, stop := s.scope("fx-saved")
	defer stop()
	s.readSavedList(ctx, s.beginSavedRead())
}

func (s *ProjectWindow) beginSavedRead() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.savedGen++
	return s.savedGen
}

func (s *ProjectWindow) readSavedList(ctx context.Context, gen uint64) {
	saved, err := fxSessions(ctx, s.coord.fxPathSnapshot(), s.project)
	if err != nil || ctx.Err() != nil {
		return
	}
	if s.publishSaved(gen, saved) {
		s.invalidate()
	}
}

func (s *ProjectWindow) publishSaved(gen uint64, saved []FxSession) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.savedGen {
		return false
	}
	s.saved = saved
	return true
}

// refreshSavedLists updates the sidebar history of every open window after a
// deletion. The windows are independent; one slow fx query must not delay the
// others.
func (c *Coordinator) refreshSavedLists() {
	for _, s := range c.sessionsSnapshot() {
		if s.project != "" {
			s.startSavedRefresh()
		}
	}
}

func (s *ProjectWindow) setAttention() {
	s.mu.Lock()
	s.attention = true
	s.mu.Unlock()
	s.invalidate()
}

// setExited records the process end and, with it, the exit status. The
// agent strip shows "Exited 1" rather than a bare "Exited": a failed run
// and a clean one are different facts.
func (s *ProjectWindow) setExited(code int) {
	s.mu.Lock()
	s.exited = true
	s.exitCode = code
	s.mu.Unlock()
	s.invalidate()
}

// openSessionWithFlags opens a session, passing extra launch flags such as
// --model <id>, -c or --resume <id> straight through to fx.
func (c *Coordinator) openSessionWithFlags(project string, args ...string) *ProjectWindow {
	project = filepath.Clean(project)
	modelFlag := os.Getenv("FX_DESKTOP_MODEL")
	if modelFlag != "" {
		args = append([]string{"--model", modelFlag}, args...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &ProjectWindow{coord: c, project: project, ctx: ctx, cancel: cancel, shell: chrome.DefaultShell(), started: time.Now(), model: modelFlag, stop: make(chan struct{})}
	s.console.window = s
	s.windowID = c.nextWindowID()
	p := c.prefs
	// The connection owns its own lifetime: the window's ctx cancels the
	// window's tasks, while fx is stopped through Close's bounded shutdown
	// (stdin EOF, then a grace period) rather than by cancellation (§2.1).
	s.chat = newChatSession(c.fxPathSnapshot(), project, args, s.invalidate)
	s.chat.stats = c.stats
	s.swap.window = s

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
			// Closing the window ends every task it owns: the submit
			// preparation, the history queries, the periodic refresh.
			s.cancel()
			s.abandonSubmit()
			s.retireImageBatches()
			close(s.stop)
			s.chat.Close()
			s.console.closeAll()
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
	s.startConversation(s.chat)

	s.startGitRefresh()
	s.startFxRefresh()
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
	ctx, cancel := context.WithCancel(context.Background())
	s := &ProjectWindow{
		coord:   c,
		title:   title,
		ctx:     ctx,
		cancel:  cancel,
		shell:   chrome.DefaultShell(),
		started: time.Now(),
		stop:    make(chan struct{}),
	}
	s.windowID = c.nextWindowID()
	s.console.window = s
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
		// Both callbacks run on the terminal's own goroutine. Touching a
		// window from anywhere but the app loop is unsafe, so both go
		// through the window controller, which checks the window is alive
		// and does the work on the app loop (§2).
		OnTitle: func(t string) {
			s.dispatch(func() {
				if w := s.winSnapshot(); w != nil {
					w.SetTitle(t)
				}
			})
		},
		OnExit: func(code int) {
			s.setExited(code)
			s.dispatch(func() {
				if w := s.winSnapshot(); w != nil {
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
			s.cancel()
			close(s.stop)
			term.Close()
			c.removeSession(s)
		})
	}
	c.addSession(s)
}

func (c *Coordinator) removeSession(s *ProjectWindow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, x := range c.sessions {
		if x == s {
			c.sessions = append(c.sessions[:i], c.sessions[i+1:]...)
			break
		}
	}
}

// formatElapsed renders an uptime for the agent strip.
func formatElapsed(d time.Duration) string { return presenter.Elapsed(d) }

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

// replaceConversation keeps one project window while fx owns each saved thread.
// The prior process finishes shutdown before its replacement is started.
func (s *ProjectWindow) replaceConversation(args []string) {
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
	// A submission prepared against the old conversation must not land in
	// the new one, and neither may an image still being read: both belong to
	// a draft that is no longer on screen (N1).
	s.abandonSubmit()
	s.retireImageBatches()
	s.chatUI = chrome.ConversationState{}
	s.draftSeen = ""
	if previous != nil {
		previous.Close()
	}
	s.invalidate()
	// The swap owns admission and the process barrier: a request waits for the
	// last conversation that actually started, not merely the one it replaced
	// (§B4). The registry claims the saved ID on the same path, so the window
	// is protected before the handshake (T9).
	s.startConversation(next)
	s.startFxRefresh()
}

// startConversation admits a conversation into this window. The saved
// conversation it names is claimed first — before any process exists, because
// fx starts writing the directory as soon as it accepts the open — and a
// deletion that owns that ID refuses the conversation here rather than after it
// has begun writing (T9, R3).
func (s *ProjectWindow) startConversation(next *chatSession) {
	if next == nil {
		return
	}
	if s.coord != nil {
		if err := next.attachRegistry(&s.coord.sessionUse); err != nil {
			// The window still shows why; no process is started.
			next.abandon(err)
			return
		}
	}
	s.swap.request(next)
}

// lifetime is the window's context. A window built by a test may not have one;
// it gets a background context rather than a panic, and loses nothing because
// such a window is closed explicitly.
func (s *ProjectWindow) lifetime() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

// dispatch runs fn on the app loop, but only while the window is alive. It is
// the one place a background task hands its result back: the closure re-checks
// the window and, where it matters, its own generation, because a cancel
// cannot retract a callback that has already been queued (§4.4).
func (s *ProjectWindow) dispatch(fn func()) {
	if s.loop != nil {
		s.loop(fn)
		return
	}
	if s.winSnapshot() == nil {
		// A window that was never created (unit tests) has no app loop to
		// hand the work to; the closure runs where the work finished.
		fn()
		return
	}
	mygo.App.WhenReady(func() {
		if s.lifetime().Err() != nil {
			return
		}
		if w := s.winSnapshot(); w != nil && w.IsDestroyed() {
			return
		}
		fn()
	})
}
