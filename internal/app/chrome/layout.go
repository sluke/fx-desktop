package chrome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// projectAgentCommands is where the palette's "Project & agent" group ends
// and its "View" group begins. It is a constant rather than a magic index
// because the command table is sliced by it.
const projectAgentCommands = 19

// Change is one modified path in the rail's changes section (and, later, the
// review drawer).
type Change struct {
	Path string
	Kind string // "M", "A", "D", "R" or "?"
	Adds int
	Dels int
}

// SavedSession is one conversation fx saved for the workspace, read from
// `fx sessions --json`. Resuming uses the owning project's window.
type SavedSession struct {
	ID     string
	Title  string
	When   string
	Turns  int
	Active bool
}

// Agent is the live state of the embedded fx process. Everything shown here
// comes from fx itself — `fx status --json`, `fx permissions --json` and
// `fx usage --json`. A zero field hides its stat rather than showing a guess.
type Agent struct {
	Status       string // Running, Exited, Attention
	Model        string // fx status: model
	Permission   string // fx permissions: mode (ask, auto, full-access)
	Tokens       string // this conversation's input+output tokens, compacted
	Spend        string // fx usage: spend
	Elapsed      string // session uptime
	Branch       string // git branch
	ContextUsed  int
	ContextTotal int
}

// SessionBar is the context strip above the terminal: which project, which
// branch, how many live windows.
type SessionBar struct {
	Name    string
	Branch  string
	Count   int
	Running bool
}

// Step is one row of a setup checklist.
type Step struct {
	Title string
	Note  string
	State string // "", "done", "active" or "failed"
}

// ShellState is the per-window UI state of the shell.
type ShellState struct {
	History          HistoryState
	HistoryCollapsed bool
	Tools            ManagementState
	SidebarOpen      bool
	SidebarWidth     float32
	PaletteOpen      bool
	PaletteQuery     string
	// PaletteHighlight is the row the arrows are on, as an index into the
	// rows the query leaves; -1 is none, which is where a palette that has
	// just opened starts. PaletteScrolled is the row the list was last
	// brought into view for, so the wheel can scroll on past it, and
	// PaletteHover is the row the pointer was on, so entering a row moves
	// the highlight once rather than on every frame it is rested on.
	PaletteHighlight int
	PaletteScrolled  string
	PaletteHover     string
	CommitMsg        string
	AskOpen          bool
	AskPrompt        string
	// Quick ask is a native form over fx's one-shot surface. The output is
	// parsed from fx ask --json and shown in this sheet, not in a terminal.
	AskBusy             bool
	AskOutput           string
	AskMeta             string
	AskProblem          string
	AskModel            string
	AskEffort           string
	AskAccess           string
	AskFast             string
	AskUltra            string
	AskSystem           string
	AskProviderOrder    string
	AskImages           string
	AskSession          string
	AskAddDirs          string
	AskContextLimits    string
	AskStrict           bool
	AskNoSave           bool
	AskNoAdditionalDirs bool
	AskGeneration       uint64
	// ReviewOpen shows the review drawer, and ReviewPath is the file whose
	// diff it has open (§4.3). Both are app state: ⌘R, the rail entry and the
	// status strip all flip the same flag rather than keeping one each.
	ReviewOpen bool
	ReviewPath string
	// ReviewFiles is the review drawer's file index. It is virtual: a tree
	// can hold thousands of paths, and only the visible rows may be built
	// (§4.10.1).
	ReviewFiles ui.ListState
	// ConsoleOpen shows the console drawer, and ConsoleRun is the run whose
	// terminal it draws (§4.7). Both are app state for the same reason: ⌘J,
	// the status strip, the palette and a run that just started all move one
	// flag rather than keeping one each.
	ConsoleOpen bool
	ConsoleRun  uint64
	// InspectorOpen shows the right-hand telemetry panel. It is on by
	// default and gives way to the review drawer and to windows that are
	// too narrow to keep a usable conversation beside it.
	InspectorOpen bool
	// InspectorTab chooses what the right-hand panel shows: the telemetry
	// readout or the workspace file browser. It is app state, so the tab
	// survives a repaint and every way in (the tab bar itself) agrees.
	InspectorTab int
	// FileRows is the file browser's list state. The panel virtualizes its
	// rows, so a folder with hundreds of entries builds only what shows,
	// and an expanded tree keeps its scroll position across repaints.
	FileRows ui.ListState
}

func DefaultShell() ShellState {
	return ShellState{
		SidebarOpen:   true,
		SidebarWidth:  float32(sidebarDefaultWide),
		InspectorOpen: true,
		// No row is highlighted until the arrows or typing pick one, which
		// is what makes the first Down land on the first row.
		PaletteHighlight: -1,
	}
}

// OpenPalette puts the ⌘K sheet up from a clean slate: a fresh palette starts
// from every command rather than from whatever was typed last time, and from no
// highlight rather than from the row the last visit left. Every way in — the
// key, the menu, the title bar's magnifier — comes through here, or one of them
// reopens onto the last search and filters the list before a key is pressed.
func OpenPalette(sh *ShellState) {
	sh.PaletteOpen = true
	sh.PaletteQuery = ""
	sh.PaletteHighlight = -1
	sh.PaletteScrolled = ""
	sh.PaletteHover = ""
}

// clampReviewWidth keeps the drawer useful and leaves the conversation at
// least mainMinWide. window is the width the conversation and the drawer
// share — the caller takes the rail off first — and on a narrow window the
// drawer gives way: the conversation is the work, the drawer is a check on
// it.
func clampReviewWidth(window float32) float32 {
	width := float32(reviewWide)
	if window <= 0 {
		return width
	}
	if limit := window - mainMinWide; limit < width {
		width = limit
	}
	if width < sidebarMinWide {
		width = sidebarMinWide
	}
	return width
}

// clampSidebarWidth applies the app's pane limits on top of ui.Split, which
// only keeps each side at least ~10 DIP wide. The conversation keeps
// mainMinWide, so dragging never leaves an unusable sliver of workspace.
func clampSidebarWidth(width, window float32) float32 {
	maxWide := float32(sidebarMaxWide)
	if window > 0 {
		if limit := window - mainMinWide - 1; limit < maxWide {
			maxWide = limit
		}
	}
	if maxWide < sidebarMinWide {
		maxWide = sidebarMinWide
	}
	if width < sidebarMinWide {
		width = sidebarMinWide
	}
	if width > maxWide {
		width = maxWide
	}
	return width
}

// AskRequest is the native Quick ask form's complete fx ask surface. Empty
// values mean "let fx use its default"; prompt-permissions is deliberately
// absent because a noninteractive JSON run has no TTY to answer it.
type AskRequest struct {
	Prompt           string
	Model            string
	Effort           string
	Access           string
	Fast, Ultra      string
	System           string
	ProviderOrder    string
	Images           string
	Session          string
	AddDirs          string
	ContextLimits    string
	Strict           bool
	NoSave           bool
	NoAdditionalDirs bool
}

