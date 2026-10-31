package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/prefs"
)

func (c *Coordinator) recentPaths() []string {
	var paths []string
	for _, e := range c.projects.List() {
		paths = append(paths, e.Path)
	}
	return paths
}

// nativeAction ends the surface frame before running an effect that can
// open a window, await a native dialog or report a launch error. OnClick
// runs after the view builds, but is still inside the native draw callback.
func nativeAction(action func()) func() {
	return func() { mygo.App.WhenReady(action) }
}

func (c *Coordinator) chromeActions(parent *mygo.Window) chrome.Actions {
	return chrome.Actions{
		OpenFolder:  func() { c.pickFolder(parent) },
		OpenProject: func(path string) { mygo.App.WhenReady(func() { c.OpenProject(path) }) },
		// Revealing the current folder is not a workspace switch: it opens
		// Finder (or the platform file manager) without touching the ACP
		// session, drafts or running work.
		OpenInFileManager: func(path string) {
			mygo.App.WhenReady(func() { c.openInFileManager(path) })
		},
		OpenPreferences: nativeAction(c.showPreferences),
		OpenDocs: nativeAction(func() {
			_ = mygo.Shell.OpenExternal("https://fx.sh/docs")
		}),
		OpenConfig: nativeAction(c.openFxConfig),
		RunDoctor:  nativeAction(func() { c.openCommandSession("fx doctor", "", "doctor") }),
		// Signing in is an interactive CLI flow and ACP advertises no auth
		// method (§12.1), so the honest affordance is a terminal window
		// running the command fx itself names in its guidance.
		SignIn:       nativeAction(func() { c.signIn(nil) }),
		CheckUpdates: nativeAction(c.checkUpdates),
	}
}

// openInFileManager reveals a workspace folder in Finder or the platform's
// file manager. It only validates and opens the path: unlike OpenProject it
// never starts, resumes or switches a session.
func (c *Coordinator) openInFileManager(path string) {
	if strings.TrimSpace(path) == "" {
		return
	}
	target, err := resolveFileManagerPath(path)
	if err != nil {
		mygo.Dialog.Error("Open folder", "The folder is no longer available:\n"+target)
		return
	}
	if err := mygo.Shell.OpenPath(target); err != nil {
		mygo.Dialog.Error("Open folder", "Could not open the folder:\n"+target)
	}
}

// resolveFileManagerPath expands the "~" form used by the UI and checks that
// the target is still a directory before the file manager is asked to open it.
func resolveFileManagerPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("no folder")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path, err
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return path, err
	}
	if !info.IsDir() {
		return path, fmt.Errorf("%s is not a folder", path)
	}
	return path, nil
}

// signIn runs the CLI that can sign in. ACP advertises no auth method
// (fx 0.0.14 returns an empty authMethods, §12.1), so the GUI cannot collect
// credentials itself: it runs fx's own flow and says so.
//
// A project window gets that flow in its console drawer, where the login can
// actually be typed into (§4.7); a window with no session to hang a drawer on
// gets a command window, as every command did before.
func (c *Coordinator) signIn(s *Session) {
	if s != nil && s.project != "" {
		c.consoleIn(s, "fx login", "login")
		return
	}
	c.openCommandSession("fx login", "", "login")
}

