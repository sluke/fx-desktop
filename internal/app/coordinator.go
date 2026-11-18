package app

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/fxbin"
	"fx-desktop/internal/fxhome"
	"fx-desktop/internal/prefs"
	"fx-desktop/internal/projects"
	"fx-desktop/internal/sessionstats"
)

// Coordinator owns application state and windows.
//
// Concurrency contract: windows, menus and dialogs live on the app loop
// (the UI thread). Everything else — git reads, fx --json queries, install
// and upgrade — runs on its own goroutine and returns to the app loop via
// mygo.App.WhenReady. Fields reachable from both sides are guarded by mu;
// a window's own fields are guarded by its own mutex.
type Coordinator struct {
	mu sync.Mutex

	prefs    prefs.Prefs
	projects *projects.Store
	// stats is the app's record of each conversation's last usage. Every
	// window shares it; a chat session attaches it and writes only its own
	// session ID.
	stats *sessionstats.Store

	// CLI identity belongs to no single window: the app loop resolves the
	// path, a worker publishes its version, and session goroutines read both.
	fxPath       string
	fxVersion    string
	fxAuth       FxAuth
	fxGeneration uint64
	// fxBroken records that a resolved fx exists but refuses to run. The
	// setup screen then offers a fresh download instead of letting every
	// session fail against the broken binary.
	fxBroken bool
	// fxUnrunnable lists the paths a version query found unrunnable in this
	// run, newest last. Resolution avoids them when the managed install
	// offers another binary, and the setup screen names the newest.
	fxUnrunnable []string

	sessions []*ProjectWindow
	// sessionUse is which saved conversations this app may still write,
	// independent of which windows are visible (T9). History deletion and
	// conversation restore meet here.
	sessionUse sessionRegistry
	// windowSeq names windows for the app's lifetime. It is monotonic and
	// never reused, so a queued closure cannot address a later window.
	windowSeq  uint64
	pickerWin  *mygo.Window
	welcomeWin *mygo.Window
	prefsWin   *mygo.Window
	tray       *mygo.Tray
	rootMenu   *mygo.Menu

	menuRecent *mygo.MenuItem

	pickerState  pickerModel
	welcomeState welcomeModel
	prefsState   prefsModel
	prefsShell   chrome.ShellState
	// directoryPicking is UI-owned; asynchronous sheets must not stack if
	// a menu action is repeated while a folder selection is pending.
	directoryPicking bool

	// attention and badge remember what the last render pass told the Dock.
	// Both are written only from the render pass (Session.view), which runs
	// on the app loop, so neither needs mu.
	attention map[string]bool
	badge     int

	quitPhase quitPhase
	// quitBudget overrides quitFlushBudget for a test that must not wait the
	// production two seconds. Zero means the default.
	quitBudget time.Duration

	// historySeams replaces History deletion's I/O and dialogs in tests. The
	// zero value is production behaviour.
	historySeams historyDeleteSeams

	// confirmLink asks whether a folder named by a link may be opened. Nil
	// means the native dialog; a test supplies its own answer.
	confirmLink func(path string) bool
	// openProject opens a project. Nil means openProjectWithFlags; a test
	// records the request instead of creating a window.
	openProject func(path string)
}

// quitPhase names how far the app's single quit owner has got. A quit may be
// asked for many times — a second Cmd+Q, a tray item, a window — but only the
// draining → ready transition lets the process end (Q4).
type quitPhase int

const (
	// quitIdle is normal operation: nothing is being drained.
	quitIdle quitPhase = iota
	// quitDraining means the stats producers are being stopped and the store
	// is being flushed. Every quit request in this phase is held.
	quitDraining
	// quitReady means the final snapshot is settled (or its budget expired)
	// and the owner's own quit may proceed.
	quitReady
)

// quitFlushBudget bounds the whole drain: stopping producers, waiting for them
// to be reaped, and flushing the store. It matches the previous flush budget;
// a stuck disk gets a logged timeout rather than a hang.
const quitFlushBudget = 2 * time.Second

type pickerModel struct {
	shell chrome.ShellState
}

type welcomeModel struct {
	status  string
	busy    bool
	version string
	shell   chrome.ShellState
}

type prefsModel struct {
	p prefs.Prefs
}

// NewCoordinator loads persisted state.
func NewCoordinator() *Coordinator {
	p, err := prefs.Load()
	if err != nil {
		log.Println("prefs:", err)
		p = prefs.Defaults()
	}
	proj, err := projects.Load()
	if err != nil {
		log.Println("projects:", err)
		proj = &projects.Store{}
	}
	// Usage history is best-effort: a missing or damaged file must not keep
	// the app from starting, and Load always returns a usable store.
	stats, err := sessionstats.Load()
	if err != nil {
		log.Println("session stats:", err)
	}
	return &Coordinator{
		prefs:    p,
		projects: proj,
		stats:    stats,
	}
}