type Actions struct {
	ManageWorkspace func()
	OpenHistory     func()
	OpenFolder      func()
	OpenProject     func(path string)
	// OpenInFileManager shows a directory in Finder or the platform's file
	// manager. It is deliberately separate from OpenFolder (choose another
	// workspace) and OpenProject (open one): looking at the current folder
	// must not change what the window is working on.
	OpenInFileManager func(path string)
	OpenPreferences   func()
	OpenDocs          func()
	OpenConfig        func()
	RunDoctor         func()
	// SignIn opens the fx CLI that signs a provider in. ACP offers no auth
	// method, so this is the only sign-in the GUI can really perform.
	SignIn func()
	// AttachImage picks image files for the next message; PasteImage takes
	// one off the clipboard. Both are nil where no session could send one.
	AttachImage  func()
	PasteImage   func()
	CheckUpdates func()
	NewSession   func()
	Commit       func()
	RunCommand   func(id string)
	// ResumeSession opens a saved fx conversation by id.
	ResumeSession func(id string)
	// FocusSession raises the window of an already-open session.
	FocusSession func(id string)
	// ChooseConfig opens an fx config picker; an empty id opens the full overview.
	// Only a native session has one: a terminal window changes its own
	// model with fx's /model.
	ChooseConfig func(configID string)
	// Ask runs one noninteractive `fx ask` request in its own window.
	Ask func(prompt string)
	// AskAdvanced is the native Quick ask form. When it is set the form
	// uses it; Ask remains the small command-window fallback.
	AskAdvanced func(AskRequest)
	// SetAppearance persists System/Dark/Light.
	SetAppearance func(mode string)
	// StageAll stages the working tree.
	StageAll func()
	// ToggleReview opens or closes the review drawer; CloseReview only
	// closes it. Keeping both means the ✕ and ⌘R agree on one flag.
	ToggleReview func()
	CloseReview  func()
	// SelectChange opens one path's diff in the drawer.
	SelectChange func(path string)
	// RefreshChanges re-reads the working tree.
	RefreshChanges func()
	// ToggleConsole opens or closes the console drawer; SelectConsole makes
	// one run the one on screen; CloseConsoleRun ends a run and drops its
	// tab. All three are nil where the window has no console to show.
	ToggleConsole   func()
	SelectConsole   func(id uint64)
	CloseConsoleRun func(id uint64)
	// SelectInspectorTab chooses the right-hand panel's tab. The file
	// browser also needs its folders read, expanded and opened; each row
	// dispatches through the action below rather than touching the disk
	// during a frame.
	SelectInspectorTab func(tab int)
	ToggleFileDir      func(path string)
	OpenFile           func(path string)
	// OpenFileInEditor opens a file in the platform's default text editor,
	// as distinct from OpenFile's "whatever the file's type opens".
	OpenFileInEditor func(path string)
	RefreshFiles     func()
}

// WorkspaceState is what the GUI knows about the project's git. The four
// cases are exclusive, and they are not two: "not a repository" is a fact
// about the workspace, not a failure, so a project that simply has no git
// must not read as broken. Any empty or failed panel says which one it is
// rather than guessing.
type WorkspaceState int

const (
	// WorkspaceReading: the first read has not answered yet.
	WorkspaceReading WorkspaceState = iota
	// WorkspaceRepo: git answered. An empty Changes is a clean tree.
	WorkspaceRepo
	// WorkspaceNotRepo: the directory is not inside a git work tree.
	WorkspaceNotRepo
	// WorkspaceFailed: a repository was expected but git could not be read.
	WorkspaceFailed
)

type SidebarData struct {
	ProjectPath string
	FxVersion   string
	Recent      []string
	Changes     []Change
	Saved       []SavedSession

	// Workspace is the project's git as the rail's Changes line reads it.
	Workspace WorkspaceState

	Welcome     bool
	WelcomeBusy bool
	WelcomeText string
	OnInstall   func()
}

type Host struct {
	Management  ManagementHost
	History     HistoryHost
	Title       string
	Appearance  string
	Subtitle    string
	StatusLeft  []string // status bar, left group, in order
	StatusRight []string // status bar, right group
	StatusHi    string   // one highlighted segment (e.g. "● 4 modified")
	Actions     Actions
	Data        SidebarData
	Agent       Agent
	SessionBar  SessionBar
	// Auth is what fx reports about signing in. The shell shows it in the
	// start state, where a signed-out user lands before opening a folder.
	Auth AuthNotice
	// Sessions is the rail: every session the app has open, already sorted by
	// attention. Active names the one this window draws.
	Sessions []SessionSummary
	Active   string
	// Review is the working tree as the review drawer shows it.
	Review ReviewData
	// Console is the runs this window's console drawer holds (§4.7).
	Console ConsoleData
	// Files is the workspace tree as the right rail's file browser shows it:
	// the rows the user has expanded, already flattened and ordered.
	Files FileBrowserData
	// CenterOnly strips the rail and sidebar: the picker and setup
	// windows are a center page on paper, per the mock — no panels to
	// show, so nothing frames them.
	Conversation bool
	CenterOnly   bool
	Shell        *ShellState
	Main         func(c *ui.Context)
}

// Command is one entry of the command palette. Every row dispatches by ID
// through Actions.RunCommand — toggle-sidebar is the one local exception.
// Nothing here types text into a terminal: the only terminals a window holds
// run one-shot fx commands (fx doctor, fx login …), which no slash command
// belongs to (§A.6.3).
type Command struct {
	ID    string
	Label string
	Hint  string
	Icon  *ui.SVG
}

var Commands = []Command{
	{ID: "open-folder", Label: "Open Folder…", Hint: "⌘O", Icon: IconExplorer},
	{ID: "workspace-tools", Label: "Workspace tools…", Icon: IconSettings},
	{ID: "new-session", Label: "New Session", Hint: "⌘T", Icon: IconPlus},
	{ID: "continue-session", Label: "Continue last session", Hint: "-c", Icon: IconClock},
	{ID: "ask", Label: "Quick ask…", Hint: "fx ask", Icon: IconTerminal},
	{ID: "session-settings", Label: "All session settings…", Icon: IconSettings},
	{ID: "set-model", Label: "Choose model…", Hint: "--model", Icon: IconSettings},
	{ID: "attach-image", Label: "Attach image…", Icon: IconImage},
	{ID: "paste-image", Label: "Paste image", Hint: "⇧⌘V", Icon: IconImage},
	{ID: "draft-pr", Label: "fx: Draft a pull request", Icon: IconChanges},
	{ID: "draft-issue", Label: "fx: Draft an issue", Icon: IconFile},
	{ID: "run-doctor", Label: "fx: Run doctor", Icon: IconTerminal},
	{ID: "sign-in", Label: "fx: Sign in", Hint: "fx login", Icon: IconTerminal},
	{ID: "check-updates", Label: "fx: Check for Updates", Icon: IconClock},
	{ID: "show-credits", Label: "fx: Credits and balance", Icon: IconClock},
	{ID: "mcp-list", Label: "fx: List MCP servers", Icon: IconChanges},
	{ID: "open-config", Label: "fx: Open ~/.fx", Icon: IconFile},
	{ID: "refresh-status", Label: "fx: Refresh model and usage", Icon: IconClock},
	{ID: "refresh-git", Label: "Changes: Refresh", Icon: IconChanges},
	// View group. The table is sliced at projectAgentCommands, so everything
	// below here is what the palette groups as "View".
	{ID: "toggle-sidebar", Label: "Toggle Sidebar", Hint: "⌘B", Icon: IconPanel},
	{ID: "review-changes", Label: "Review Changes", Hint: "⌘R", Icon: IconChanges},
	{ID: "toggle-console", Label: "Toggle Console", Hint: "⌘J", Icon: IconTerminal},
	// Interrupt sends Ctrl-C to the running session. The mock advertises
	// it, so it has to really work.
	{ID: "session-interrupt", Label: "Session: Interrupt", Hint: "⌘.", Icon: IconAlert},
	{ID: "focus-attention", Label: "Focus next session needing you", Hint: "⌘]", Icon: IconAgent},
	{ID: "preferences", Label: "Preferences…", Hint: "⌘,", Icon: IconSettings},
	{ID: "docs", Label: "Documentation", Icon: IconFile},
}