// sessionActions extends the shared actions with the ones only a project
// session offers: new sessions, palette dispatch, commits, slash commands
// typed into the terminal, and the fx launch options.
func (c *Coordinator) sessionActions(s *Session) chrome.Actions {
	a := c.chromeActions(s.winSnapshot())
	a.NewSession = nativeAction(func() {
		if s.chat != nil {
			s.replaceConversation(nil)
		} else {
			c.showPicker()
		}
	})
	a.RunCommand = func(id string) { mygo.App.WhenReady(func() { c.runCommand(s, id) }) }
	// The palette only offers the attach rows where fx really takes images
	// and there is a native session to send them from: a row that opens
	// nothing is a lie (§A.8).
	if s.chat != nil && s.chat.canTakeImages() {
		a.AttachImage = nativeAction(func() { c.pickImage(s) })
		a.PasteImage = nativeAction(func() { c.pasteImage(s) })
	}
	a.SetAppearance = func(mode string) { c.setAppearance(mode) }
	// The rail raises an already-open session's window in every window kind,
	// command sessions included: the rail is the app's map, not a project
	// affordance.
	a.FocusSession = func(id string) { mygo.App.WhenReady(func() { c.focusSession(id) }) }
	// A command window (fx pr, fx doctor …) has no workspace. Binding the
	// git and fx-session actions here would offer buttons that cannot
	// work; left nil, the rail hides them instead.
	if s.project == "" {
		return a
	}
	a.ManageWorkspace = func() { c.openManagement(s) }
	a.OpenHistory = func() { mygo.App.WhenReady(func() { c.openHistory(s) }) }
	a.Commit = nativeAction(func() { c.commitChanges(s) })
	a.RefreshChanges = nativeAction(func() { go s.refreshGit() })
	// The review drawer reads one workspace's tree. A command window has
	// none, so it gets no entry point — not even a status-strip button.
	// A project that is not a git repository has nothing the drawer could
	// ever show either: the rail's Changes line says so, and neither the
	// strip nor the palette offers a dead end. The menu item stays, as the
	// console's does, and opens that same answer.
	if s.workspace() != chrome.WorkspaceNotRepo {
		a.ToggleReview = nativeAction(func() { c.toggleReview(s) })
	}
	a.CloseReview = nativeAction(func() { c.closeReview(s) })
	a.SelectChange = func(path string) { mygo.App.WhenReady(func() { c.selectChange(s, path) }) }
	// The right rail's Files tab browses this window's workspace. It is
	// wired here rather than in chromeActions because its rows name a
	// workspace path: a command window has none to show.
	a.SelectInspectorTab = func(tab int) { c.selectInspectorTab(s, tab) }
	a.ToggleFileDir = func(path string) { c.toggleFileDir(s, path) }
	a.OpenFile = func(path string) { c.openFileWithDefaultApp(s, path) }
	a.OpenFileInEditor = func(path string) { c.openFileInTextEditor(s, path) }
	a.RefreshFiles = func() { c.refreshFiles(s) }
	// The console belongs to a project window: it is where the runs that
	// need the workspace (fx doctor, fx pr, fx login) report back (§4.7).
	a.ToggleConsole = nativeAction(func() { c.toggleConsole(s) })
	a.SelectConsole = func(id uint64) { mygo.App.WhenReady(func() { c.selectConsole(s, id) }) }
	a.CloseConsoleRun = func(id uint64) { mygo.App.WhenReady(func() { c.closeConsoleRun(s, id) }) }
	// The CLI that signs in runs in the drawer too, so the sign-in the
	// conversation offers is the one the console can carry.
	a.SignIn = nativeAction(func() { c.signIn(s) })
	a.ResumeSession = func(id string) { c.sessionManagement(s).Resume(id, s.project) }
	a.Ask = func(prompt string) {
		mygo.App.WhenReady(func() { c.consoleIn(s, "fx ask", "ask", prompt) })
	}
	a.AskAdvanced = func(req chrome.AskRequest) {
		mygo.App.WhenReady(func() { c.runAsk(s, req) })
	}
	// The picker belongs to the conversation, so opening it is UI state and
	// not a session call: the strip and the palette must open the same panel
	// rather than keep one each.
	a.ChooseConfig = s.openConfig
	a.StageAll = nativeAction(func() { c.stageAll(s) })
	return a
}

// openConfig keeps palette configuration and inline configuration on one state.
// An empty id opens the complete list, including the hidden permission mode.
func (s *Session) openConfig(configID string) {
	s.chatUI.ConfigListOpen = configID == ""
	s.chatUI.ConfigOpen = configID
	s.chatUI.ConfigQuery = ""
	s.invalidate()
}

// setAppearance persists the appearance choice and repaints every live
// window so the change is visible immediately.
func (c *Coordinator) setAppearance(mode string) {
	switch mode {
	case "System":
		c.prefs.Appearance = prefs.AppearanceSystem
	case "Dark":
		c.prefs.Appearance = prefs.AppearanceDark
	case "Light":
		c.prefs.Appearance = prefs.AppearanceLight
	default:
		return
	}
	_ = c.prefs.Save()
	c.invalidateFxWindows()
}

// shellFor returns the ShellState a window draws, falling back to the
// picker, welcome and preferences windows. It is what lets a menu
// accelerator act on the window that has focus.
// focusAttentionFocused resolves the window an accelerator came from to its
// session, then raises the next session that wants the user.
func (c *Coordinator) focusAttentionFocused(w *mygo.Window) {
	c.focusNextAttention(c.sessionForWindow(w))
}

func (c *Coordinator) shellFor(w *mygo.Window) *chrome.ShellState {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w != nil {
		for _, s := range c.sessions {
			if s.winSnapshot() == w {
				return &s.shell
			}
		}
	}
	if c.pickerWin != nil && w == c.pickerWin {
		return &c.pickerState.shell
	}
	if c.welcomeWin != nil && w == c.welcomeWin {
		return &c.welcomeState.shell
	}
	if c.prefsWin != nil && w == c.prefsWin {
		return &c.prefsShell
	}
	// No window identified (or the shortcut arrived without one): act on
	// the frontmost session rather than doing nothing.
	for _, s := range c.sessions {
		if w := s.winSnapshot(); w != nil && !w.IsDestroyed() {
			return &s.shell
		}
	}
	if len(c.sessions) > 0 {
		return &c.sessions[0].shell
	}
	return nil
}

