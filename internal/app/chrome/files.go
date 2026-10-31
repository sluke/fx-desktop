package chrome

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

// InspectorTabTelemetry and InspectorTabFiles are the right panel's two
// destinations: the fx readout that has always lived there, and the workspace
// itself as a browsable tree.
const (
	InspectorTabTelemetry = iota
	InspectorTabFiles
)

// FileRow is one visible row of the file browser: a folder or file at its
// depth below the workspace root. Path is workspace-relative and is the handle
// its actions take; Abs is what the default application is given.
type FileRow struct {
	Name     string
	Path     string
	Abs      string
	Dir      bool
	Depth    int
	Expanded bool
	// Note is the quiet line under an expanded folder: "Reading…" while its
	// contents are on the way, or why the folder could not be read.
	Note string
	// More marks the synthetic row that counts what a listing cap left out.
	// It is not a file, so it is not clickable.
	More bool
}

// FileBrowserData is the Files tab: the root the rows belong to and the
// flattened, ordered rows the user has expanded.
type FileBrowserData struct {
	Root    string
	Rows    []FileRow
	Loading bool
	Problem string
}

// fileBrowserPanel is the workspace tree. Rows come already flattened and
// ordered by the app, which is the only side that reads the disk; this panel
// only decides how they look and what a click means.
func fileBrowserPanel(c *ui.Context, h Host, sh *ShellState, p Palette) {
	d := h.Files
	fileBrowserHead(c, h, p)
	switch {
	case d.Problem != "":
		fileBrowserMessage(c, p, d.Problem)
	case len(d.Rows) == 0 && d.Loading:
		fileBrowserMessage(c, p, "Reading workspace…")
	case len(d.Rows) == 0:
		fileBrowserMessage(c, p, "This folder is empty")
	default:
		// The tree is a virtual list: a folder can hold hundreds of entries
		// and a deep tree thousands, so only the rows on screen are built.
		ui.List(c, &sh.FileRows, len(d.Rows)).
			ItemKey(func(i int) any {
				// A real row keeps its place when folders above it expand;
				// the synthetic count row has no path of its own.
				if d.Rows[i].More {
					return "more:" + itoa(i)
				}
				return d.Rows[i].Abs
			}).
			Grow(1).MinHeight(0).FillWidth().Padding(2, 4, 10, 4).
			Label("Workspace files").
			Rows(func(row ui.ListRow) {
				fileBrowserRow(row.Context, h, p, d.Rows[row.Index])
			})
	}
}

// fileBrowserHead names the root and offers a fresh read. The footer already
// shows the absolute path and the way to Finder, so the head stays short.
func fileBrowserHead(c *ui.Context, h Host, p Palette) {
	ui.Row(c).Shrink(0).FillWidth().Padding(10, panelPad, 5, panelPad).
		AlignItems(ui.Center).Gap(8).Children(func() {
		name := filepath.Base(h.Files.Root)
		if name == "" || name == "." || name == string(filepath.Separator) {
			name = "Workspace"
		}
		mono(ui.Text(c, name)).FontSize(10).FontWeight(600).TextColor(p.InkStrong).
			SingleLine().MinWidth(0)
		ui.Box(c).Grow(1)
		if h.Actions.RefreshFiles != nil {
			ui.Button(c, "Refresh").Label("Refresh files").Tooltip("Re-read the workspace").
				FontSize(9).BorderWidth(1).BorderColor(p.Hairline).Background(p.Surface).
				TextColor(p.InkMuted).Padding(2, 7).Radius(3).
				OnClick(h.Actions.RefreshFiles)
		}
	})
}

// fileBrowserMessage is the panel's one-line answer for empty, loading and
// failed folders. It sits where the first row would, in the same indent.
func fileBrowserMessage(c *ui.Context, p Palette, text string) {
	ui.Row(c).FillWidth().Padding(6, 10).Children(func() {
		mono(ui.Text(c, text)).FontSize(10).TextColor(p.InkMuted).SingleLine().MinWidth(0)
	})
}

// fileBrowserRow is one folder or file. Folders expand in place; files open
// with the platform's default application. Both carry the external-open
// affordance, so a folder can also be handed to Finder without being
// expanded first.
func fileBrowserRow(c *ui.Context, h Host, p Palette, row FileRow) {
	if row.More {
		ui.Row(c).FillWidth().Padding(3, 6+float32(row.Depth)*12, 3, 6).Children(func() {
			mono(ui.Text(c, row.Name)).FontSize(9).TextColor(p.InkFaint).SingleLine()
		})
		return
	}
	indent := float32(row.Depth) * 12
	bg := p.Surface
	ui.Column(c).FillWidth().Children(func() {
		btn := ui.ButtonBase(c).Label(row.Abs).Tooltip(row.Abs).
			FillWidth().Height(24).Padding(2, 3).Radius(controlRadius).
			Background(bg).AlignItems(ui.Center)
		if btn.Hovered() {
			bg = p.SurfaceHi
			btn.Background(bg)
		}
		switch {
		case row.Dir && h.Actions.ToggleFileDir != nil:
			btn.OnClick(func() { h.Actions.ToggleFileDir(row.Path) })
		case !row.Dir && h.Actions.OpenFile != nil:
			btn.OnClick(func() { h.Actions.OpenFile(row.Path) })
		}
		// The context menu is the way to choose the kind of app: a file can
		// go to the text editor or to whatever owns its type. A folder has
		// no text editor to open in — the default application is Finder.
		if h.Actions.OpenFile != nil || h.Actions.OpenFileInEditor != nil {
			btn.ContextMenu(func(m *ui.Menu) {
				if !row.Dir && h.Actions.OpenFileInEditor != nil {
					if m.Item("Open with Text Editor").Chosen() {
						h.Actions.OpenFileInEditor(row.Path)
					}
				}
				if h.Actions.OpenFile != nil {
					if m.Item("Open with Default Application").Chosen() {
						h.Actions.OpenFile(row.Path)
					}
				}
			})
		}
		btn.Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(5).Padding(0, 0, 0, indent).Children(func() {
				// The chevron slot is always present, so files line up with
				// the folders they sit beside.
				if row.Dir {
					deg := float32(0)
					if row.Expanded {
						deg = 90
					}
					ui.Icon(c, IconChevron).FontSize(11).TextColor(p.InkMuted).Shrink(0).Rotate(deg)
				} else {
					ui.Box(c).Width(11).Shrink(0)
				}
				icon := IconFile
				if row.Dir {
					icon = IconExplorer
				}
				ui.Icon(c, icon).FontSize(13).TextColor(p.InkMuted).Shrink(0)
				mono(ui.Text(c, row.Name)).FontSize(11).TextColor(p.Ink).SingleLine().MinWidth(0)
				ui.Box(c).Grow(1)
				if h.Actions.OpenFile != nil {
					ui.Button(c, "").Label("Open " + row.Name + " with the default application").
						Tooltip("Open with default application").
						Width(18).Height(18).Padding(0).BorderWidth(0).Background(bg).
						OnClick(func() { h.Actions.OpenFile(row.Path) }).Children(func() {
						ui.Icon(c, IconExternal).FontSize(10).TextColor(p.InkFaint)
					})
				}
			})
		})
		if row.Note != "" {
			ui.Row(c).FillWidth().Padding(0, 8+indent+16, 3, 6).Children(func() {
				mono(ui.Text(c, row.Note)).FontSize(9).TextColor(p.InkMuted).SingleLine().MinWidth(0)
			})
		}
	})
}
