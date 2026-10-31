package chrome

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

// sidebarPanel is the session rail: every session the app has open, sorted by
// attention, with the workspace's changes beneath them. It replaces the old
// five-panel navigation — for an agent client the sessions *are* the spine.
func sidebarPanel(c *ui.Context, h Host, p Palette) {
	ui.Row(c).Fill().Background(p.Window).Children(func() {
		iconRail(c, h, p)
		ui.Column(c).Grow(1).MinWidth(0).Fill().Background(p.Surface).Children(func() {
			railHeader(c, h, p)
			ui.Scroll(c).Grow(1).FillWidth().Padding(0, 8).Children(func() {
				railList(c, h, p)
				if h.Data.ProjectPath != "" {
					changesEntry(c, h, p)
				}
				savedSection(c, h, p)
			})
		})
	})
}

// iconRail is the narrow navigation spine at the window's left edge. Every
// button maps to an action the window already has; a button whose action is
// nil is not drawn. The active destination is marked with the signal tone, a
// 2px edge and a lifted surface — the one chromatic mark in the rail.
func iconRail(c *ui.Context, h Host, p Palette) {
	// Fill the row under the title bar rather than asking for the whole
	// window height: the latter overflows by the title and status bars,
	// and flex shrink then collapses the first and last buttons.
	ui.Column(c).Width(iconRailWide).FillHeight().Shrink(0).
		Background(p.Window).BorderWidth(0, 1, 0, 0).BorderColor(p.Hairline).
		Padding(8, 0).Gap(4).Children(func() {
		if h.Actions.NewSession != nil {
			railButton(c, p, IconPlus, "New Session", "New Session (⌘T)", false, h.Actions.NewSession)
		}
		if h.Actions.ToggleReview != nil {
			active := h.Shell != nil && h.Shell.ReviewOpen
			railButton(c, p, IconChanges, "Changes", "Changes (⌘R)", active, h.Actions.ToggleReview)
		}
		if h.Actions.OpenHistory != nil {
			active := h.Shell != nil && h.Shell.History.Open
			railButton(c, p, IconClock, "History", "History", active, h.Actions.OpenHistory)
		}
		if h.Actions.ManageWorkspace != nil {
			active := h.Shell != nil && h.Shell.Tools.Open
			railButton(c, p, IconTerminal, "Workspace tools", "Workspace tools", active, h.Actions.ManageWorkspace)
		}
		if h.Actions.ToggleConsole != nil && len(h.Console.Runs) > 0 {
			active := h.Shell != nil && h.Shell.ConsoleOpen
			railButton(c, p, IconChevron, "Console", "Console", active, h.Actions.ToggleConsole)
		}
		ui.Box(c).Grow(1)
		if h.Actions.OpenPreferences != nil {
			railButton(c, p, IconSettings, "Settings", "Settings (⌘,)", false, h.Actions.OpenPreferences)
		}
	})
}

// railButton is one destination in the rail. The tip carries the shortcut the
// destination has, so the rail keeps that affordance now that the sidebar's
// own Settings row is gone.
func railButton(c *ui.Context, p Palette, icon *ui.SVG, label, tip string, active bool, action func()) {
	// The button is four DIPs narrower than the rail. A full-width surface
	// painted over the rail's own separator reads as a block that has spilled
	// into the sidebar beside it — the selected item has to stay inside the
	// rail, with the hairline still visible next to it.
	btn := ui.Button(c, "").Label(label).Tooltip(tip).Width(iconRailWide-4).Height(34).
		Padding(0, 0, 0, 2).Radius(0).BorderWidth(0).Background(p.Window).OnClick(action)
	col := p.InkMuted
	if active {
		// The mock marks the active destination with a left edge; the top
		// edge read as a stray rule between rail buttons. The edge sits on
		// the window's own border, so the signal stays flush with the rail.
		btn.Background(p.SurfaceHi).BorderWidth(0, 0, 0, 2).BorderColor(p.Accent).Padding(0)
		col = p.Accent
	}
	btn.Children(func() {
		ui.Row(c).FillWidth().Height(32).Center().Children(func() {
			ui.Icon(c, icon).FontSize(16).TextColor(col)
		})
	})
}

// railHeader is the New Session action, or the install block on a machine
// that has no fx yet (the welcome window makes the same offer).
func railHeader(c *ui.Context, h Host, p Palette) {
	d := h.Data
	ui.Column(c).FillWidth().Padding(10, 10, 8, 10).Gap(8).Children(func() {
		switch {
		case d.Welcome:
			ui.Text(c, "Install fx").FontSize(13).FontWeight(600).TextColor(p.InkStrong)
			ui.Text(c, "Sessions start once the CLI is installed.").FontSize(11).TextColor(p.InkMuted)
			if d.WelcomeBusy {
				ui.Text(c, d.WelcomeText).FontSize(11).TextColor(p.InkMuted)
			} else if d.OnInstall != nil {
				ui.PrimaryButton(c, "Download fx").Background(p.Ink).TextColor(p.OnInk).FillWidth().OnClick(d.OnInstall)
			}
		case h.Actions.NewSession != nil:
			navigationButton(c, p, IconPlus, "New Session", "⌘T", false, h.Actions.NewSession)
		}
	})
}

