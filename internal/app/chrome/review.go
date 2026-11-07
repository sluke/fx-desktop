package chrome

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// ReviewData is the working tree as the review drawer shows it: the one
// surface where "what did the agent change" becomes a decision (§4.3).
type ReviewData struct {
	Open    bool
	Branch  string
	Changes []Change
	// Workspace is the tree read as the drawer reads it: reading, a
	// repository, not a git repository at all, or unreadable. An empty list
	// means four different things depending on which one it is.
	Workspace WorkspaceState
	// Selected is the path whose diff is open, Diff is that file's change as
	// classified lines, and Omitted counts what the read cap left behind.
	Selected string
	Diff     []DiffLine
	Omitted  int
	// Busy says a diff read is in flight, and Note explains an empty diff
	// that is not busy: a binary file, or a path git did not answer for.
	Busy bool
	Note string
}

// reviewWide is the drawer's width. It is wide enough for a monospace diff
// without stealing the conversation, and the caller clamps it on small
// windows.
const reviewWide = 420

// reviewListHeight caps the file list so the diff keeps the rest of the
// panel: the list is an index, the diff is the content.
const reviewListHeight = 168

// reviewDrawer is the review surface. It replaced a section of the rail and a
// pair of buttons in its footer: a list of file names you cannot read the
// change in is not a review (§4.3, step 5).
func reviewDrawer(c *ui.Context, h Host, sh *ShellState, p Palette, width float32) {
	// FillHeight, not Fill: Fill sets the width to 100% and would swallow
	// the conversation beside the drawer.
	ui.Column(c).Shrink(0).Width(width).FillHeight().Background(p.Surface).
		BorderWidth(0, 1, 0, 0).BorderColor(p.Hairline).
		Children(func() {
			reviewHeader(c, h, p)
			hairlineH(c, p)
			reviewFiles(c, h, sh, p)
			hairlineH(c, p)
			ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() { reviewDiff(c, h, p) })
			hairlineH(c, p)
			reviewActions(c, h, sh, p)
		})
}

// reviewHeader is what the drawer is about: how much changed, in which
// branch, and the two ways out (a fresh read, or closing the panel).
func reviewHeader(c *ui.Context, h Host, p Palette) {
	d := h.Review
	ui.Column(c).FillWidth().Shrink(0).Padding(Space5, panelPad, Space4, panelPad).Gap(Space1).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(Space4).Children(func() {
			ui.Icon(c, IconChanges).FontSize(IconMedium).TextColor(p.InkMuted)
			labelText(c, "Review", p.InkStrong).FontWeight(600)
			ui.Box(c).Grow(1)
			if h.Actions.RefreshChanges != nil {
				ui.Button(c, "Refresh").FontSize(TypeLabel).BorderWidth(0).Background(p.Surface).
					TextColor(p.InkMuted).Padding(Space1, Space3).OnClick(h.Actions.RefreshChanges)
			}
			if h.Actions.CloseReview != nil {
				ui.Button(c, "✕").FontSize(TypeControl).BorderWidth(0).Background(p.Surface).
					TextColor(p.InkMuted).Padding(Space1, Space3).OnClick(h.Actions.CloseReview)
			}
		})
		mono(ui.Text(c, reviewSummary(d))).FontSize(TypeBadge).TextColor(p.InkMuted).SingleLine()
	})
}

// reviewSummary is the header line: what changed, how much, and where. A
// clean tree says so rather than showing an empty tally.
func reviewSummary(d ReviewData) string {
	if len(d.Changes) == 0 {
		switch d.Workspace {
		case WorkspaceFailed:
			return "Workspace unavailable"
		case WorkspaceNotRepo:
			return "Not a git repository"
		case WorkspaceRepo:
			if d.Branch != "" {
				return "Clean · " + d.Branch
			}
			return "Working tree clean"
		default:
			return "Reading workspace…"
		}
	}
	out := itoa(len(d.Changes)) + " changed"
	adds, dels := 0, 0
	for _, ch := range d.Changes {
		adds += ch.Adds
		dels += ch.Dels
	}
	if adds > 0 || dels > 0 {
		out += " · +" + itoa(adds) + " −" + itoa(dels)
	}
	if d.Branch != "" {
		out += " · " + d.Branch
	}
	return out
}

// reviewFiles is the index: every changed path, its kind, and its line
// counts. Clicking one opens its diff.
//
// The index is a virtual list: a working tree can hold thousands of paths
// (git status lists every untracked file), and building them all each frame
// cost 120ms at 5000 changes. Only the rows on screen are built now; the
// list still shrinks to its content when there are fewer.
func reviewFiles(c *ui.Context, h Host, sh *ShellState, p Palette) {
	d := h.Review
	if len(d.Changes) == 0 {
		switch d.Workspace {
		case WorkspaceFailed:
			emptyState(c, p, IconAlert, "Could not read the workspace")
		case WorkspaceNotRepo:
			emptyState(c, p, IconFile, "Not a git repository — nothing to review here")
		case WorkspaceRepo:
			emptyState(c, p, IconCheck, "Working tree clean")
		default:
			emptyState(c, p, IconFile, "Reading workspace…")
		}
		return
	}
	ui.List(c, &sh.ReviewFiles, len(d.Changes)).
		ItemKey(func(i int) any { return d.Changes[i].Path }).
		MaxHeight(reviewListHeight).FillWidth().Label("Changed files").
		Rows(func(row ui.ListRow) {
			reviewFileRow(row.Context, h, p, d.Changes[row.Index])
		})
}