// beginQuit lets the quit callback return immediately, then drains on its own
// goroutine. While draining, every quit request is held: a user pressing Cmd+Q
// again must not skip the flush, and the app must not read "the flush has been
// asked for" as "the flush is done" (Q4).
//
// The owner's completion is the only transition to quitReady, and the only
// caller of quit. MyGo's App.Quit posts to the main thread, so the background
// goroutine may call it.
func (c *Coordinator) beginQuit(e interface{ PreventDefault() }, quit func()) {
	if c == nil || c.stats == nil || quit == nil {
		return
	}
	c.mu.Lock()
	phase := c.quitPhase
	if phase == quitIdle {
		c.quitPhase = quitDraining
	}
	c.mu.Unlock()

	// A quit that arrives while draining — or while the owner is already
	// ready — never starts a second drain. Draining holds it; ready is the
	// owner's own quit, so it may pass.
	if phase == quitDraining && e != nil {
		e.PreventDefault()
	}
	if phase != quitIdle {
		return
	}
	if e != nil {
		e.PreventDefault()
	}
	// Close admission on the app loop, then wait off it. Candidates are the
	// displayed chat, the conversation that actually owns the process barrier
	// and the one still waiting to be admitted: during a swap s.chat may
	// already be B while A is still producing stats (Q5).
	drain := c.stopStatsProducers()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), c.flushBudget())
		defer cancel()
		for _, done := range drain.wait {
			select {
			case <-done:
			case <-ctx.Done():
				// The budget is an explicit degradation boundary: a stuck
				// disk or process must not trap the app, and the final
				// snapshot is best-effort at this point.
				log.Println("session stats: producer still stopping at quit budget")
				goto snapshot
			}
		}
	snapshot:
		// Only after every producer is gone are the counters final. Close
		// Store afterwards, so the last accepted stats are actually written.
		for _, chat := range drain.chats {
			chat.snapshotStats()
		}
		if err := c.stats.Close(ctx); err != nil {
			log.Println("session stats:", err)
		}
		c.mu.Lock()
		c.quitPhase = quitReady
		c.mu.Unlock()
		quit()
	}()
}

// flushBudget is how long the drain may take: stopping producers, waiting for
// them, and closing the store share one deadline.
func (c *Coordinator) flushBudget() time.Duration {
	if c.quitBudget > 0 {
		return c.quitBudget
	}
	return quitFlushBudget
}

// statsDrain is what the quit owner must wait for before it may fix the last
// snapshot and close the store. wait has one signal per process teardown and
// one per stats producer group; chats are the sessions whose counters are
// still eligible to be persisted (Q5).
type statsDrain struct {
	wait  []<-chan struct{}
	chats []*chatSession
}

// stopStatsProducers closes new admission, withdraws replacements that are
// still waiting, and collects every producer that could still write session
// statistics. It runs on the app loop so it never races a conversation swap.
func (c *Coordinator) stopStatsProducers() statsDrain {
	var drain statsDrain
	seen := map[*chatSession]bool{}
	for _, s := range c.sessionsSnapshot() {
		if s == nil {
			continue
		}
		candidates := s.conversationCandidates()
		s.swap.cancelPending()
		for _, chat := range candidates {
			if chat == nil || seen[chat] {
				continue
			}
			seen[chat] = true
			drain.chats = append(drain.chats, chat)
			chat.Close()
			drain.wait = append(drain.wait, chat.closedDone)
			drain.wait = append(drain.wait, chat.producers.stop())
		}
	}
	return drain
}

// conversationCandidates is every chatSession this window may still own: the
// displayed one, the one holding the process barrier, and a request waiting
// to replace them. Reads happen on the app loop.
func (s *ProjectWindow) conversationCandidates() []*chatSession {
	s.swap.mu.Lock()
	started, waiting := s.swap.started, s.swap.waiting
	s.swap.mu.Unlock()
	return []*chatSession{s.chat, started, waiting}
}

// quitting reports whether the app has begun its final drain. New work is
// refused from that point: the store is about to close, so a prompt started
// now could not record its usage.
func (c *Coordinator) quitting() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quitPhase != quitIdle
}

// closeHidesWindow reports whether closing a project window hides it to the
// tray instead of closing it. That is the "Close window to menu bar" choice,
// and it stops applying once the app is quitting: MyGo's quit closes every
// window first and gives up when one refuses. A listener that always refused
// made Quit impossible while the option was on, and because the drain had
// already closed the conversations and the stats store, the app that stayed
// open could neither work nor quit.
func (c *Coordinator) closeHidesWindow() bool { return !c.quitting() }

// Setup registers menus, tray, and deep-link handlers.
func (c *Coordinator) Setup() {
	// Before-quit, not will-quit: MyGo destroys the windows before it fires
	// OnWillQuit, so draining there would leave the app running without a
	// window for up to the flush budget. Holding the quit before the windows
	// close keeps the app intact until the owner is ready (Q4).
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		c.beginQuit(e, mygo.App.Quit)
	})
	mygo.App.SetName("fx")
	_ = mygo.App.RegisterURLScheme("fx-desktop")
	// Clicking an attention notification raises the session it is about.
	// This is the one registration that also catches the click that
	// launched the app, which no Notification of this run can hold.
	mygo.App.OnNotificationClick(func(id string) {
		railID, ok := notificationRailID(id)
		if !ok {
			return
		}
		mygo.App.WhenReady(func() { c.focusSession(railID) })
	})
	c.setupMenus()
	c.setupTray()
}

// Launch handles startup after mygo is ready.
func (c *Coordinator) Launch() {
	if c.quitting() {
		return
	}
	c.refreshFx()
	if c.fxPathSnapshot() == "" {
		c.showWelcome()
		return
	}
	if c.prefs.CheckUpdatesOnStart {
		// Resolve on this thread: the goroutine below must not read
		// c.fxPath, which refreshFx rewrites on the app loop.
		fxPath := c.fxPathSnapshot()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), fxbin.DefaultHTTPTimeout)
			defer cancel()
			_ = fxbin.Upgrade(ctx, fxPath)
		}()
	}
	for _, arg := range os.Args[1:] {
		request, ok := ProjectRequestFromArg(arg, "")
		if !ok {
			continue
		}
		// A link the user turns down is not a reason to start with no
		// window: the launch goes on to the last project or the picker.
		if c.openRequest(request) {
			return
		}
		break
	}
	if c.prefs.RestoreLastProject && c.prefs.LastProject != "" {
		if st, err := os.Stat(c.prefs.LastProject); err == nil && st.IsDir() {
			c.OpenProject(c.prefs.LastProject)
			return
		}
	}
	c.showPicker()
}