// Frame builds the toolbar, one sidebar, terminal workspace and compact metadata.
func Frame(c *ui.Context, h Host) {
	ApplyTheme(c, h.Appearance)
	p := palette(c.Theme())
	sh := h.Shell
	if sh == nil {
		def := DefaultShell()
		sh = &def
	}
	h.Shell = sh
	ui.Column(c).Fill().Background(p.Window).Children(func() {
		titleBar(c, h, p, sh)
		if h.CenterOnly {
			ui.Column(c).Grow(1).Fill().Children(func() {
				// A start screen is exactly where a signed-out user lands,
				// so fx's own sign-in guidance belongs at the top of it —
				// above the page, not buried in a menu.
				if h.Auth.NeedsSignIn() {
					ui.Row(c).FillWidth().Shrink(0).Padding(18, 24, 0, 24).Justify(ui.Center).
						Children(func() { AuthNoticeCard(c, h.Auth, h.Actions.SignIn) })
				}
				mainPanel(c, h, p)
			})
		} else {
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				window, _ := c.Size()
				// The rail, the conversation, the drawer and the inspector
				// share one window, and each clamp keeps mainMinWide for
				// the conversation. Each therefore has to know what the
				// others take: reserving the same minimum out of the same
				// width twice is how the conversation got squeezed below
				// the minimum both of them promised it.
				drawer := float32(0)
				if sh.ReviewOpen {
					drawer = clampReviewWidth(window - float32(sidebarMinWide))
				}
				inspector := inspectorWidth(window, h, sh)
				if sh.SidebarOpen {
					sh.SidebarWidth = clampSidebarWidth(sh.SidebarWidth, window-drawer-inspector)
					sidebarSplit(c, h, p, sh).Grow(1).Fill()
				} else {
					ui.Box(c).Grow(1).Fill().Children(func() { mainPanel(c, h, p) })
				}
				// The drawer sits beside the work rather than over it: the
				// change under review stays readable while the agent keeps
				// going in the conversation next to it.
				if sh.ReviewOpen {
					rail := float32(0)
					if sh.SidebarOpen {
						rail = sh.SidebarWidth
					}
					reviewDrawer(c, h, sh, p, clampReviewWidth(window-rail))
				}
				if inspector > 0 {
					inspectorPanel(c, h, sh, p, inspector)
				}
			})
		}
		// The console sits under the work it belongs to and across the full
		// width: a run is context for the whole window, not for one pane. It
		// shows only where a window has somewhere to put runs — a command
		// window's entire surface is already a terminal.
		if sh.ConsoleOpen && h.Actions.ToggleConsole != nil {
			consoleDrawer(c, h, sh, p)
		}
		statusBar(c, h, p)
	})
	if sh.PaletteOpen {
		commandPalette(c, h, sh, p)
	}
	if sh.AskOpen && (h.Actions.AskAdvanced != nil || h.Actions.Ask != nil) {
		askDialog(c, h, sh, p)
	}
	if sh.Tools.Open && h.Actions.ManageWorkspace != nil {
		managementDialog(c, &sh.Tools, h.Management, p)
	}
	if sh.History.Open && h.Actions.OpenHistory != nil {
		historyDialog(c, &sh.History, h.History, p)
	}
}

// sidebarSplit keeps MyGo's native splitter input and accessibility. Split
// captures its theme when constructed; only its divider gets Hairline, while
// the panes and the rest of the frame keep Edge for accessible control borders.
func sidebarSplit(c *ui.Context, h Host, p Palette, sh *ShellState) ui.Element {
	base := c.Theme()
	divider := *base
	divider.Border = p.Hairline
	c.SetTheme(&divider)
	defer c.SetTheme(base)
	return ui.Split(c, &sh.SidebarWidth, func() {
		c.SetTheme(base)
		sidebarPanel(c, h, p)
	}, func() {
		c.SetTheme(base)
		mainPanel(c, h, p)
	})
}

// titleBar draws the unified 52pt app toolbar. With TitleBarHidden the window
// controls overlay the left edge, so pad by the room they take and make
// the whole strip drag the window.
func titleBar(c *ui.Context, h Host, p Palette, sh *ShellState) {
	bar := c.TitleBar()
	left := bar.Left + 14
	width, _ := c.Size()
	showChips := h.Conversation && !h.CenterOnly && width >= 980
	ui.Row(c).Shrink(0).Height(titleBarHeight).AlignItems(ui.Center).Gap(9).
		Background(p.Window).
		BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).
		Padding(0, bar.Right+12, 0, left).
		DragWindow().
		Children(func() {
			title := h.Title
			if title == "" {
				title = "fx"
			}
			if !h.CenterOnly {
				ui.Button(c, "").Label("Toggle Sidebar").Tooltip("Toggle Sidebar (⌘B)").
					Padding(7).Radius(controlRadius).BorderWidth(0).Background(p.Window).
					OnClick(func() { sh.SidebarOpen = !sh.SidebarOpen }).
					Children(func() { ui.Icon(c, IconPanel).FontSize(15).TextColor(p.InkMuted) })
				brandMark(c, p)
			}
			ui.Column(c).MinWidth(0).Gap(1).Children(func() {
				windowTitle(c, title, p.InkStrong)
				if h.Subtitle != "" && !h.CenterOnly && width >= 900 {
					mono(ui.Text(c, h.Subtitle)).FontSize(9).TextColor(p.InkMuted).SingleLine()
				}
			})
			ui.Box(c).Grow(1)
			if showChips {
				if h.Agent.Model != "" {
					ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Padding(3, 8).Radius(4).
						Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
						signalDot(c, p.Purple, 5)
						mono(ui.Text(c, shortChoice(h.Agent.Model))).FontSize(10).TextColor(p.InkMuted).SingleLine()
					})
				}
				if h.Agent.Status != "" {
					col := p.OK
					if strings.EqualFold(h.Agent.Status, "attention") {
						col = p.Warn
					} else if strings.EqualFold(h.Agent.Status, "exited") {
						col = p.InkMuted
					}
					ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(6).Padding(3, 8).Radius(4).
						Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
						signalDot(c, col, 5)
						mono(ui.Text(c, h.Agent.Status)).FontSize(10).TextColor(p.InkMuted).SingleLine()
					})
				}
			}
			if !h.CenterOnly {
				ui.Button(c, "").Label("Commands").Tooltip("Commands (⌘K)").Padding(7).BorderWidth(0).Background(p.Window).
					OnClick(func() { OpenPalette(sh) }).
					Children(func() { ui.Icon(c, IconSearch).FontSize(15).TextColor(p.InkMuted) })
			}
			if !h.CenterOnly && !sh.ReviewOpen {
				// A narrow probe, not a copy of the shell: the panel state
				// carries a live list, and copying it is not what this
				// question is about.
				probe := ShellState{InspectorOpen: true}
				if inspectorWidth(width, h, &probe) > 0 {
					active := sh.InspectorOpen
					btn := ui.Button(c, "").Label("Toggle Inspector").Tooltip("Toggle Inspector").
						Padding(7).Radius(controlRadius).BorderWidth(0).Background(p.Window).
						OnClick(func() { sh.InspectorOpen = !sh.InspectorOpen })
					if active {
						btn.Background(p.Surface)
					}
					btn.Children(func() {
						col := p.InkMuted
						if active {
							col = p.Accent
						}
						ui.Icon(c, IconGauge).FontSize(15).TextColor(col)
					})
				}
			}
		})
}

// brandMark is the small bordered fx tile at the head of the toolbar: the
// product's signature, not another menu.
func brandMark(c *ui.Context, p Palette) {
	ui.Row(c).Width(24).Height(24).Center().Radius(5).Shrink(0).
		Background(p.Surface).BorderWidth(1).BorderColor(p.Edge).Children(func() {
		ui.Text(c, "fx").Font(MonoFont).FontSize(11).FontWeight(600).TextColor(p.InkStrong)
	})
}

