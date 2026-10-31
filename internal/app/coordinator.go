package app

import (
	"context"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/fxbin"
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
// a Session's own fields are guarded by its own mutex.
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

	sessions   []*Session
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
}

type pickerModel struct {
	status string
	shell  chrome.ShellState
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

// Setup registers menus, tray, and deep-link handlers.
func (c *Coordinator) Setup() {
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
	var pending string
	for _, arg := range os.Args[1:] {
		if path, ok := ProjectFromArg(arg, ""); ok {
			pending = path
			break
		}
	}
	if pending != "" {
		c.OpenProject(pending)
		return
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
	for _, arg := range args {
		if path, ok := ProjectFromArg(arg, wd); ok {
			c.OpenProject(path)
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
	path, err := fxbin.ResolvePath()
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
		c.mu.Lock()
		stale := c.fxGeneration != generation || c.fxPath != path
		if !stale {
			c.fxPath = ""
			c.fxVersion = ""
			c.fxBroken = true
		}
		c.mu.Unlock()
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
func (c *Coordinator) sessionFor(path string) *Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.sessions {
		if s.project == path {
			return s
		}
	}
	return nil
}

// sessionForWindow finds the session a menu accelerator should act on: the
// window the click came from, else the frontmost live session. Window reads
// go through winSnapshot — window-close callbacks may run concurrently.
func (c *Coordinator) sessionForWindow(w *mygo.Window) *Session {
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
func (c *Coordinator) sessionsSnapshot() []*Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Session, len(c.sessions))
	copy(out, c.sessions)
	return out
}

// addSession registers a session under the lock.
func (c *Coordinator) addSession(s *Session) {
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
	c.openProjectWithFlags(path)
}

func (c *Coordinator) openProjectWithFlags(path string, args ...string) {
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
		body = "The fx CLI at ~/.local/bin/fx could not be started. It may be damaged or incomplete — download it again to repair the install."
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
	_, err := fxbin.Install(ctx)
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
		c.refreshFx()
		c.welcomeState.status = "Installed."
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
				c.prefs = *p
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
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".fx")
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