// HandleSecondInstance opens projects from another instance's argv.
func (c *Coordinator) HandleSecondInstance(args []string, wd string) {
	// A deep link arriving during the final drain must not open a project.
	if c.quitting() {
		return
	}
	for _, arg := range args {
		if request, ok := ProjectRequestFromArg(arg, wd); ok {
			c.openRequest(request)
			return
		}
	}
	for _, s := range c.sessionsSnapshot() {
		if w := s.winSnapshot(); w != nil {
			w.Show()
			w.Focus()
			return
		}
	}
	if c.pickerWin != nil {
		c.pickerWin.Show()
		c.pickerWin.Focus()
	}
}

func (c *Coordinator) refreshFx() {
	// Resolving the executable only inspects files. Never start the CLI
	// here: Launch, OpenProject, installation and updates call this on the
	// app loop, and a stalled --version used to freeze every window.
	path, err := fxbin.ResolvePathAvoiding(c.fxUnrunnableList()...)
	c.mu.Lock()
	c.fxGeneration++
	generation := c.fxGeneration
	if err != nil {
		c.fxPath = ""
		c.fxVersion = ""
		c.mu.Unlock()
		return
	}
	if c.fxPath != path {
		c.fxVersion = ""
	}
	c.fxPath = path
	c.mu.Unlock()
	go func() {
		if c.refreshFxVersion(path, generation) {
			mygo.App.WhenReady(c.invalidateFxWindows)
		}
	}()
}

// refreshFxVersion never owns a window or holds a lock while waiting on fx.
// A later refresh supersedes this result even when the executable path is
// unchanged (e.g. an in-place upgrade).
func (c *Coordinator) refreshFxVersion(path string, generation uint64) bool {
	raw, err := fxbin.Version(path)
	if err != nil {
		log.Printf("fx version: %v", err)
		// A file that exists but cannot run is worse than a missing one:
		// the setup screen offers a repair, while a broken binary only
		// produces failed sessions. Treat it as not installed so the
		// user is offered the download that replaces it.
		//
		// A timeout is not that case: fx can be slow to start, and
		// claiming it is broken would send the user to reinstall a
		// perfectly good CLI.
		if !fxbin.Unrunnable(err) {
			return false
		}
		// A damaged fx found first (on PATH) must not hide a working
		// managed install: resolve once more, avoiding what has failed.
		// A candidate that already failed is not offered again, so two
		// damaged binaries cannot hand the retry back and forth. The
		// lookup touches the filesystem, so it runs before the lock.
		avoid := appendUnique(c.fxUnrunnableList(), path)
		next, nextErr := fxbin.ResolvePathAvoiding(avoid...)
		c.mu.Lock()
		stale := c.fxGeneration != generation || c.fxPath != path
		retry := false
		if !stale {
			c.fxUnrunnable = appendUnique(c.fxUnrunnable, path)
			if nextErr == nil && !containsString(avoid, next) {
				retry = true
			} else {
				c.fxPath = ""
				c.fxVersion = ""
				c.fxBroken = true
			}
		}
		c.mu.Unlock()
		if retry {
			mygo.App.WhenReady(c.refreshFx)
			return false
		}
		if !stale {
			// The setup screen is the only place that offers a working
			// CLI, so a broken one has to bring it up. The picker is
			// dismissed: it lists projects that cannot be opened while
			// fx is unusable.
			mygo.App.WhenReady(func() {
				c.invalidateFxWindows()
				c.closeStartupWindows()
				c.showWelcome()
			})
		}
		return false
	}
	// The auth state is a fact about this machine, not about a window, so
	// it is read once here and shared: the start screen can then say what
	// fx said without every session re-asking.
	auth := readFxAuth(path, "")
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fxGeneration != generation || c.fxPath != path {
		return false
	}
	c.fxVersion = fxVersionLabel(raw)
	c.fxAuth = auth
	c.fxBroken = false
	// A path that answered is not unrunnable, whatever an earlier query in
	// this run found (it may have been repaired or replaced in place).
	c.fxUnrunnable = withoutString(c.fxUnrunnable, path)
	return true
}

// invalidateFxWindows runs on the app loop; picker/setup references are
// UI-owned, unlike the session list and version snapshots.
func (c *Coordinator) invalidateFxWindows() {
	for _, w := range []*mygo.Window{c.pickerWin, c.welcomeWin, c.prefsWin} {
		invalidateWin(w)
	}
	for _, s := range c.sessionsSnapshot() {
		s.invalidate()
	}
}

// fxPathSnapshot is the goroutine-safe read that session refreshes use;
// refreshFx is the app-loop writer.
func (c *Coordinator) fxPathSnapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fxPath
}

// fxBrokenSnapshot reports that the resolved fx exists but cannot run.
func (c *Coordinator) fxBrokenSnapshot() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fxBroken
}

// fxUnrunnableSnapshot is the path that could not be started, for the setup
// screen to name.
func (c *Coordinator) fxUnrunnableSnapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.fxUnrunnable) == 0 {
		return ""
	}
	return c.fxUnrunnable[len(c.fxUnrunnable)-1]
}

// fxUnrunnableList is a copy of every path found unrunnable in this run.
func (c *Coordinator) fxUnrunnableList() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.fxUnrunnable...)
}