// reviewFileRow is one path in the index.
func reviewFileRow(c *ui.Context, h Host, p Palette, ch Change) {
	selected := ch.Path == h.Review.Selected
	row := ui.Row(c).FillWidth().Padding(Space2, panelPad).Radius(controlRadius).
		AlignItems(ui.Center).Gap(Space4)
	if h.Actions.SelectChange != nil {
		row.OnClick(func() { h.Actions.SelectChange(ch.Path) })
	}
	// Selected and hovered rows share the sidebar's lifted surface (T50),
	// not a sunken well.
	stateSurface(row, p, selected, row.Hovered())
	meta := ch.Kind
	if ch.Adds > 0 || ch.Dels > 0 {
		meta = "+" + itoa(ch.Adds) + " −" + itoa(ch.Dels)
	}
	row.Children(func() {
		markText(c, p, ch.Kind)
		ui.Text(c, ch.Path).Font(MonoFont).FontSize(TypeControl).
			TextColor(inkFor(p, selected)).SingleLine().MinWidth(0)
		ui.Box(c).Grow(1)
		ui.Text(c, meta).FontSize(TypeLabel).TextColor(p.InkMuted).Shrink(0)
	})
}

func inkFor(p Palette, strong bool) ui.Color {
	if strong {
		return p.InkStrong
	}
	return p.Ink
}

// reviewDiff is the selected file's whole change. It is the reason the drawer
// exists, so it takes the space the file list does not.
func reviewDiff(c *ui.Context, h Host, p Palette) {
	d := h.Review
	if d.Selected == "" {
		// Asking for a file to select only makes sense where there is a
		// file to select: with an empty list the pane above already says
		// why there is nothing to read, and this would contradict it.
		if len(d.Changes) == 0 {
			return
		}
		ui.Row(c).FillWidth().Grow(1).MinHeight(0).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			// The file list on the left is the affordance. A quiet glyph
			// marks the empty pane without instructing the reader.
			ui.Icon(c, IconChanges).Label("No file selected").FontSize(IconDisplay).TextColor(p.InkFaint)
		})
		return
	}
	ui.Column(c).FillWidth().Shrink(0).Padding(Space4, panelPad, Space2, panelPad).Children(func() {
		ui.Text(c, d.Selected).Font(MonoFont).FontSize(TypeLabel).TextColor(p.Accent).SingleLine()
	})
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Padding(Space0, Space0, Space3, Space0).Children(func() {
			switch {
			case d.Busy:
				ui.Text(c, "Reading diff…").FontSize(TypeControl).TextColor(p.InkMuted).Padding(Space2, panelPad)
			case d.Note != "":
				ui.Text(c, d.Note).FontSize(TypeControl).TextColor(p.InkMuted).Padding(Space2, panelPad)
			default:
				diffLines(c, d.Diff, p, maxDiffRenderLines)
				if d.Omitted > 0 {
					ui.Text(c, "… "+itoa(d.Omitted)+" more lines not read").FontSize(TypeLabel).
						TextColor(p.InkMuted).Padding(Space2, panelPad)
				}
			}
		})
	})
}

// reviewActions are the working-tree writes. Both stage the whole tree, so
// both go through the confirmation the app asks for before touching git;
// the drawer only offers them where there is something to commit.
func reviewActions(c *ui.Context, h Host, sh *ShellState, p Palette) {
	d := h.Review
	// "Nothing to commit" is a statement about a tree git answered for.
	// Where there is no tree — still reading, no repository, or unreadable
	// — the bar stays silent: the drawer above already said which it is,
	// and a second, wrong sentence would only muddle it.
	if d.Workspace != WorkspaceRepo {
		return
	}
	ui.Column(c).FillWidth().Shrink(0).Padding(Space5, panelPad).Gap(Space4).Children(func() {
		if len(d.Changes) == 0 || h.Actions.Commit == nil || sh == nil {
			ui.Text(c, "Nothing to commit").FontSize(TypeLabel).TextColor(p.InkMuted)
			return
		}
		// The app's form sizes: a 12px input and 11px buttons, as the
		// tools pages. At the widget defaults this row rendered several
		// pixels taller than everything around it and read as a louder,
		// separate panel.
		ui.TextInput(c, &sh.CommitMsg).Label("Commit message").Placeholder("Commit message…").FontSize(TypeInput).FillWidth()
		ui.Row(c).FillWidth().Gap(Space4).Children(func() {
			// Grow, not FillWidth: FillWidth is width:100% — an absolute
			// claim on the row — so the button beside it was pushed out of
			// the window (rect W=0 at x=1286 in a 1280-wide window). Grow(1)
			// takes the room the sibling leaves. Both keep their text whole
			// (§4.8.4 W1): Shrink(0) is the same rule as the status word's.
			// A commit with no message cannot be submitted. Disable the write
			// at the source, and re-check inside OnClick so a disabled-looking
			// button can never dispatch on a stale frame (design system §5).
			// The app layer keeps its own confirmation/error guard as a
			// second line of defence.
			commitReady := strings.TrimSpace(sh.CommitMsg) != "" && h.Actions.Commit != nil
			ui.PrimaryButton(c, "Commit").Background(p.Accent).TextColor(p.OnInk).
				FontSize(TypeControl).Padding(ButtonPadY, ButtonPadX).
				Grow(1).MinWidth(0).Shrink(0).Disabled(!commitReady).
				OnClick(func() {
					if strings.TrimSpace(sh.CommitMsg) == "" || h.Actions.Commit == nil {
						return
					}
					h.Actions.Commit()
				})
			if h.Actions.StageAll != nil {
				ui.Button(c, "Stage all").FontSize(TypeControl).Padding(ButtonPadY, ButtonPadX).Radius(controlRadius).TextColor(p.Ink).
					Shrink(0).OnClick(h.Actions.StageAll)
			}
		})
	})
}