// inspectorWidth answers whether the telemetry panel belongs in this frame.
// It gives way to the review drawer — one side panel at a time — and to a
// window too narrow to keep a usable conversation beside it.
func inspectorWidth(window float32, h Host, sh *ShellState) float32 {
	if !h.Conversation || sh.ReviewOpen || !sh.InspectorOpen {
		return 0
	}
	if window < float32(sidebarMinWide+mainMinWide+inspectorWide+24) {
		return 0
	}
	return inspectorWide
}

func navigationButton(c *ui.Context, p Palette, icon *ui.SVG, label, hint string, active bool, action func()) {
	btn := ui.Button(c, "").Label(label).Tooltip(label).FillWidth().Height(32).Padding(0, 10).Radius(controlRadius).BorderWidth(0).OnClick(action)
	col := p.InkMuted
	if active {
		btn.Background(p.SurfaceHi).BorderWidth(0, 0, 0, 2).BorderColor(p.Accent)
		col = p.Accent
	} else {
		btn.Background(p.Surface)
	}
	btn.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(9).Children(func() {
			ui.Icon(c, icon).FontSize(15).TextColor(col)
			// Sidebar actions belong to the workbench body, not the
			// top-level chrome: keep them on the same mono face as the
			// section labels and rows around them.
			ui.Text(c, label).Font(MonoFont).FontSize(12).FontWeight(500).TextColor(p.Ink)
			ui.Box(c).Grow(1)
			if hint != "" {
				mono(ui.Text(c, hint)).FontSize(9).TextColor(p.InkMuted)
			}
		})
	})
}

// mainPanel gives the terminal the full work surface between two compact context rows.
func mainPanel(c *ui.Context, h Host, p Palette) {
	ui.Column(c).Grow(1).Fill().MinWidth(0).Background(p.Window).Children(func() {
		if h.CenterOnly || h.Conversation {
			ui.Box(c).Grow(1).Fill().MinHeight(0).Children(func() {
				if h.Main != nil {
					h.Main(c)
				}
			})
			return
		}
		sessionBar(c, h, p)
		ui.Box(c).Grow(1).FillWidth().MinHeight(0).Padding(terminalInset, 20).Background(p.TermBG).Children(func() {
			if h.Main != nil {
				h.Main(c)
			} else {
				ui.Text(c, "No session").TextColor(p.InkMuted)
			}
		})
		if h.Agent.Status != "" {
			agentBar(c, h.Agent, p)
		}
	})
}

// sessionBar is the 36px context strip. It is deliberately thin: it
// answers only "is the agent live, and on what branch" — the project name
// is already in the title bar and the new-session action already sits
// there too. When there is nothing contextual to say (no branch, nothing
// running) the strip is not drawn at all.
func sessionBar(c *ui.Context, h Host, p Palette) {
	sb := h.SessionBar
	if sb.Branch == "" && !sb.Running {
		return
	}
	ui.Row(c).Shrink(0).Height(sessionBarHeight).AlignItems(ui.Center).
		Background(p.Window).
		BorderWidth(0, 0, 0, 0).BorderColor(p.Hairline).
		Padding(0, 14).Gap(8).
		Children(func() {
			dot := p.InkFaint
			if sb.Running {
				dot = p.OK
			}
			signalDot(c, dot, 6)
			mono(ui.Text(c, liveLabel(sb))).FontSize(10).TextColor(p.InkMuted)
			if sb.Branch != "" {
				ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(5).Padding(1, 6).Radius(3).
					Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
					mono(ui.Text(c, sb.Branch)).FontSize(9).TextColor(p.InkMuted).SingleLine()
				})
			}
			if sb.Name != "" {
				mono(ui.Text(c, sb.Name)).FontSize(10).TextColor(p.InkFaint).SingleLine()
			}
			ui.Box(c).Grow(1)
			if sb.Count > 0 {
				mono(ui.Text(c, itoa(sb.Count)+" open")).FontSize(9).TextColor(p.InkMuted).SingleLine()
			}
		})
}

func liveLabel(sb SessionBar) string {
	if sb.Running {
		return "agent live"
	}
	return "idle"
}

func agentBar(c *ui.Context, a Agent, p Palette) {
	row := ui.Row(c).Shrink(0).Height(agentBarHeight).AlignItems(ui.Center).
		Background(p.Window).
		BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline)
	// room is the strip's own box from the frame before, and the strip is as
	// wide as the pane it sits in — not as wide as the window, which the rail
	// and the drawer have already taken their share of. Nothing is measured
	// on the first frame, which fitAgent answers with the width ladder.
	room := row.Bounds().W
	row.Children(func() {
		fit := fitAgent(c, a, room)
		statBlock(c, p, "", a.Status, false)
		if fit.Model != "" {
			// No Grow: the one flexible child in this row is the spacer (or
			// the meter). A content block that grows competes with it, and
			// the row then ellipsises "Model" to "Mo…" while a hole as wide
			// as the model name sits before Elapsed.
			statBlock(c, p, "Model", fit.Model, false)
		}
		if fit.Permission {
			// Quiet by default: ask/auto is the normal state and does
			// not need to be said. full-access turns fx's permission
			// checks off, which does, so it shows in the danger tone.
			statBlockValue(c, p, "Permission", a.Permission, false, p.Danger)
		}
		if fit.Tokens {
			statBlock(c, p, "Tokens", a.Tokens, false)
		}
		if fit.Spend {
			statBlock(c, p, "Spend", a.Spend, false)
		}
		if fit.Context {
			ui.Column(c).Grow(1).Justify(ui.Center).Padding(0, statPad).Gap(2).Children(func() {
				mono(ui.Text(c, contextText(a))).
					FontSize(statLabelSize).FontWeight(400).TextColor(p.InkMuted)
				segmentedMeter(c, p, a.ContextUsed, a.ContextTotal, 24)
			})
		} else {
			ui.Box(c).Grow(1)
		}
		if a.Elapsed != "" {
			statBlock(c, p, "Elapsed", a.Elapsed, false)
		}
	})
}

// agentFit is what the agent strip shows of the run: the model name to print
// (empty for none) and which optional blocks fit beside it.
type agentFit struct {
	Model                              string
	Permission, Tokens, Spend, Context bool
}

// fitAgent decides the strip from the room it has. Blocks are whole: an
// optional block is shown complete or left out, never squeezed until its
// label reads "Mo…" — the row is a readout, and a half-word in it reads as a
// bug. The model is the one block that shortens before it goes: fx names a
// model by a long path and the tail is the part that identifies it, so the
// strip prints the tail before it prints nothing.
//
// room is the strip's box from the frame before; 0 means nothing has been
// measured yet, and the width ladder that used to decide on its own answers
// for that one frame — the window is the widest the pane can be, so a block
// that used to appear at a given window width certainly had room then.
func fitAgent(c *ui.Context, a Agent, room float32) agentFit {
	fit := agentFit{
		Model:      a.Model,
		Permission: a.Permission != "" && !isDefaultPermission(a.Permission),
		Tokens:     a.Tokens != "",
		Spend:      a.Spend != "",
		Context:    a.ContextTotal > 0,
	}
	if room <= 0 {
		width, _ := c.Size()
		fit.Tokens = fit.Tokens && width >= 1000
		fit.Spend = fit.Spend && width >= 1120
		fit.Context = fit.Context && width >= 1280
		return fit
	}
	// What the strip keeps when it can keep only one thing: what the session
	// is doing, and how long it has run. Then, in the order the rule used to
	// reveal them, the blocks that fit beside those.
	used := statWidth(c, "", a.Status)
	if a.Elapsed != "" {
		used += statWidth(c, "Elapsed", a.Elapsed)
	}
	// A non-default mode is a warning, not trivia: it is reserved before the
	// model, so a long model name never costs the row its safety notice.
	if fit.Permission {
		if w := statWidth(c, "Permission", a.Permission); used+w <= room {
			used += w
		} else {
			fit.Permission = false
		}
	}
	switch model := a.Model; {
	case model == "":
		fit.Model = ""
	case used+statWidth(c, "Model", model) <= room:
		used += statWidth(c, "Model", model)
	case used+statWidth(c, "Model", shortChoice(model)) <= room:
		fit.Model = shortChoice(model)
		used += statWidth(c, "Model", fit.Model)
	default:
		fit.Model = ""
	}
	if fit.Tokens {
		if w := statWidth(c, "Tokens", a.Tokens); used+w <= room {
			used += w
		} else {
			fit.Tokens = false
		}
	}
	if fit.Spend {
		if w := statWidth(c, "Spend", a.Spend); used+w <= room {
			used += w
		} else {
			fit.Spend = false
		}
	}
	if fit.Context {
		// The meter needs room to read as a meter: a bar squeezed to a few
		// points says less than nothing, so the block waits for the width
		// that makes it legible.
		if w := contextWidth(c, a) + contextMeterMin; used+w <= room {
			used += w
		} else {
			fit.Context = false
		}
	}
	return fit
}