// forgetUnrunnable drops path from the unrunnable list: Install has just
// replaced that file, so what was learned about the old one no longer holds.
func (c *Coordinator) forgetUnrunnable(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fxUnrunnable = withoutString(c.fxUnrunnable, path)
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func appendUnique(list []string, value string) []string {
	if containsString(list, value) {
		return list
	}
	return append(list, value)
}

// withoutString returns list without value, in a slice of its own.
func withoutString(list []string, value string) []string {
	kept := list[:0:0]
	for _, item := range list {
		if item != value {
			kept = append(kept, item)
		}
	}
	return kept
}

// brokenFxBody is what the setup screen says about a fx that cannot run. It
// names the file that failed: the text used to say ~/.local/bin/fx whatever
// was resolved. With an override set, the download is not what the app will
// use, and the user has to be told so before pressing it.
func brokenFxBody(path string, overridden bool) string {
	if path == "" {
		path = "~/.local/bin/fx"
	}
	if overridden {
		return "FX_DESKTOP_FX_PATH names " + path + ", which could not be started. Point it at a working fx or unset it — a download installs ~/.local/bin/fx, which is not used while the variable is set."
	}
	return "The fx CLI at " + path + " could not be started. It may be damaged or incomplete — download it again to repair the install."
}

// installOutcome is what setup reports after a download: whether the app
// now has a fx to run, and the sentence to show. A successful download is
// not that by itself — resolution can still fail (an override naming a
// missing file), and saying "Installed." then sent the user round the setup
// screen again.
func installOutcome(target, resolved string, resolveErr error) (usable bool, status string) {
	if resolved != "" && resolveErr == nil {
		return true, "Installed."
	}
	if resolveErr != nil {
		return false, "fx was installed to " + target + ", but it cannot be used: " + resolveErr.Error()
	}
	return false, "fx was installed to " + target + ", but it cannot be used."
}

// fxVersionSnapshot is the goroutine-safe read of the display version.
func (c *Coordinator) fxVersionSnapshot() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fxVersion
}

// fxAuthNotice is the goroutine-safe read of fx's sign-in state, in the shape
// the shell renders.
func (c *Coordinator) fxAuthNotice() chrome.AuthNotice {
	c.mu.Lock()
	defer c.mu.Unlock()
	return chrome.AuthNotice{Auth: c.fxAuth.Auth, Help: c.fxAuth.Help, Providers: c.fxAuth.Providers}
}

// sessionFor finds the session that owns a project path, if any.
func (c *Coordinator) sessionFor(path string) *ProjectWindow {
	sessions := c.sessionsSnapshot()
	for _, s := range sessions {
		if s.project == path {
			return s
		}
	}
	// The same folder can arrive under another spelling: through a symbolic
	// link, or in another letter case on a case-insensitive volume. Compared
	// as strings it opened a second window, and with it a second fx writing
	// the same workspace — what the one-window rule exists to prevent.
	//
	// Only the path being asked about is read here, outside the lock; every
	// caller has just read it anyway. Each window's identity was recorded
	// when it opened, so a window whose volume has stopped answering cannot
	// hold up opening another project.
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	for _, s := range sessions {
		if s.projectInfo != nil && os.SameFile(info, s.projectInfo) {
			return s
		}
	}
	return nil
}

// sessionForWindow finds the session a menu accelerator should act on: the
// window the click came from, else the frontmost live session. Window reads
// go through winSnapshot — window-close callbacks may run concurrently.
func (c *Coordinator) sessionForWindow(w *mygo.Window) *ProjectWindow {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w != nil {
		for _, s := range c.sessions {
			if s.winSnapshot() == w {
				return s
			}
		}
	}
	for _, s := range c.sessions {
		if win := s.winSnapshot(); win != nil && !win.IsDestroyed() {
			return s
		}
	}
	return nil
}

// interruptFocused runs the interrupt command against the window the ⌘.
// accelerator came from; chat sessions cancel the turn, terminals get ^C.
func (c *Coordinator) interruptFocused(w *mygo.Window) {
	if s := c.sessionForWindow(w); s != nil {
		c.runCommand(s, "session-interrupt")
	}
}

// sessionCount guards the session list, which the app loop appends to and
// window-close callbacks remove from.
func (c *Coordinator) sessionCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sessions)
}

// sessionsSnapshot copies the session list under the lock. Callers that
// only iterate — focus-all, repaint-all, second-instance handling — use it
// so a window closing on another thread cannot mutate the slice mid-walk.
func (c *Coordinator) sessionsSnapshot() []*ProjectWindow {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*ProjectWindow, len(c.sessions))
	copy(out, c.sessions)
	return out
}

// addSession registers a session under the lock.
func (c *Coordinator) addSession(s *ProjectWindow) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = append(c.sessions, s)
}

// fxVersionLabel normalises the CLI's version output to the form the shell
// displays everywhere: "fx v0.0.13". The CLI may answer "0.0.13", "v0.0.13"
// or a full banner depending on build.
func fxVersionLabel(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	low := strings.ToLower(v)
	switch {
	case strings.HasPrefix(low, "fx v"), strings.HasPrefix(low, "fx "):
		return v
	case strings.HasPrefix(low, "v"):
		return "fx " + v
	default:
		return "fx v" + v
	}
}

// OpenProject opens or focuses a project in a new session window.
func (c *Coordinator) OpenProject(path string) {
	if c.openProject != nil {
		c.openProject(path)
		return
	}
	c.openProjectWithFlags(path)
}

// openRequest opens what a launch argument asked for and reports whether a
// project was opened.
func (c *Coordinator) openRequest(request ProjectRequest) bool {
	if request.FromLink {
		return c.OpenProjectFromLink(request.Path)
	}
	c.OpenProject(request.Path)
	return true
}

// OpenProjectFromLink opens a folder an fx-desktop:// link names, and reports
// whether it did.
//
// Opening a project is not a passive act: fx is started in the folder and
// its Git state is read, and Git runs commands the folder's own configuration
// names (core.fsmonitor on status, diff.external and filters on diff). A link
// can be followed from a web page, so a folder the user has never opened here
// is opened only after they have seen its path and agreed. A folder that is
// already open, or is among the recent projects, was opened by the user
// before and needs no question.
func (c *Coordinator) OpenProjectFromLink(path string) bool {
	if c.quitting() {
		return false
	}
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return false
	}
	if !linkedProjectKnown(path, c.sessionFor(path) != nil, c.prefs.LastProject, c.recentPaths()) {
		confirm := c.confirmLink
		if confirm == nil {
			confirm = confirmLinkedProject
		}
		if !confirm(path) {
			return false
		}
	}
	c.OpenProject(path)
	return true
}