// railList draws one row per session. SortSessionSummaries already ordered
// them, so the top row is always the one that most wants the user.
func railList(c *ui.Context, h Host, p Palette) {
	if len(h.Sessions) == 0 {
		sidebarEmptyState(c, p, IconClock, "No sessions yet")
		return
	}
	sectionLabel(c, p, "Sessions", itoa(len(h.Sessions)))
	for _, sum := range h.Sessions {
		sum := sum
		railRow(c, p, sum, sum.ID == h.Active, func() {
			if sum.ID != h.Active && h.Actions.FocusSession != nil {
				h.Actions.FocusSession(sum.ID)
			}
		})
	}
}

// railRow is one session: attention dot, title, context, right-hand meta.
func railRow(c *ui.Context, p Palette, sum SessionSummary, active bool, onClick func()) {
	row := ui.Row(c).FillWidth().Padding(7, panelPad).Radius(controlRadius).AlignItems(ui.Center).Gap(8)
	if onClick != nil {
		row.OnClick(onClick)
	}
	if active || row.Hovered() {
		row.Background(p.SurfaceHi)
	}
	row.Children(func() {
		ui.Box(c).Width(7).Height(7).Radius(4).Background(attentionColor(p, sum.Status)).Shrink(0)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, sum.Title).FontSize(12).TextColor(p.Ink).SingleLine()
			if sub := railSubtitle(sum); sub != "" {
				ui.Text(c, sub).FontSize(10).TextColor(p.InkMuted).SingleLine()
			}
		})
		if meta := railMeta(sum); meta != "" {
			ui.Text(c, meta).FontSize(10).TextColor(p.InkMuted).SingleLine().Shrink(0)
		}
		if sum.Unread {
			ui.Box(c).Width(6).Height(6).Radius(3).Background(p.Accent).Shrink(0)
		}
	})
}

// attentionColor maps a rail state to the palette. Each state has a tone of
// its own: an approval is not a failure and not ordinary emphasis, so it
// cannot borrow Danger or Accent without saying the wrong thing (§4.8).
func attentionColor(p Palette, a Attention) ui.Color {
	switch a {
	case AttentionNeedsApproval:
		return p.Warn
	case AttentionErrored:
		return p.Danger
	case AttentionDoneUnreviewed:
		return p.OK
	case AttentionWorking:
		return p.InkMuted
	default:
		return p.InkFaint
	}
}

// attentionLabel is the short word a row shows when it wants the user.
func attentionLabel(a Attention) string {
	switch a {
	case AttentionNeedsApproval:
		return "approval"
	case AttentionErrored:
		return "error"
	case AttentionWorking:
		return "working"
	case AttentionDoneUnreviewed:
		return "review"
	case AttentionExited:
		return "exited"
	}
	return ""
}