// contextText is the meter's caption: the one count the strip shows. It is
// written once and measured by the same function, so a caption the fit did
// not measure cannot appear.
func contextText(a Agent) string {
	return fmt.Sprintf("Context %s / %s", countText(a.ContextUsed), countText(a.ContextTotal))
}

// countText renders a token count the way fx does — 1.2k, 34k, 1.8M — so the
// context caption and the Tokens block beside it read in one unit system
// (§4.8.4 W2). The same rule the app's compactCount applies to `fx usage
// --json`; a raw integer in this strip sat next to "128.4k" and read as a
// different quantity.
func countText(n int) string {
	switch {
	case n < 0:
		return "0"
	case n < 1000:
		return itoa(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}

// contextWidth is the room the caption takes; the meter beside it is
// flexible, so only its floor is counted.
func contextWidth(c *ui.Context, a Agent) float32 {
	text, _ := c.MeasureText(0, ui.Span{Text: contextText(a), Size: statLabelSize})
	return text + 2*statPad
}

// contextMeterMin is the narrowest a context meter is still worth drawing.
const contextMeterMin = 56

// isDefaultPermission reports whether a mode is the unremarkable one.
// fx asks before acting unless told otherwise, so "ask" and "auto" are
// the silent states; anything else means checks are relaxed.
func isDefaultPermission(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "ask", "auto", "default", "":
		return true
	}
	return false
}

// statusBar is the ink strip at the bottom, and it is deliberately quiet:
// the version, then the working-tree state highlighted; the project path on
// the right. The branch lives in the session bar, the permission mode in the
// agent strip, and the font and encoding were dropped as IDE habits — all
// three used to sit here and made the strip noisy. Segments join with
// middots like the mock.
func statusBar(c *ui.Context, h Host, p Palette) {
	ui.Row(c).Shrink(0).Height(statusBarHeight).AlignItems(ui.Center).
		Background(p.Window).BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).
		Padding(0, 12).
		Gap(8).
		Children(func() {
			signalDot(c, p.OK, 5)
			sep := false
			emit := func(s string, hi bool) {
				if s == "" {
					return
				}
				if sep {
					mono(ui.Text(c, "·")).FontSize(9).TextColor(p.InkFaint)
				}
				sep = true
				col := p.InkMuted
				if hi {
					col = p.Accent
				}
				text := mono(ui.Text(c, s)).FontSize(9).TextColor(col).SingleLine()
				if hi {
					text.FontWeight(500)
				}
			}
			for _, s := range h.StatusLeft {
				emit(s, false)
			}
			emit(h.StatusHi, true)
			ui.Box(c).Grow(1)
			// The groups sit at the two ends of the bar, so the separator
			// starts over: a middot belongs between two segments, and the
			// one that opened the right group had the whole gap on its left.
			sep = false
			for _, s := range h.StatusRight {
				if s != "" && h.Data.ProjectPath != "" && s == ProjectSubtitle(h.Data.ProjectPath) && h.Actions.OpenFolder != nil {
					if sep {
						mono(ui.Text(c, "·")).FontSize(9).TextColor(p.InkFaint)
					}
					sep = true
					width, _ := c.Size()
					ui.Button(c, "").Label("Switch workspace (status bar)").Tooltip(h.Data.ProjectPath+"\nSwitch workspace (⌘O)").
						Height(statusBarHeight-2).MaxWidth(width/3).MinWidth(0).ClipX().Padding(0, 4).
						BorderWidth(0).Background(p.Window).Radius(4).OnClick(h.Actions.OpenFolder).
						Children(func() { mono(ui.Text(c, s)).FontSize(9).TextColor(p.InkMuted).SingleLine().MinWidth(0) })
				} else {
					emit(s, false)
				}
			}
			if h.Actions.ToggleReview != nil {
				ui.Button(c, "Review").Label("Review changes ⌘R").Tooltip("Review changes (⌘R)").
					Font(MonoFont).FontSize(9).BorderWidth(0).Background(p.Window).TextColor(p.Ink).
					Padding(0, 6).OnClick(h.Actions.ToggleReview)
			}
			if len(h.Console.Runs) > 0 && h.Actions.ToggleConsole != nil {
				// The console is the only place a window's runs are listed,
				// so hiding it must not lose them: the strip keeps the way
				// back, and takes the "hi" tone while a process is still
				// going, which is the one thing worth noticing from here.
				col := p.Ink
				if consoleRunning(h.Console) {
					col = p.Accent
				}
				ui.Button(c, "Console").Label("Console ⌘J").Tooltip("Show or hide the console (⌘J)").
					Font(MonoFont).FontSize(9).BorderWidth(0).Background(p.Window).TextColor(col).
					Padding(0, 6).OnClick(h.Actions.ToggleConsole)
			}
			if !sep {
				mono(ui.Text(c, "Ready")).FontSize(9).TextColor(p.InkMuted)
			}
		})
}

// sheet supplies one surface and one border for palettes and small dialogs.
// The panel is given a definite width rather than a maximum: every row in it
// fills the width it is given, so a panel sized by its content would collapse
// to the widest thing inside it — a text field's own width, on a palette —
// and leave the list to find its own. label names the dialog for assistive
// technology.
func sheet(c *ui.Context, open *bool, p Palette, label string, content func()) {
	width, height := c.Size()
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.Scrim)
		panel.Padding(0).Gap(0).Radius(sheetRadius).Background(p.Window).Label(label).
			Width(min(float32(contentMaxWidth), width-48)).MaxHeight(height-48).
			BorderWidth(1).BorderColor(p.Hairline).Clip().Shadow(0, 18, 48, 0, p.SheetShadow)
		content()
	})
}

// paletteRow is one command as the palette shows it: a stable key the list
// and the highlight can be held by, plus the command itself.
type paletteRow struct {
	Key string
	Command
}

// paletteGroup is one heading of the palette with the rows under it. The
// title travels with the rows rather than being looked up by position
// afterwards: a query that empties a group drops it, and the group after it
// must not inherit its heading.
type paletteGroup struct {
	Title string
	Rows  []paletteRow
}