// linkedProjectKnown reports whether a folder named by a link is one the user
// has already opened in this app: it has a window, it is the last project, or
// it is in the recent list. Paths are compared as written; the filesystem is
// not asked, so a list entry on a volume that has gone away costs nothing.
func linkedProjectKnown(path string, open bool, last string, recents []string) bool {
	if open {
		return true
	}
	if last != "" && filepath.Clean(last) == path {
		return true
	}
	for _, recent := range recents {
		if filepath.Clean(recent) == path {
			return true
		}
	}
	return false
}

// confirmLinkedProject shows the folder a link wants opened and asks. Cancel
// is the default and the answer to Escape.
func confirmLinkedProject(path string) bool {
	result, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:          mygo.MessageWarning,
		Title:         "Open folder from link",
		Message:       "A link asked fx to open this folder:\n\n" + path,
		Detail:        "Opening a folder starts fx in it and reads its Git status, which can run commands that the folder's own Git configuration names. Open it only if you trust where it came from.",
		Buttons:       []string{"Cancel", "Open Folder"},
		DefaultButton: 0,
		CancelButton:  0,
	})
	return err == nil && result.Button == 1
}

func (c *Coordinator) openProjectWithFlags(path string, args ...string) {
	if c.quitting() {
		return
	}
	path = filepath.Clean(path)
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() {
		mygo.Dialog.Error("Open project", "Not a directory: "+path)
		return
	}
	if c.fxPathSnapshot() == "" {
		c.refreshFx()
	}
	if c.fxPathSnapshot() == "" {
		c.showWelcome()
		return
	}
	if c.prefs.TrackRecentProjects {
		c.projects.Touch(path)
		_ = c.projects.Save()
		c.rebuildRecentMenu()
	}
	c.prefs.LastProject = path
	_ = c.prefs.Save()

	// One window per project: two fx processes sharing a workspace would
	// both read and rewrite the same tree. Reopening focuses the window
	// that already owns it.
	if s := c.sessionFor(path); s != nil {
		if w := s.winSnapshot(); w != nil && !w.IsDestroyed() {
			if len(args) > 0 {
				s.replaceConversation(args)
			}
			w.Show()
			w.Focus()
			c.closeStartupWindows()
			return
		}
	}

	// MyGo quits when the last window closes. Create the replacement first,
	// and retain the picker/setup window if the terminal cannot be created.
	if c.openSessionWithFlags(path, args...) == nil {
		return
	}
	c.closeStartupWindows()
}

func (c *Coordinator) closeStartupWindows() {
	if c.welcomeWin != nil && !c.welcomeWin.IsDestroyed() {
		c.welcomeWin.Close()
		c.welcomeWin = nil
	}
	if c.pickerWin != nil && !c.pickerWin.IsDestroyed() {
		c.pickerWin.Close()
		c.pickerWin = nil
	}
}

func (c *Coordinator) pickFolder(parent *mygo.Window) {
	// UI actions run inside the native surface's drawing callback. A
	// synchronous dialog pumps events but cannot finish that draw, leaving
	// its sheet invisible and the app apparently frozen. Queue the whole
	// interaction so the drawing callback returns before opening the panel.
	mygo.App.WhenReady(func() { c.pickFolderOnMain(parent) })
}

// pickDirectory asks for one folder and hands it back on the UI thread.
// Callers leave the drawing callback before snapshotting UI-owned state.
// The public Dialog.Open wrapper creates the native panel on the UI thread
// while a worker waits for it, rather than nesting an event loop there.
func (c *Coordinator) pickDirectory(parent *mygo.Window, title string, onPick func(string)) {
	if onPick == nil {
		return
	}
	mygo.App.WhenReady(func() { c.pickDirectoryOnMain(parent, title, onPick) })
}

func (c *Coordinator) pickDirectoryOnMain(parent *mygo.Window, title string, onPick func(string)) {
	if onPick == nil || c.directoryPicking {
		return
	}
	if parent != nil && parent.IsDestroyed() {
		return
	}
	def := c.prefs.DefaultProjectDir
	if def == "" {
		def, _ = os.UserHomeDir()
	}
	c.directoryPicking = true
	options := mygo.OpenDialogOptions{
		Parent:            parent,
		Title:             title,
		DefaultPath:       def,
		Directory:         true,
		CreateDirectories: true,
	}
	requestDirectoryDialog(options, mygo.Dialog.Open, mygo.App.WhenReady, func(paths []string, err error) {
		c.directoryPicking = false
		if parent != nil && parent.IsDestroyed() {
			return
		}
		if err != nil {
			mygo.Dialog.Error("Open folder", err.Error())
			return
		}
		if len(paths) != 0 {
			onPick(paths[0])
		}
	})
}

func (c *Coordinator) pickFolderOnMain(parent *mygo.Window) {
	c.pickDirectoryOnMain(parent, "Open project folder", func(path string) { c.OpenProject(path) })
}

// chooseDefaultProjectDir fills the Preferences field with a folder the
// user picked, instead of asking them to type a path by hand.
func (c *Coordinator) chooseDefaultProjectDir() {
	c.pickDirectory(c.prefsWin, "Choose default project folder", func(dir string) {
		c.prefsState.p.DefaultProjectDir = dir
		if c.prefsWin != nil {
			c.prefsWin.Invalidate()
		}
	})
}