func (c *Coordinator) toggleSidebar(w *mygo.Window) {
	sh := c.shellFor(w)
	if sh == nil {
		return
	}
	sh.SidebarOpen = !sh.SidebarOpen
	invalidateWin(w)
}

func (c *Coordinator) openPalette(w *mygo.Window) {
	sh := c.shellFor(w)
	if sh == nil {
		return
	}
	// A fresh ⌘K starts from every command rather than from whatever was
	// typed last time: the palette is a launcher, not a sticky search box.
	// The rule lives in chrome so that the magnifier obeys it too.
	chrome.OpenPalette(sh)
	invalidateWin(w)
}

func invalidateWin(w *mygo.Window) {
	if w != nil && !w.IsDestroyed() {
		w.Invalidate()
	}
}

func (c *Coordinator) runCommand(s *Session, id string) {
	switch id {
	case "workspace-tools":
		c.openManagement(s)
	case "open-folder":
		c.pickFolder(s.winSnapshot())
	case "new-session":
		if s.chat != nil {
			s.replaceConversation(nil)
		} else {
			c.showPicker()
		}
	case "continue-session":
		c.openSessionWithFlags(s.project, "-c")
	case "ask":
		s.shell.AskOpen = true
	case "session-settings":
		if s.chat != nil {
			s.openConfig("")
		}
	case "set-model":
		if s.chat != nil {
			s.openConfig("model")
		}
	case "draft-pr":
		c.openManagementPage(s, "GitHub")
		s.shell.Tools.GitHub.Kind = "pr"
	case "draft-issue":
		c.openManagementPage(s, "GitHub")
		s.shell.Tools.GitHub.Kind = "issue"
	case "show-credits":
		c.openManagementPage(s, "Account")
		c.runManagement(s, "credits")
	case "mcp-list":
		c.openManagementPage(s, "MCP")
		c.runManagement(s, "mcp-list")
	case "run-doctor":
		c.openManagementPage(s, "Details")
		c.runManagement(s, "doctor")
	case "toggle-console":
		c.toggleConsole(s)
	case "sign-in":
		// ACP has no sign-in method, so the palette's row opens the CLI
		// that does — the same flow the start screen points at.
		c.signIn(s)
	case "attach-image":
		c.pickImage(s)
	case "paste-image":
		c.pasteImage(s)
	case "check-updates":
		c.checkUpdates()
	case "open-config":
		c.openFxConfig()
	case "preferences":
		c.showPreferences()
	case "docs":
		_ = mygo.Shell.OpenExternal("https://fx.sh/docs")
	case "refresh-git":
		go s.refreshGit()
	case "review-changes":
		c.toggleReview(s)
	case "session-interrupt":
		// Ctrl-C, as typed in the terminal: fx owns what it interrupts.
		if s.chat != nil {
			s.chat.stopTurn()
		} else if s.term != nil {
			s.term.Send([]byte{0x03})
		}
	case "refresh-status":
		go s.refreshFxInfo()
	case "focus-attention":
		c.focusNextAttention(s)
	}
}

// commitChanges stages the working tree and commits it with the message
// typed in the changes panel.
//
// Two things matter here beyond the git calls themselves. First, `git add
// -A` stages everything in the tree, so it is a human-in-the-loop action:
// the user confirms before anything is staged. Second, git runs off the UI
// thread — a 30s commit must not freeze the window — and reports back
// through the app loop, where alone a dialog may be shown.
func (c *Coordinator) commitChanges(s *Session) {
	msg := strings.TrimSpace(s.shell.CommitMsg)
	if msg == "" {
		mygo.Dialog.Error("Commit", "Write a commit message first")
		return
	}
	n := 0
	s.mu.Lock()
	if s.git != nil {
		n = len(s.git.Changes)
	}
	s.mu.Unlock()
	detail := "This stages every change in the working tree and commits it."
	if n > 0 {
		detail = fmt.Sprintf("This stages all %d changed file(s) and commits them.", n)
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:    mygo.MessageQuestion,
		Title:   "Commit",
		Message: "Stage everything and commit?",
		Detail:  detail,
		Buttons: []string{"Commit", "Cancel"},
	})
	if err != nil || res.Button != 0 {
		return
	}
	go func() {
		out, err := runGitCommand(context.Background(), s.project, "add", "-A")
		if err == nil {
			out, err = runGitCommand(context.Background(), s.project, "commit", "-m", msg)
		}
		finalOut, finalErr := out, err
		mygo.App.WhenReady(func() {
			if finalErr != nil {
				mygo.Dialog.Error("Commit", firstLine(finalOut))
				return
			}
			s.shell.CommitMsg = ""
			go s.refreshGit()
		})
	}()
}