// paletteGroupRows are the commands fx would let this window run, in groups,
// filtered by the query. A group with nothing left in it disappears rather
// than leaving an empty heading behind.
func paletteGroupRows(h Host, query string) []paletteGroup {
	q := strings.ToLower(strings.TrimSpace(query))
	groups := []paletteGroup{
		{Title: "Project & agent", Rows: paletteRows(h, q, "cmd:", Commands[:projectAgentCommands])},
		{Title: "View", Rows: paletteRows(h, q, "cmd:", Commands[projectAgentCommands:])},
	}
	out := make([]paletteGroup, 0, len(groups))
	for _, g := range groups {
		if len(g.Rows) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// paletteCount is how many rows all the groups hold together, which is what
// the highlight moves through and what tells an empty query's list from a
// query that matched nothing.
func paletteCount(groups []paletteGroup) int {
	n := 0
	for _, g := range groups {
		n += len(g.Rows)
	}
	return n
}

// paletteHighlighted resolves the flat row index the arrows move through back
// to the row a group holds, heading by heading.
func paletteHighlighted(groups []paletteGroup, index int) (paletteRow, bool) {
	if index < 0 {
		return paletteRow{}, false
	}
	for _, g := range groups {
		if index < len(g.Rows) {
			return g.Rows[index], true
		}
		index -= len(g.Rows)
	}
	return paletteRow{}, false
}

// paletteListMax caps the palette's list so the sheet stays a sheet: about
// 70% of the window, with the field above it and the key hints below both
// left in place while the list scrolls.
func paletteListMax(height float32) float32 {
	return min(float32(340), max(float32(120), height*0.7-90))
}

func paletteRows(h Host, query, prefix string, cmds []Command) []paletteRow {
	rows := make([]paletteRow, 0, len(cmds))
	for _, cmd := range cmds {
		if !commandAvailable(h, cmd) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(cmd.Label), query) {
			continue
		}
		rows = append(rows, paletteRow{Key: prefix + cmd.ID, Command: cmd})
	}
	return rows
}

// commandPalette is the ⌘K sheet: every command this window may run, a few
// keystrokes away. The field, the list and the key hints are one panel (§4.6)
// rather than a list floating over one: the panel is one width, the list
// scrolls inside it, and the arrows belong to the field that has the focus.
//
// The keys are the shell's rather than a widget's because the widget a list
// of choices comes from — a combobox — shows its choices in a popup of its
// own, sized to its anchor and hung over the window. A command palette is one
// surface with a list in it, so the shell does the four keys itself.
func commandPalette(c *ui.Context, h Host, sh *ShellState, p Palette) {
	_, height := c.Size()
	sheet(c, &sh.PaletteOpen, p, "Command palette", func() {
		ui.Column(c).FillWidth().Children(func() {
			// The field takes the whole width of the panel with nothing
			// beside it inside the row, so the panel and the field share one
			// left edge and the list lines up with both.
			// TextInput's form styling recolors its border on focus/hover
			// and rounds the bottom corners. This field is the sheet's
			// header: keep native editing with a flat structural hairline.
			field := ui.TextInputBase(c, &sh.PaletteQuery).Placeholder("Search commands…").
				Label("Search commands").FontSize(13).Padding(12, panelPad).
				Radius(0).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).
				Background(p.Window).FillWidth().AutoFocus()
			groups := paletteGroupRows(h, sh.PaletteQuery)
			rows := paletteCount(groups)
			// A query that leaves less than the last one must not strand the
			// highlight past the end of the list.
			if sh.PaletteHighlight >= rows {
				sh.PaletteHighlight = rows - 1
			}
			var moved bool
			if rows > 0 {
				switch {
				case field.Shortcut(0, ui.KeyDown):
					sh.PaletteHighlight, moved = min(sh.PaletteHighlight+1, rows-1), true
				case field.Shortcut(0, ui.KeyUp):
					sh.PaletteHighlight, moved = max(sh.PaletteHighlight-1, -1), true
				}
			}
			// Typing filters the list and preselects its first row, so ⌘K,
			// a few letters and Enter is the whole search.
			if field.Changed() {
				sh.PaletteHighlight, moved = 0, true
			}
			if moved {
				sh.PaletteScrolled = ""
			}
			if field.Submitted() {
				if row, ok := paletteHighlighted(groups, sh.PaletteHighlight); ok {
					runCommand(h, sh, row.Command)
				}
			}
			if rows == 0 {
				ui.Row(c).FillWidth().Justify(ui.Center).Padding(22, panelPad).Children(func() {
					ui.Text(c, "No matching command").FontSize(12).TextColor(p.InkMuted)
				})
			} else {
				ui.Scroll(c).FillWidth().MaxHeight(paletteListMax(height)).Children(func() {
					ui.Column(c).FillWidth().Padding(6).Gap(2).Children(func() {
						index, hovered := 0, ""
						for _, group := range groups {
							group := group
							ui.Row(c).FillWidth().Padding(8, panelPad, 4, panelPad).AlignItems(ui.Center).Children(func() {
								labelText(c, group.Title, p.InkMuted)
								ui.Box(c).Grow(1)
								mono(ui.Text(c, itoa(len(group.Rows)))).FontSize(9).TextColor(p.Accent)
							})
							for _, row := range group.Rows {
								row, i := row, index
								index++
								item := ui.Row(c).FillWidth().Padding(8, panelPad).Radius(4).
									AlignItems(ui.Center).Gap(10).
									OnClick(func() { runCommand(h, sh, row.Command) })
								switch {
								case i == sh.PaletteHighlight:
									item.Background(p.Sunken)
								case item.Hovered():
									// Entering a row moves the highlight into it,
									// once: resting on it must not undo what the
									// arrows just did.
									if sh.PaletteHover != row.Key {
										sh.PaletteHighlight, sh.PaletteHover = i, row.Key
									}
									item.Background(p.Sunken)
								}
								if item.Hovered() {
									hovered = row.Key
								}
								// The row the arrows moved to is brought into
								// view as they move, once, so the wheel can
								// scroll on past it.
								if i == sh.PaletteHighlight && sh.PaletteScrolled != row.Key {
									item.ScrollIntoView()
									sh.PaletteScrolled = row.Key
								}
								item.Children(func() {
									iconCol := p.InkMuted
									if i == sh.PaletteHighlight {
										iconCol = p.Accent
									}
									ui.Icon(c, row.Icon).FontSize(14).TextColor(iconCol)
									ui.Text(c, row.Label).FontSize(12).TextColor(p.Ink).SingleLine()
									ui.Box(c).Grow(1)
									if row.Hint != "" {
										mono(ui.Text(c, row.Hint)).FontSize(9).TextColor(p.InkFaint)
									}
								})
							}
						}
						sh.PaletteHover = hovered
					})
				})
			}
			// The hints sit under the list on the panel's own surface: the
			// list scrolls between them rather than over them.
			ui.Row(c).FillWidth().Padding(8, panelPad).Gap(14).AlignItems(ui.Center).
				Background(p.Surface).
				BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).
				Children(func() {
					mono(ui.Text(c, "↑↓ move · enter run · esc dismiss")).FontSize(9).TextColor(p.InkMuted)
					ui.Box(c).Grow(1)
					mono(ui.Text(c, itoa(availableCommands(h))+" commands")).FontSize(9).TextColor(p.InkFaint)
				})
		})
	})
}