func (c *Coordinator) showPicker() {
	if c.pickerWin != nil && !c.pickerWin.IsDestroyed() {
		c.pickerWin.Show()
		c.pickerWin.Focus()
		return
	}
	c.pickerState.shell = chrome.DefaultShell()
	c.pickerWin = mygo.NewWindow(mygo.WindowOptions{
		Title:                "fx",
		Width:                1280,
		Height:               800,
		MinWidth:             720,
		MinHeight:            480,
		TitleBarStyle:        mygo.TitleBarHidden,
		TitleBarHeight:       chrome.TitleBarHeight,
		TrafficLightPosition: windowTrafficLights(),
		Content:              ui.View(c.pickerView),
	})
	c.pickerWin.OnClosed(func() { c.pickerWin = nil })
}

func (c *Coordinator) pickerView(ctx *ui.Context) {
	ver := c.fxVersionSnapshot()
	sub := "Open a folder to run the fx agent."
	if ver != "" {
		sub = ver + " · " + sub
	}
	chrome.Frame(ctx, chrome.Host{
		Appearance:  string(c.prefs.Appearance),
		Title:       "fx — Open a project",
		CenterOnly:  true,
		Subtitle:    sub,
		Auth:        c.fxAuthNotice(),
		StatusLeft:  c.centerStatusLeft(),
		StatusRight: []string{strconv.Itoa(len(c.recentPaths())) + " recent"},
		Actions:     c.chromeActions(c.pickerWin),
		Data:        c.sidebarData("", nil),
		Shell:       &c.pickerState.shell,
		Main: func(inner *ui.Context) {
			lede := "Select a folder. fx runs the agent in that directory, with its own session window and context."
			if ver != "" {
				lede = ver + " installed · " + lede
			}
			_, height := inner.Size()
			ui.Scroll(inner).Fill().Children(func() {
				ui.Column(inner).FillWidth().MinHeight(height-chrome.TitleBarHeight-24).Center().Padding(36, 24).Children(func() {
					ui.Column(inner).FillWidth().MaxWidth(560).Gap(20).AlignItems(ui.Start).Children(func() {
						ui.Row(inner).Width(34).Height(34).Center().Radius(5).Background(chrome.SurfaceColor(inner)).BorderWidth(1).BorderColor(chrome.EdgeColor(inner)).Children(func() {
							ui.Text(inner, "fx").Font(chrome.MonoFont).FontSize(13).FontWeight(600).TextColor(chrome.InkStrongColor(inner))
						})
						ui.Column(inner).FillWidth().Gap(10).Children(func() {
							ui.Text(inner, "Open a project").Font(chrome.SansFont).FontSize(26).FontWeight(500).LetterSpacing(-0.6).TextColor(chrome.InkStrongColor(inner))
							ui.Text(inner, lede).FontSize(13).TextColor(chrome.InkMutedColor(inner))
						})
						chrome.PrimaryButton(inner, "Choose folder…", func() { c.pickFolder(c.pickerWin) })
						if recents := c.recentPaths(); len(recents) > 0 {
							ui.Text(inner, "Recent projects").Font(chrome.MonoFont).FontSize(9).FontWeight(500).LetterSpacing(0.6).TextColor(chrome.InkMutedColor(inner))
							chrome.RecentGrid(inner, recents, func(path string) { mygo.App.WhenReady(func() { c.OpenProject(path) }) })
						}
					})
				})
			})
		},
	})
}

// centerStatusLeft feeds the status bar of the picker and welcome windows.
func (c *Coordinator) centerStatusLeft() []string {
	if v := c.fxVersionSnapshot(); v != "" {
		return []string{v}
	}
	return []string{"fx"}
}

func (c *Coordinator) showWelcome() {
	if c.welcomeWin != nil && !c.welcomeWin.IsDestroyed() {
		c.welcomeWin.Show()
		return
	}
	c.welcomeState.version = c.fxVersionSnapshot()
	c.welcomeState.shell = chrome.DefaultShell()
	c.welcomeWin = mygo.NewWindow(mygo.WindowOptions{
		Title:                "fx — Setup",
		Width:                1280,
		Height:               800,
		MinWidth:             720,
		MinHeight:            480,
		TitleBarStyle:        mygo.TitleBarHidden,
		TitleBarHeight:       chrome.TitleBarHeight,
		TrafficLightPosition: windowTrafficLights(),
		Content:              ui.View(c.welcomeView),
	})
	c.welcomeWin.OnClosed(func() { c.welcomeWin = nil })
}

func (c *Coordinator) welcomeView(ctx *ui.Context) {
	w := &c.welcomeState
	// A binary that exists but cannot run is a different situation from one
	// that was never installed, and the screen should say so: the fix is the
	// same download, but the user did not ask for it.
	broken := c.fxBrokenSnapshot()
	var steps []chrome.Step
	if w.busy {
		steps = []chrome.Step{
			{Title: "Resolve release", Note: "github.com/vercel-labs/fx · latest", State: "done"},
			{Title: "Download asset", Note: "fx release archive", State: "done"},
			{Title: "Extract & chmod 755", Note: w.status, State: "active"},
			{Title: "Pick a project", Note: "open a folder to start", State: ""},
		}
	} else {
		steps = []chrome.Step{
			{Title: "Resolve release", Note: "github.com/vercel-labs/fx · latest"},
			{Title: "Download asset", Note: "latest release archive"},
			{Title: "Verify checksum", Note: "release sha256"},
			{Title: "Extract & chmod 755", Note: "~/.local/bin/fx"},
			{Title: "Pick a project", Note: "open a folder to start"},
		}
	}
	subtitle := "Install fx to continue"
	statusLeft := "fx not installed"
	headline := "Install the fx CLI"
	body := "fx Desktop runs the agent in a native conversation window. The CLI is not installed yet — download it to ~/.local/bin/fx to continue."
	if broken {
		subtitle = "Repair fx to continue"
		statusLeft = "fx cannot run"
		headline = "Repair the fx CLI"
		body = brokenFxBody(c.fxUnrunnableSnapshot(), fxbin.Overridden())
	}
	chrome.Frame(ctx, chrome.Host{
		Appearance:  string(c.prefs.Appearance),
		Title:       "fx — Setup",
		CenterOnly:  true,
		Subtitle:    subtitle,
		StatusLeft:  []string{statusLeft},
		StatusRight: []string{"setup"},
		Actions:     c.chromeActions(c.welcomeWin),
		Data:        c.sidebarData("", w),
		Shell:       &w.shell,
		Main: func(inner *ui.Context) {
			label := "Download fx"
			secondary := []struct {
				Label   string
				OnClick func()
			}{
				{"Documentation", func() { _ = mygo.Shell.OpenExternal("https://fx.sh/docs/getting-started/installation") }},
			}
			if w.busy {
				// While busy the checklist carries the progress; no buttons.
				label = ""
				secondary = nil
			}
			chrome.CenterSteps(inner, "01 / SETUP", headline, body,
				steps,
				label, func() {
					w.busy = true
					w.status = "Downloading…"
					if c.welcomeWin != nil {
						c.welcomeWin.Invalidate()
					}
					go c.runInstall()
				},
				secondary)
		},
	})
}

