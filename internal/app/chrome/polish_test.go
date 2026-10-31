package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Preferences had two entries: the rail's Settings button and a Settings
// row at the foot of the sidebar. The rail is the navigation spine, so the
// sidebar's copy is gone and the shortcut moved into the rail's tooltip.
func TestSettingsHasOneEntryInTheRail(t *testing.T) {
	h, _ := testHost()
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	count := 0
	for _, s := range ts.Texts() {
		if s == "Settings" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%d Settings entries on screen, want only the rail's", count)
	}
	r, ok := ts.Find("Settings")
	if !ok || r.X >= iconRailWide {
		t.Fatalf("Settings is not in the rail: %+v", r)
	}
}

// The rail fills the row between the title and status bars. Asking for the
// whole window height overflowed by those bars and flex shrink collapsed the
// first (New Session) and last (Settings) buttons to four DIPs.
func TestRailButtonsKeepTheirHeight(t *testing.T) {
	h, _ := testHost()
	h.Data.Changes = nil
	h.Data.Workspace = WorkspaceNotRepo
	h.Data.Saved = nil
	h.Sessions = []SessionSummary{{ID: "/p", Workspace: "/p", Title: "VC", Status: AttentionIdle}}
	h.Active = "/p"
	h.Actions.OpenHistory = noop
	h.Actions.ManageWorkspace = noop
	h.Actions.ToggleReview = nil
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 640, 480)
	ts.Frame()
	for _, label := range []string{"New Session", "History", "Workspace tools", "Settings"} {
		r, ok := ts.Find(label)
		if !ok {
			t.Fatalf("rail button %q missing", label)
		}
		if r.H < 30 || r.H > 40 {
			t.Fatalf("rail button %q is %.1f high, want the 34 DIP hit area", label, r.H)
		}
	}
}

// The active rail destination carries its signal edge on the left, like the
// mock's border-l, not across the top of the button.
func TestRailActiveMarkIsOnTheLeft(t *testing.T) {
	ts := ui.NewTester(func(c *ui.Context) {
		ApplyTheme(c, "dark")
		railButton(c, palette(c.Theme()), IconTerminal, "Rail test", "Rail test", true, noop)
	}, iconRailWide, 34)
	ts.SetDark(true)
	ts.Frame()
	accent := palette(ui.DarkTheme()).Accent
	img := ts.Image()
	left := img.RGBAAt(0, 17)
	top := img.RGBAAt(iconRailWide/2, 0)
	if left.R != accent.R || left.G != accent.G || left.B != accent.B {
		t.Fatalf("left edge %v, want the accent %v", left, accent)
	}
	if top.R == accent.R && top.G == accent.G && top.B == accent.B {
		t.Fatal("active mark is still drawn across the top edge")
	}
}

// The selected rail item must not reach the rail's own separator: a surface
// painted over the hairline reads as a block that has spilled into the
// sidebar beside it.
func TestRailActiveSurfaceStaysInsideTheRail(t *testing.T) {
	h, sh := testHost()
	h.Actions.ToggleReview = noop
	sh.ReviewOpen = true
	ts := ui.NewTester(func(c *ui.Context) {
		ApplyTheme(c, "light")
		iconRail(c, h, palette(c.Theme()))
	}, iconRailWide, 200)
	ts.SetDark(false)
	ts.Frame()
	p := palette(ui.LightTheme())
	img := ts.Image()
	// New Session is the first button, the active Changes the second.
	row := 8 + 34 + 4 + 17
	gutter := img.RGBAAt(iconRailWide-4, row)
	if gutter.R != 255 || gutter.G != 255 || gutter.B != 255 {
		t.Fatalf("pixel %d is %v; the active surface must stop before the rail's edge", iconRailWide-4, gutter)
	}
	sep := img.RGBAAt(iconRailWide-1, row)
	if sep.R != p.Hairline.R || sep.G != p.Hairline.G || sep.B != p.Hairline.B {
		t.Fatalf("separator pixel %v, want the hairline %v", sep, p.Hairline)
	}
}

// A light sheet must not turn the whole workbench into disabled gray. The
// scrim still dims it, but the paper stays recognizably light.
func TestSheetScrimKeepsLightThemeLight(t *testing.T) {
	h, sh := testHost()
	h.Appearance = "light"
	h.Actions.ManageWorkspace = noop
	sh.Tools = ManagementState{Open: true, Page: "Account"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.SetDark(false)
	ts.Frame()
	if !ts.HasText("Workspace tools") {
		t.Fatal("tools sheet did not open")
	}
	got := ts.Image().RGBAAt(10, 700)
	if got.R < 170 || got.R > 245 {
		t.Fatalf("light scrim pixel %v; want a light veil, not the dark 55%% one", got)
	}
}

// The sidebar's file icon used to leave "Not a git repository" four pixels
// short, wrapping an otherwise one-line fact into two rows.
func TestSidebarEmptyStateKeepsShortNoteOnOneLine(t *testing.T) {
	h, _ := testHost()
	h.Data.Changes = nil
	h.Data.Workspace = WorkspaceNotRepo
	h.Data.Saved = nil
	h.Actions.OpenHistory = nil
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	note, ok := ts.Find("Not a git repository")
	if !ok {
		t.Fatal("sidebar note missing")
	}
	if note.H > 20 {
		t.Fatalf("sidebar note is %.1f high, want one 12px line", note.H)
	}
}
