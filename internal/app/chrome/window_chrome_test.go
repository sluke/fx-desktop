package chrome

import (
	"fmt"
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFrameSidebarHasOneHairline(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark=%v/scale=%v", dark, scale), func(t *testing.T) {
				h, sh := testHost()
				ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
				ts.SetDark(dark)
				ts.SetScale(scale)
				ts.Frame()
				p := palette(ui.LightTheme())
				if dark {
					p = palette(ui.DarkTheme())
				}
				img := ts.Image()
				// Sample a quiet row through the complete seam, including the
				// old rail border immediately to its left, at both pixel scales.
				for x := int(sh.SidebarWidth - 2); x <= int(sh.SidebarWidth+2); x++ {
					want := p.Surface
					if x == int(sh.SidebarWidth) {
						want = p.Hairline
					}
					if x > int(sh.SidebarWidth) {
						want = p.Window
					}
					for dx := 0; dx < int(scale); dx++ {
						got := img.RGBAAt(x*int(scale)+dx, 400*int(scale))
						if got != (color.RGBA{R: want.R, G: want.G, B: want.B, A: want.A}) {
							t.Errorf("x=%d pixel=%d: got %v want %v", x, dx, got, want)
						}
					}
				}
			})
		}
	}
}

func TestFrameTitleBarReservesBothSystemButtonRegions(t *testing.T) {
	for _, bar := range []ui.TitleBar{{Height: 52, Left: 69}, {Height: 52, Right: 150}, {Height: 52, Left: 69, Right: 150}} {
		for _, width := range []int{720, 1280} {
			h, _ := testHost()
			ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, width, 800)
			ts.SetTitleBar(bar)
			for _, label := range []string{"Toggle Sidebar", "Commands"} {
				r, ok := ts.Find(label)
				if !ok || r.W <= 0 || r.X < bar.Left+14 || r.X+r.W > float32(width)-bar.Right-12 || r.Y < 0 || r.Y+r.H > 52 {
					t.Errorf("width=%d bar=%+v %s overlaps system controls: %+v", width, bar, label, r)
				}
			}
		}
	}
}

func TestFrameSplitterKeepsNativeInputAndPaneTheme(t *testing.T) {
	h, sh := testHost()
	var border, accent, afterBorder ui.Color
	h.Main = func(c *ui.Context) {
		border, accent = c.Theme().Border, c.Theme().Accent
		ui.Text(c, "main")
	}
	ts := ui.NewTester(func(c *ui.Context) {
		Frame(c, h)
		afterBorder = c.Theme().Border
	}, 1280, 800)
	p := palette(ui.LightTheme())
	if border != p.Edge || accent != p.Ink || afterBorder != p.Edge {
		t.Fatal("split theme leaked into main pane")
	}
	div, ok := ts.Find("Divider")
	if !ok {
		t.Fatal("native divider missing")
	}
	x, y := div.X+div.W/2, float32(400)
	ts.Move(x, y)
	if ts.Cursor() != ui.CursorResizeEW {
		t.Fatal("divider lost resize cursor")
	}
	if got := ts.Image().RGBAAt(int(div.X), int(y)); got.R != p.Ink.R || got.G != p.Ink.G || got.B != p.Ink.B {
		t.Fatalf("hovered divider lost its active feedback: %v", got)
	}
	before := sh.SidebarWidth
	ts.Press(x, y)
	ts.Move(x+40, y)
	ts.Release(x+40, y)
	if sh.SidebarWidth != before+40 {
		t.Fatalf("drag width=%v, want %v", sh.SidebarWidth, before+40)
	}
	if !ts.Focused("Divider") {
		t.Fatal("drag did not focus native divider")
	}
	ts.Key(0, ui.KeyRight)
	if sh.SidebarWidth != before+50 {
		t.Fatalf("keyboard width=%v, want %v", sh.SidebarWidth, before+50)
	}
	ts.Key(0, ui.KeyLeft)
	if sh.SidebarWidth != before+40 {
		t.Fatal("left arrow did not reverse keyboard resize")
	}
	if border != p.Edge || accent != p.Ink || afterBorder != p.Edge {
		t.Fatal("split theme leaked after interaction")
	}
	sh.SidebarOpen = false
	ts.Frame()
	sh.SidebarOpen = true
	ts.Frame()
	if _, ok := ts.Find("Divider"); !ok {
		t.Fatal("native divider did not return after sidebar toggle")
	}
}