// runGitCommand runs one git command with a bounded lifetime. It never runs on
// the UI thread.
func runGitCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	return cmd.CombinedOutput()
}

// stageAll stages the working tree without committing.
//
// `git add -A` is recoverable (a reset undoes it), so it sits one rung
// below the irreversible actions — but it still stages everything, so it
// asks first rather than silently restaging files the user had kept out.
func (c *Coordinator) stageAll(s *Session) {
	n := 0
	s.mu.Lock()
	if s.git != nil {
		n = len(s.git.Changes)
	}
	s.mu.Unlock()
	detail := "Every change in the working tree will be staged."
	if n > 0 {
		detail = fmt.Sprintf("All %d changed file(s) will be staged.", n)
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:    mygo.MessageQuestion,
		Title:   "Stage all",
		Message: "Stage every change?",
		Detail:  detail,
		Buttons: []string{"Stage", "Cancel"},
	})
	if err != nil || res.Button != 0 {
		return
	}
	go func() {
		out, err := runGitCommand(context.Background(), s.project, "add", "-A")
		finalOut, finalErr := out, err
		mygo.App.WhenReady(func() {
			if finalErr != nil {
				mygo.Dialog.Error("Stage all", firstLine(finalOut))
				return
			}
			go s.refreshGit()
		})
	}()
}

// firstLine reports the first line of a command's output as a dialog
// message. The fallback is supplied by the caller: a command that printed
// nothing must still say which command failed rather than guess.
func firstLine(b []byte) string {
	return firstLineWith(b, "git failed")
}

func firstLineWith(b []byte, fallback string) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		s = fallback
	}
	return s
}

func (c *Coordinator) sidebarData(project string, welcome *welcomeModel) chrome.SidebarData {
	d := chrome.SidebarData{
		ProjectPath: project,
		FxVersion:   c.fxVersionSnapshot(),
		Recent:      c.recentPaths(),
	}
	if welcome != nil {
		d.Welcome = true
		d.WelcomeBusy = welcome.busy
		d.WelcomeText = welcome.status
		d.OnInstall = func() {
			welcome.busy = true
			welcome.status = "Downloading…"
			go c.runInstall()
		}
	}
	return d
}

// sessionData feeds the rail: the workspace's changes from git, fx's saved
// conversations, and the models the session can switch to.
func (c *Coordinator) sessionData(s *Session) chrome.SidebarData {
	d := chrome.SidebarData{
		ProjectPath: s.project,
		FxVersion:   c.fxVersionSnapshot(),
		Recent:      c.recentPaths(),
	}
	s.mu.Lock()
	if s.git != nil {
		d.Changes = s.git.Changes
	}
	d.Workspace = workspaceState(s.git, s.gitErr)
	now := time.Now()
	for _, x := range s.saved {
		d.Saved = append(d.Saved, chrome.SavedSession{
			ID:     x.ID,
			Title:  x.Title,
			When:   relTime(x.UpdatedAtMS, now),
			Turns:  x.HistoryLen,
			Active: false,
		})
	}
	s.mu.Unlock()
	if s.chat != nil {
		id := s.chat.status().SessionID
		for i := range d.Saved {
			d.Saved[i].Active = d.Saved[i].ID == id
		}
	}
	return d
}

// focusSession raises an open session's window. The shell keeps one window
// per session, so "focus" is a raise: the rail's active row is the window
// that already has focus.
func (c *Coordinator) focusSession(id string) {
	for _, s := range c.sessionsSnapshot() {
		if s.railID() != id {
			continue
		}
		if w := s.winSnapshot(); w != nil && !w.IsDestroyed() {
			w.Show()
			w.Focus()
		}
		return
	}
}

// focusNextAttention raises the top-most session that wants the user, which
// is what ⌘] and the palette's "Focus next session needing you" promise.
func (c *Coordinator) focusNextAttention(current *Session) {
	active := ""
	if current != nil {
		active = current.railID()
	}
	for _, sum := range c.sessionSummaries() {
		if !sum.Status.NeedsAttention() || sum.ID == active {
			continue
		}
		c.focusSession(sum.ID)
		return
	}
}

func sessionTitle(project string) string {
	base := filepath.Base(project)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "fx"
	}
	return "fx — " + base
}

func (c *Coordinator) newSession(w *mygo.Window) {
	for _, s := range c.sessionsSnapshot() {
		if w != nil && s.winSnapshot() == w && s.chat != nil {
			s.replaceConversation(nil)
			return
		}
	}
	c.showPicker()
}
