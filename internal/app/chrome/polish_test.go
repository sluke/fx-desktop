package chrome

import (
	"image"
	"image/color"
	"math"
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

// The rail's Workspace tools button used to carry the terminal glyph, while
// the palette entry for the same sheet carried the sliders. >_ promises a
// command will run, but the sheet only manages the workspace; the sliders
// already mean Settings. Both entries must share one distinct tool mark.
func TestWorkspaceToolsUsesAToolIconInTheRailAndPalette(t *testing.T) {
	for _, cmd := range Commands {
		if cmd.ID != "workspace-tools" {
			continue
		}
		if cmd.Icon != IconTools || cmd.Icon == IconTerminal || cmd.Icon == IconSettings {
			t.Fatalf("palette entry %q does not use the rail's distinct tool mark", cmd.ID)
		}
	}

	h, _ := testHost()
	h.Appearance = "dark"
	h.Actions.ManageWorkspace = noop
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 640, 480)
	ts.SetDark(true)
	ts.Frame()
	button, ok := ts.Find("Workspace tools")
	if !ok {
		t.Fatal("the tools button is missing from the rail")
	}
	prod := ts.Image()

	// Render the same button three times with the three candidate glyphs and
	// compare the drawn pixels: this checks what the rail actually draws,
	// not just which constant the source mentions.
	render := func(icon *ui.SVG) (*image.RGBA, ui.Rect) {
		ref := ui.NewTester(func(c *ui.Context) {
			ApplyTheme(c, "dark")
			railButton(c, palette(c.Theme()), icon, "Workspace tools", "Workspace tools", false, noop)
		}, iconRailWide, 34)
		ref.SetDark(true)
		ref.Frame()
		box, found := ref.Find("Workspace tools")
		if !found {
			t.Fatal("reference rail button is missing")
		}
		return ref.Image(), box
	}
	diffs := map[string]int{}
	for name, icon := range map[string]*ui.SVG{
		"terminal": IconTerminal,
		"settings": IconSettings,
		"tools":    IconTools,
	} {
		ref, box := render(icon)
		diffs[name] = railGlyphDiff(prod, button, ref, box)
	}
	if diffs["tools"] >= diffs["terminal"] || diffs["tools"] >= diffs["settings"] {
		t.Fatalf("rail glyph is not the tool mark: differing pixels %v", diffs)
	}
	if diffs["terminal"] < 30 || diffs["settings"] < 30 {
		t.Fatalf("rail glyph is too close to terminal/settings: differing pixels %v", diffs)
	}
	// Both frames could otherwise agree on an empty glyph: prove the tool
	// mark actually draws strokes inside the icon box.
	toolsRef, toolsBox := render(IconTools)
	if ink := railGlyphInk(toolsRef, toolsBox); ink < 20 {
		t.Fatalf("the tool icon draws only %d ink pixels; its SVG may be empty", ink)
	}
}

// railGlyphInk counts the icon-box pixels that differ from the button's
// background. A parsed but empty SVG would still match itself in the diff
// comparison above.
func railGlyphInk(img *image.RGBA, box ui.Rect) int {
	cx, cy := int(box.X+box.W/2), int(box.Y+box.H/2)
	bg := img.RGBAAt(int(box.X)+2, int(box.Y)+2)
	ink := 0
	for dy := -10; dy <= 10; dy++ {
		for dx := -10; dx <= 10; dx++ {
			if pixelDistance(img.RGBAAt(cx+dx, cy+dy), bg) > 24 {
				ink++
			}
		}
	}
	return ink
}

func pixelDistance(a, b color.RGBA) float64 {
	return math.Abs(float64(a.R)-float64(b.R)) +
		math.Abs(float64(a.G)-float64(b.G)) +
		math.Abs(float64(a.B)-float64(b.B))
}

// railGlyphDiff counts visibly different pixels in the 21×21 icon box,
// centered on each button. A small tolerance absorbs antialiasing and the
// half-DIP offset between a full frame and a standalone button.
func railGlyphDiff(prod *image.RGBA, prodBox ui.Rect, ref *image.RGBA, refBox ui.Rect) int {
	pcx, pcy := int(prodBox.X+prodBox.W/2), int(prodBox.Y+prodBox.H/2)
	rcx, rcy := int(refBox.X+refBox.W/2), int(refBox.Y+refBox.H/2)
	changed := 0
	for dy := -10; dy <= 10; dy++ {
		for dx := -10; dx <= 10; dx++ {
			a, b := prod.RGBAAt(pcx+dx, pcy+dy), ref.RGBAAt(rcx+dx, rcy+dy)
			if pixelDistance(a, b) > 24 {
				changed++
			}
		}
	}
	return changed
}