func (c *Coordinator) runInstall() {
	ctx, cancel := context.WithTimeout(context.Background(), fxbin.DefaultHTTPTimeout)
	defer cancel()
	target, err := fxbin.Install(ctx)
	mygo.App.WhenReady(func() {
		c.welcomeState.busy = false
		if err != nil {
			c.welcomeState.status = err.Error()
			if c.welcomeWin != nil {
				c.welcomeWin.Invalidate()
			}
			return
		}
		// Publish the installed path and query its version in the background.
		// Direct field writes here would bypass the generation/race guards.
		c.forgetUnrunnable(target)
		c.refreshFx()
		_, resolveErr := fxbin.ResolvePathAvoiding(c.fxUnrunnableList()...)
		usable, status := installOutcome(target, c.fxPathSnapshot(), resolveErr)
		c.welcomeState.status = status
		if !usable {
			if c.welcomeWin != nil {
				c.welcomeWin.Invalidate()
			}
			return
		}
		// Keep a window alive throughout setup → picker, for the same reason
		// OpenProject opens its session before closing the startup windows.
		c.showPicker()
		if c.welcomeWin != nil {
			c.welcomeWin.Close()
			c.welcomeWin = nil
		}
	})
}

func (c *Coordinator) showPreferences() {
	if c.prefsWin != nil && !c.prefsWin.IsDestroyed() {
		c.prefsWin.Show()
		c.prefsWin.Focus()
		return
	}
	c.prefsState.p = c.prefs
	c.prefsShell = chrome.DefaultShell()
	c.prefsWin = mygo.NewWindow(mygo.WindowOptions{
		Title:                "Preferences",
		TitleBarStyle:        mygo.TitleBarHidden,
		TitleBarHeight:       chrome.TitleBarHeight,
		TrafficLightPosition: windowTrafficLights(),
		MinWidth:             520,
		MinHeight:            480,
		Width:                520,
		Height:               560,
		Content:              ui.View(c.prefsView),
	})
	c.prefsWin.OnClosed(func() { c.prefsWin = nil })
}

func (c *Coordinator) prefsView(ctx *ui.Context) {
	p := &c.prefsState.p
	chrome.Frame(ctx, chrome.Host{
		// Preview the draft, not the saved value: choosing Dark or Light has
		// to change the window now. Save still decides what every other
		// window and the next launch use.
		Appearance: string(p.Appearance),
		Title:      "Preferences",
		CenterOnly: true,
		StatusLeft: c.centerStatusLeft(),

		Actions: c.chromeActions(c.prefsWin),
		Shell:   &c.prefsShell,
		Main: func(inner *ui.Context) {
			drawPrefs(inner, p, func() { c.chooseDefaultProjectDir() }, func() {
				c.prefs = mergePrefsDraft(c.prefs, *p)
				_ = c.prefs.Save()
				c.invalidateFxWindows()
				if c.prefsWin != nil {
					c.prefsWin.Close()
				}
			}, func() {
				*p = c.prefs
			})
		},
	})
}

// mergePrefsDraft applies the Preferences draft to the saved preferences. The
// draft is a copy taken when the window opened, and LastProject is not one of
// the fields the window edits: a project opened meanwhile must not be
// replaced by the one that was current when the window opened.
func mergePrefsDraft(saved, draft prefs.Prefs) prefs.Prefs {
	draft.LastProject = saved.LastProject
	return draft
}