// commandAvailable reports whether this window can actually run a palette
// entry. A command window has no workspace, so it has no model to choose
// and no repository to ask about — and a row that opens nothing is a lie.
func commandAvailable(h Host, cmd Command) bool {
	switch cmd.ID {
	case "workspace-tools":
		return h.Actions.ManageWorkspace != nil && h.Data.ProjectPath != ""
	case "ask":
		return h.Data.ProjectPath != "" && (h.Actions.AskAdvanced != nil || h.Actions.Ask != nil)
	case "set-model", "session-settings":
		// Only a native session has a config picker to open; a terminal
		// window changes its own model with fx's /model.
		return h.Actions.ChooseConfig != nil
	case "sign-in":
		// fx login needs no workspace, so this stays available in every
		// window kind: a signed-out user must be able to reach it before
		// they have opened a folder.
		return h.Actions.SignIn != nil
	case "attach-image":
		return h.Actions.AttachImage != nil
	case "paste-image":
		return h.Actions.PasteImage != nil
	case "review-changes":
		// The drawer reads one workspace's tree; without an action behind
		// it there is nothing to open.
		return h.Actions.ToggleReview != nil
	case "toggle-console":
		// A window with no console to show — a command window, whose whole
		// surface is already a terminal — does not offer the drawer.
		return h.Actions.ToggleConsole != nil
	// These spawn fx against a repository: without a workspace they would
	// run in the app's own directory, which is never what the user meant.
	case "continue-session", "draft-pr", "draft-issue":
		return h.Data.ProjectPath != ""
	}
	return true
}

// availableCommands counts the rows this window can run, which is what the
// palette footer advertises.
func availableCommands(h Host) int {
	n := 0
	for _, cmd := range Commands {
		if commandAvailable(h, cmd) {
			n++
		}
	}
	return n
}

// askDialog is the native front end for fx ask. It keeps the one-shot
// request out of a terminal: the result is parsed from --json and shown here,
// while the prompt and the console stay available for the main conversation.
func askDialog(c *ui.Context, h Host, sh *ShellState, p Palette) {
	_, height := c.Size()
	if sh.AskAccess == "" {
		sh.AskAccess = "auto"
	}
	if sh.AskFast == "" {
		sh.AskFast = "default"
	}
	if sh.AskUltra == "" {
		sh.AskUltra = "default"
	}
	sheet(c, &sh.AskOpen, p, "Quick ask", func() {
		ui.Column(c).FillWidth().MaxWidth(contentMaxWidth).Background(p.Window).
			Radius(sheetRadius).
			Children(func() {
				ui.Column(c).FillWidth().Padding(14, panelPad, 12, panelPad).Gap(4).
					BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).
					Children(func() {
						sheetTitle(c, "Quick ask", p.InkStrong)
						ui.Text(c, "Runs fx ask with the options below and shows the answer here.").
							FontSize(11).TextColor(p.InkMuted)
					})
				ui.Scroll(c).FillWidth().MaxHeight(min(float32(430), max(float32(120), height-230))).Children(func() {
					ui.Column(c).FillWidth().Padding(12, panelPad, 8, panelPad).Gap(9).Children(func() {
						field := func(label string, build func()) {
							ui.Column(c).FillWidth().Gap(3).Children(func() {
								mono(ui.Text(c, label)).FontSize(9).TextColor(p.InkMuted)
								build()
							})
						}
						field("Prompt", func() {
							ui.TextArea(c, &sh.AskPrompt).Label("Prompt").Lines(2, 5).FillWidth().Disabled(sh.AskBusy)
						})
						field("Model override (optional)", func() {
							ui.TextInput(c, &sh.AskModel).Placeholder("provider/model").FillWidth().Disabled(sh.AskBusy)
						})
						field("Reasoning effort (optional)", func() {
							ui.TextInput(c, &sh.AskEffort).Placeholder("auto, minimal, low, medium, high, xhigh").FillWidth().Disabled(sh.AskBusy)
						})
						field("Additional directories (one absolute path per line)", func() {
							ui.TextArea(c, &sh.AskAddDirs).Label("Additional directories").Lines(1, 3).FillWidth().Disabled(sh.AskBusy)
						})
						field("Context limit overrides (NAME=BYTES|off, one per line)", func() {
							ui.TextArea(c, &sh.AskContextLimits).Label("Context limit overrides").Lines(1, 3).FillWidth().Disabled(sh.AskBusy)
						})
						ui.Column(c).FillWidth().Gap(2).Children(func() {
							mono(ui.Text(c, "Access")).FontSize(9).TextColor(p.InkMuted)
							ui.RadioGroup(c, func() {
								ui.Radio(c, &sh.AskAccess, "auto", "Auto safety review").FontSize(11)
								ui.Radio(c, &sh.AskAccess, "full-access", "Full access").FontSize(11)
							})
							ui.Text(c, "Interactive permission prompts need a TTY, so a one-shot native run uses fx's automatic review or full access.").
								FontSize(10).TextColor(p.InkMuted).Wrap()
						})
						ui.Row(c).FillWidth().Gap(18).Children(func() {
							ui.Column(c).Grow(1).Gap(2).Children(func() {
								mono(ui.Text(c, "Fast mode")).FontSize(9).TextColor(p.InkMuted)
								ui.RadioGroup(c, func() {
									ui.Radio(c, &sh.AskFast, "default", "Default").FontSize(11)
									ui.Radio(c, &sh.AskFast, "on", "On").FontSize(11)
									ui.Radio(c, &sh.AskFast, "off", "Off").FontSize(11)
								})
							})
							ui.Column(c).Grow(1).Gap(2).Children(func() {
								mono(ui.Text(c, "Ultra mode")).FontSize(9).TextColor(p.InkMuted)
								ui.RadioGroup(c, func() {
									ui.Radio(c, &sh.AskUltra, "default", "Default").FontSize(11)
									ui.Radio(c, &sh.AskUltra, "on", "On").FontSize(11)
									ui.Radio(c, &sh.AskUltra, "off", "Off").FontSize(11)
								})
							})
						})
						ui.Column(c).FillWidth().Gap(2).Children(func() {
							ui.Checkbox(c, &sh.AskNoSave, "Do not save this request as a session").FontSize(11).Disabled(sh.AskBusy)
							ui.Checkbox(c, &sh.AskStrict, "Restrict gateway providers to the order below").FontSize(11).Disabled(sh.AskBusy)
							ui.Checkbox(c, &sh.AskNoAdditionalDirs, "Ignore saved additional directories").FontSize(11).Disabled(sh.AskBusy)
						})
						field("Provider order (optional, comma-separated)", func() {
							ui.TextInput(c, &sh.AskProviderOrder).Placeholder("gateway-a,gateway-b").FillWidth().Disabled(sh.AskBusy)
						})
						field("Resume session (last or id, optional)", func() {
							ui.TextInput(c, &sh.AskSession).Placeholder("last").FillWidth().Disabled(sh.AskBusy)
						})
						field("System prompt override (optional)", func() {
							ui.TextArea(c, &sh.AskSystem).Label("System prompt").Lines(1, 3).FillWidth().Disabled(sh.AskBusy)
						})
						field("Image paths (one per line, optional)", func() {
							ui.TextArea(c, &sh.AskImages).Label("Image paths").Lines(1, 3).FillWidth().Disabled(sh.AskBusy)
						})
						if sh.AskBusy {
							ui.Text(c, "Running fx ask…").FontSize(11).TextColor(p.InkMuted)
						}
						if sh.AskProblem != "" {
							ui.Text(c, sh.AskProblem).FontSize(11).TextColor(p.Danger).Wrap().Selectable()
						}
						if sh.AskOutput != "" {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								mono(ui.Text(c, "Result")).FontSize(9).TextColor(p.InkMuted)
								if sh.AskMeta != "" {
									ui.Text(c, sh.AskMeta).Font(MonoFont).FontSize(9).TextColor(p.InkMuted).Wrap()
								}
								ui.Scroll(c).FillWidth().MaxHeight(180).Children(func() {
									ui.Text(c, sh.AskOutput).FontSize(12).TextColor(p.Ink).Wrap().Selectable().Padding(8, 10).Background(p.Sunken)
								})
							})
						}
					})
				})
				ui.Row(c).FillWidth().Padding(10, panelPad, 12, panelPad).Gap(8).Justify(ui.End).
					BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
					ui.Button(c, "Cancel").Padding(7, 14).Radius(controlRadius).TextColor(p.Ink).Disabled(sh.AskBusy).
						OnClick(func() { sh.AskOpen = false })
					ui.PrimaryButton(c, "Run").Background(p.Ink).Padding(7, 14).Disabled(sh.AskBusy).
						OnClick(func() {
							prompt := strings.TrimSpace(sh.AskPrompt)
							if prompt == "" {
								return
							}
							if h.Actions.AskAdvanced != nil {
								sh.AskBusy = true
								sh.AskProblem = ""
								sh.AskOutput = ""
								sh.AskMeta = ""
								h.Actions.AskAdvanced(AskRequest{
									Prompt: prompt, Model: sh.AskModel, Effort: sh.AskEffort,
									Access: sh.AskAccess, Fast: sh.AskFast, Ultra: sh.AskUltra,
									System: sh.AskSystem, ProviderOrder: sh.AskProviderOrder, Images: sh.AskImages,
									Session: sh.AskSession, AddDirs: sh.AskAddDirs, ContextLimits: sh.AskContextLimits,
									Strict: sh.AskStrict, NoSave: sh.AskNoSave,
									NoAdditionalDirs: sh.AskNoAdditionalDirs,
								})
								return
							}
							sh.AskPrompt = ""
							sh.AskOpen = false
							if h.Actions.Ask != nil {
								h.Actions.Ask(prompt)
							}
						})
				})
			})
	})
}