func railSubtitle(sum SessionSummary) string {
	if sum.Branch != "" {
		return sum.Branch
	}
	base := filepath.Base(sum.Workspace)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

func railMeta(sum SessionSummary) string {
	label := attentionLabel(sum.Status)
	if sum.Changes > 0 {
		if label != "" {
			return label + " · " + itoa(sum.Changes)
		}
		return itoa(sum.Changes) + " changed"
	}
	return label
}

// changesEntry is the rail's pointer to the review drawer: the count of what
// changed, and a way in. The file list and the working-tree actions moved to
// the drawer (§4.3) — a change is something you read, not something you
// scroll past in a sidebar.
func changesEntry(c *ui.Context, h Host, p Palette) {
	if len(h.Data.Changes) == 0 {
		hairlineH(c, p)
		sectionLabel(c, p, "Changes", "")
		switch h.Data.Workspace {
		case WorkspaceFailed:
			sidebarEmptyState(c, p, IconAlert, "Could not read the workspace")
		case WorkspaceNotRepo:
			// A fact about the workspace, said plainly: there is no git
			// here, so there is no change list to read and the review
			// drawer is not offered.
			sidebarEmptyState(c, p, IconFile, "Not a git repository")
		case WorkspaceRepo:
			sidebarEmptyState(c, p, IconCheck, "Working tree clean")
		default:
			sidebarEmptyState(c, p, IconFile, "Reading workspace…")
		}
		return
	}
	hairlineH(c, p)
	sectionLabel(c, p, "Changes", "")
	row := ui.ButtonBase(c).Label("Review "+changesDetail(h)).FillWidth().
		Padding(7, panelPad).Radius(controlRadius).AlignItems(ui.Center).Gap(8)
	if h.Actions.ToggleReview != nil {
		row.OnClick(h.Actions.ToggleReview)
	}
	if row.Hovered() {
		row.Background(p.SurfaceHi)
	}
	row.Children(func() {
		ui.Icon(c, IconChanges).FontSize(13).TextColor(p.InkMuted)
		ui.Text(c, changesDetail(h)).FontSize(12).TextColor(p.Ink).SingleLine().MinWidth(0)
		ui.Box(c).Grow(1)
		ui.Text(c, "⌘R").FontSize(10).TextColor(p.InkMuted).Shrink(0)
	})
}

func changesDetail(h Host) string {
	n := len(h.Data.Changes)
	if n == 0 {
		return ""
	}
	a, d := 0, 0
	for _, ch := range h.Data.Changes {
		a += ch.Adds
		d += ch.Dels
	}
	out := itoa(n) + " changed"
	if a > 0 || d > 0 {
		out += " · +" + itoa(a) + " −" + itoa(d)
	}
	return out
}

// savedSection lists fx's saved conversations for the workspace; picking one
// resumes it (session/load), the contract the old Sessions panel had.
func savedSection(c *ui.Context, h Host, p Palette) {
	if len(h.Data.Saved) == 0 && h.Actions.OpenHistory == nil {
		return
	}
	hairlineH(c, p)
	collapsed := h.Shell != nil && h.Shell.HistoryCollapsed
	ui.ButtonBase(c).Label("Toggle history").FillWidth().Padding(12, panelPad, 7, panelPad).Gap(6).AlignItems(ui.Center).OnClick(func() {
		if h.Shell != nil {
			h.Shell.HistoryCollapsed = !h.Shell.HistoryCollapsed
		}
	}).Children(func() {
		mark := "▾"
		if collapsed {
			mark = "▸"
		}
		ui.Text(c, mark).FontSize(10).TextColor(p.InkMuted)
		ui.Text(c, "History").FontSize(11).FontWeight(500).TextColor(p.InkMuted)
		ui.Text(c, itoa(min(5, len(h.Data.Saved)))+" recent").FontSize(10).TextColor(p.InkMuted)
	})
	if !collapsed {
		for _, sv := range h.Data.Saved[:min(5, len(h.Data.Saved))] {
			sv := sv
			title := sv.Title
			if title == "" {
				title = sv.ID
			}
			meta := ""
			if sv.Turns > 0 {
				meta = itoa(sv.Turns) + " turns"
			}
			sidebarRow(c, p, IconClock, title, meta, sv.Active, func() {
				if h.Actions.ResumeSession != nil {
					h.Actions.ResumeSession(sv.ID)
				}
			})
			if sv.When != "" {
				ui.Text(c, sv.When).FontSize(10).TextColor(p.InkMuted).SingleLine().Padding(0, 0, 4, panelPad+21)
			}
		}
	}
	if h.Actions.OpenHistory != nil {
		ui.Button(c, "View all…").Label("View all session history").FontSize(11).BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).Padding(6, panelPad).OnClick(h.Actions.OpenHistory)
	}
}

// railFoot is gone: the rail's own Settings button is the one entry for
// preferences, and a second copy at the foot of the sidebar sent the user
// to the same window twice.

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// sidebarRow is one clickable line of a secondary list: icon, label, meta.
func sidebarRow(c *ui.Context, p Palette, icon *ui.SVG, label, meta string, active bool, onClick func()) {
	fg := p.Ink
	row := ui.Row(c).FillWidth().Padding(8, panelPad).Radius(controlRadius).AlignItems(ui.Center).Gap(8)
	if onClick != nil {
		row.OnClick(onClick)
	}
	if active || row.Hovered() {
		row.Background(p.SurfaceHi)
	}

	row.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(13).TextColor(p.InkMuted)
		}
		ui.Text(c, label).FontSize(12).TextColor(fg).SingleLine().MinWidth(0)
		ui.Box(c).Grow(1)
		if meta != "" {
			// Keep the right-hand value on one line: when a long title
			// squeezed it, "12 turns" wrapped into two rows and made the
			// list rhythm uneven.
			ui.Text(c, meta).FontSize(10).TextColor(p.InkMuted).SingleLine().Shrink(0)
		}
	})
}

// markText colours a git status mark: modified in accent, added in the ok
// tone, deleted in danger.
func markText(c *ui.Context, p Palette, mark string) {
	if mark == "" {
		return
	}
	col := p.Accent
	switch mark {
	case "A", "R":
		col = p.OK
	case "D":
		col = p.Danger
	}
	ui.Text(c, mark).FontSize(10).FontWeight(600).TextColor(col)
}

// sidebarEmptyState is the rail's compact version: the icon and the note
// have to share a narrow column, so the gutter is tighter and the icon can
// never push a short phrase such as "Not a git repository" onto two lines.
func sidebarEmptyState(c *ui.Context, p Palette, icon *ui.SVG, text string) {
	ui.Row(c).FillWidth().Padding(13, 12).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Icon(c, icon).FontSize(13).TextColor(p.InkMuted).Shrink(0)
		ui.Text(c, text).FontSize(12).TextColor(p.InkMuted).Grow(1).MinWidth(0)
	})
}

// emptyState answers every wide list that has nothing to show.
func emptyState(c *ui.Context, p Palette, icon *ui.SVG, text string) {
	ui.Row(c).FillWidth().Padding(16, panelPad).AlignItems(ui.Center).Gap(8).Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(p.InkMuted)
		ui.Text(c, text).FontSize(12).TextColor(p.InkMuted)
	})
}