// drawPrefs shares the shell's flat surfaces, compact type and control sizes.
// Values stay in the draft until Save; Revert restores the saved preferences.
// Appearance is previewed in this window as soon as it is chosen.
func drawPrefs(c *ui.Context, p *prefs.Prefs, onChooseFolder, onSave, onRevert func()) {
	ink, muted := chrome.InkColor(c), chrome.InkMutedColor(c)
	hairline := chrome.HairlineColor(c)
	button := func(label string) ui.Element {
		return ui.Button(c, label).FontSize(12).Height(28).Padding(0, 10).Radius(7).Shrink(0)
	}
	label := func(name, desc string) {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, name).FontSize(12).TextColor(ink).Wrap()
			ui.Text(c, desc).FontSize(11).TextColor(muted).Wrap()
		})
	}
	ui.Column(c).Fill().Children(func() {
		ui.Scroll(c).Grow(1).FillWidth().Label("Preferences settings").Children(func() {
			ui.Column(c).FillWidth().MaxWidth(720).Padding(16, 20).Gap(20).Children(func() {
				group := func(name string, body func()) {
					c.Key("prefs:" + name)
					ui.Column(c).FillWidth().Children(func() {
						ui.Text(c, name).Font(chrome.MonoFont).FontSize(11).FontWeight(600).LetterSpacing(0.3).TextColor(muted).Padding(0, 0, 8, 0)
						body()
					})
				}
				toggle := func(name, desc string, value *bool, last bool) {
					c.Key(name)
					row := ui.Row(c).FillWidth().Padding(10, 0).Gap(20).AlignItems(ui.Center)
					if !last {
						row.BorderWidth(0, 0, 1, 0).BorderColor(hairline)
					}
					row.Children(func() {
						label(name, desc)
						ui.Switch(c, value).Label(name).Shrink(0)
					})
				}
				fieldHeader := func(name, desc string) {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, name).FontSize(12).TextColor(ink).Shrink(0)
						ui.Text(c, desc).FontSize(11).TextColor(muted).SingleLine().MinWidth(0)
					})
				}
				group("General", func() {
					ui.Column(c).FillWidth().Gap(6).Padding(0, 0, 10, 0).
						BorderWidth(0, 0, 1, 0).BorderColor(hairline).Children(func() {
						fieldHeader("Default project folder", "Where the project picker starts")
						ui.Row(c).FillWidth().Gap(8).Children(func() {
							ui.TextInput(c, &p.DefaultProjectDir).Label("Project folder path").FontSize(12).
								Height(28).Padding(5, 8).Grow(1).MinWidth(0)
							if onChooseFolder != nil {
								button("Choose…").OnClick(onChooseFolder)
							}
						})
					})
					toggle("Restore last project", "Reopen the last project on launch", &p.RestoreLastProject, false)
					toggle("Track recent projects", "Keep up to 20 recent projects", &p.TrackRecentProjects, false)
					toggle("Close window to menu bar", "Hide to the tray instead of quitting", &p.CloseToTray, true)
				})
				group("Appearance", func() {
					ui.Row(c).FillWidth().Padding(10, 0).Gap(20).AlignItems(ui.Center).Children(func() {
						label("Theme", "System, dark or light appearance")
						ui.RadioGroup(c, func() {
							for _, option := range []struct {
								name  string
								value prefs.Appearance
							}{
								{"System", prefs.AppearanceSystem}, {"Dark", prefs.AppearanceDark}, {"Light", prefs.AppearanceLight},
							} {
								c.Key(option.value)
								segment := ui.RadioBase(c, &p.Appearance, option.value).Label(option.name).
									Height(26).Padding(0, 9).Radius(6).AlignItems(ui.Center).Justify(ui.Center)
								if p.Appearance == option.value {
									segment.Background(c.Theme().Background)
								}
								segment.Children(func() { ui.Text(c, option.name).FontSize(11).TextColor(ink).SingleLine() })
							}
						}).Row().Gap(1).Padding(1).Radius(7).Background(c.Theme().SurfacePressed).Shrink(0).Label("Theme")
					})
				})
				group("Terminal", func() {
					ui.Column(c).FillWidth().Gap(6).Padding(0, 0, 10, 0).
						BorderWidth(0, 0, 1, 0).BorderColor(hairline).Children(func() {
						fieldHeader("Font family", "Command terminals; empty = SF Mono")
						ui.TextInput(c, &p.FontFamily).Label("Terminal font family").FontSize(12).
							Height(28).Padding(5, 8).FillWidth()
					})
					ui.Row(c).FillWidth().Padding(10, 0).Gap(20).AlignItems(ui.Center).Children(func() {
						label("Font size", "10–24 pt · new command windows")
						ui.Row(c).Width(180).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Slider(c, &p.FontSize, 10, 24).Label("Terminal font size").Grow(1)
							ui.Text(c, strconv.Itoa(int(p.FontSize+0.5))).FontSize(11).TextColor(muted).Width(20)
						})
					})
					ui.Text(c, "Conversations use the app's font.").FontSize(11).TextColor(muted)
				})
				group("Updates", func() {
					toggle("Check on launch", "Look for newer fx releases on launch", &p.CheckUpdatesOnStart, true)
				})
			})
		})
		ui.Row(c).Shrink(0).FillWidth().Height(52).Padding(0, 20).Gap(8).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(hairline).Children(func() {
			ui.Text(c, "Changes apply when saved.").FontSize(11).TextColor(muted)
			ui.Box(c).Grow(1)
			button("Revert").OnClick(onRevert)
			chrome.PrimaryButton(c, "Save", onSave).FontSize(12).Height(28).Padding(0, 14).Radius(7).Shrink(0)
		})
	})
}

func (c *Coordinator) openFxConfig() {
	dir, err := fxhome.Root()
	if err != nil {
		mygo.Dialog.Error("fx", err.Error())
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = mygo.Shell.OpenPath(dir)
}

func (c *Coordinator) checkUpdates() {
	if c.fxPathSnapshot() == "" {
		c.refreshFx()
	}
	fxPath := c.fxPathSnapshot()
	if fxPath == "" {
		mygo.Dialog.Error("fx", "fx is not installed")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fxbin.DefaultHTTPTimeout)
		defer cancel()
		err := fxbin.Upgrade(ctx, fxPath)
		mygo.App.WhenReady(func() {
			if err != nil {
				mygo.Dialog.Error("Update failed", err.Error())
				return
			}
			c.refreshFx()
			_, _ = mygo.Dialog.Message(mygo.MessageOptions{
				Type:    mygo.MessageInfo,
				Title:   "fx",
				Message: "Update finished",
				Detail:  "New sessions use the updated CLI.",
			})
		})
	}()
}

func (c *Coordinator) showAllWindows() {
	for _, s := range c.sessionsSnapshot() {
		if w := s.winSnapshot(); w != nil {
			w.Show()
			w.Focus()
			return
		}
	}
	if c.pickerWin != nil {
		c.pickerWin.Show()
		c.pickerWin.Focus()
	}
}

// nextWindowID hands out the next window identity.
func (c *Coordinator) nextWindowID() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.windowSeq++
	return c.windowSeq
}