func runCommand(h Host, sh *ShellState, cmd Command) {
	sh.PaletteOpen = false
	switch {
	case cmd.ID == "toggle-sidebar":
		sh.SidebarOpen = !sh.SidebarOpen
	default:
		if h.Actions.RunCommand != nil {
			h.Actions.RunCommand(cmd.ID)
		} else if (cmd.ID == "session-settings" || cmd.ID == "set-model") && h.Actions.ChooseConfig != nil {
			id := ""
			if cmd.ID == "set-model" {
				id = "model"
			}
			h.Actions.ChooseConfig(id)
		} else {
			// Picker/setup hosts expose shared actions without a session dispatcher.
			actions := map[string]func(){"open-folder": h.Actions.OpenFolder, "new-session": h.Actions.NewSession,
				"workspace-tools": h.Actions.ManageWorkspace,
				"preferences":     h.Actions.OpenPreferences, "docs": h.Actions.OpenDocs, "open-config": h.Actions.OpenConfig,
				"run-doctor": h.Actions.RunDoctor, "check-updates": h.Actions.CheckUpdates,
				"sign-in": h.Actions.SignIn, "review-changes": h.Actions.ToggleReview,
				"toggle-console": h.Actions.ToggleConsole}
			if action := actions[cmd.ID]; action != nil {
				action()
			}
		}
	}
}

// PrimaryButton is the neutral primary action; always use it instead of
// ui.PrimaryButton, whose fill follows the system accent.
func PrimaryButton(c *ui.Context, label string, onClick func()) ui.Element {
	return ui.PrimaryButton(c, label).Background(palette(c.Theme()).Ink).TextColor(palette(c.Theme()).OnInk).OnClick(onClick)
}

func CenterPage(c *ui.Context, index, title, subtitle string, primaryLabel string, onPrimary func()) {
	CenterSteps(c, index, title, subtitle, nil, primaryLabel, onPrimary, nil)
}

// CenterSteps renders a center page with an optional setup
// checklist (the setup screen) and optional secondary rows.
func CenterSteps(c *ui.Context, index, title, subtitle string, steps []Step, primaryLabel string, onPrimary func(), secondary []struct {
	Label   string
	OnClick func()
}) {
	p := palette(c.Theme())
	// FillWidth capped at contentMaxWidth: a fixed 560 column overflows
	// its parent in a narrow window, as the first real run showed.
	ui.Column(c).Fill().Center().Padding(40, 24).Children(func() {
		ui.Column(c).FillWidth().MaxWidth(contentMaxWidth).Gap(13).AlignItems(ui.Start).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(9).Children(func() {
				ui.Row(c).Width(30).Height(30).Center().Radius(5).Background(p.Surface).
					BorderWidth(1).BorderColor(p.Edge).Children(func() {
					ui.Text(c, "fx").Font(MonoFont).FontSize(12).FontWeight(600).TextColor(p.InkStrong)
				})
				ui.Text(c, "fx").Font(MonoFont).FontSize(26).FontWeight(600).LetterSpacing(-0.6).TextColor(p.InkStrong)
				ui.Box(c).Grow(1)
				if index != "" {
					badge(c, p, index, p.Accent)
				}
			})
			ui.Text(c, title).Font(SansFont).FontSize(26).FontWeight(500).LetterSpacing(-0.6).TextColor(p.InkStrong)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(13).TextColor(p.InkMuted)
			}
			if len(steps) > 0 {
				ui.Column(c).FillWidth().Padding(16, 0, 0, 0).Children(func() {
					hairlineH(c, p)
					for i, st := range steps {
						st := st
						numCol := p.InkMuted
						switch st.State {
						case "done":
							numCol = p.OK
						case "active":
							numCol = p.Accent
						case "failed":
							numCol = p.Danger
						}
						ui.Row(c).FillWidth().Padding(11, 0).AlignItems(ui.Center).Gap(14).
							BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).
							Children(func() {
								mono(ui.Text(c, fmt.Sprintf("%02d", i+1))).FontSize(10).TextColor(numCol)
								ui.Text(c, st.Title).FontSize(12).FontWeight(500).TextColor(p.InkStrong)
								ui.Box(c).Grow(1)
								if st.Note != "" {
									ui.Text(c, st.Note).FontSize(11).TextColor(p.InkMuted).SingleLine()
								}
							})
					}
				})
			}
			if primaryLabel != "" && onPrimary != nil {
				ui.PrimaryButton(c, primaryLabel).Background(p.Ink).TextColor(p.OnInk).OnClick(onPrimary)
			}
			for _, s := range secondary {
				s := s
				if s.OnClick == nil {
					continue
				}
				ui.Button(c, s.Label).Padding(7, 14).Radius(controlRadius).TextColor(p.Ink).OnClick(s.OnClick)
			}
		})
	})
}

// RecentGrid is the picker's list of recent project folders.
func RecentGrid(c *ui.Context, recents []string, onOpen func(path string)) {
	p := palette(c.Theme())
	ui.Column(c).FillWidth().MaxWidth(contentMaxWidth).Gap(4).Children(func() {
		for i, path := range recents {
			if i >= 6 {
				break
			}
			path := path
			c.Key(path)
			row := ui.ButtonBase(c).FillWidth().Padding(10, 12).Gap(11).Radius(4).Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline).Tooltip(ProjectSubtitle(path)).OnClick(func() {
				if onOpen != nil {
					onOpen(path)
				}
			})
			if row.Hovered() {
				row.Background(p.SurfaceHi)
			}
			row.Children(func() {
				ui.Icon(c, IconExplorer).FontSize(17).TextColor(p.InkMuted)
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, filepath.Base(path)).FontSize(12).FontWeight(500).TextColor(p.InkStrong).SingleLine()
					mono(ui.Text(c, ProjectSubtitle(path))).FontSize(9).TextColor(p.InkMuted).SingleLine()
				})
				ui.Icon(c, IconChevron).FontSize(12).TextColor(p.InkMuted)
			})
		}
	})
}

func ProjectSubtitle(path string) string {
	if path == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err == nil && home != "" && strings.HasPrefix(path, home) {
		rel, err := filepath.Rel(home, path)
		if err == nil && rel != "" && !strings.HasPrefix(rel, "..") {
			return "~/" + rel
		}
	}
	return path
}
