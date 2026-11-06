package chrome

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

// inkWidths renders three lines of the same string in one frame and returns
// the ink width of each line: the first is the element under test, the
// second the workhorse face, the third the display face. Comparing the
// rendered ink is the only way to see which face an element really used.
func inkWidths(t *testing.T, label string, first func(c *ui.Context)) []int {
	t.Helper()
	ts := ui.NewTester(func(c *ui.Context) {
		ApplyTheme(c, "light")
		p := palette(c.Theme())
		ui.Column(c).Children(func() {
			first(c)
			ui.Text(c, label).Font(MonoFont).FontSize(13).FontWeight(600).TextColor(p.InkStrong)
			ui.Text(c, label).Font(SansFont).FontSize(13).FontWeight(600).TextColor(p.InkStrong)
		})
	}, 600, 200)
	ts.SetDark(false)
	ts.Frame()
	img := ts.Image()
	width, height := img.Rect.Dx(), img.Rect.Dy()
	type band struct {
		y0, y1 int
	}
	var bands []band
	inBand := false
	for y := 0; y < height; y++ {
		ink := false
		for x := 0; x < width; x++ {
			p := img.RGBAAt(x, y)
			if p.R < 200 && p.G < 200 && p.B < 200 {
				ink = true
				break
			}
		}
		switch {
		case ink && !inBand:
			bands = append(bands, band{y, y})
			inBand = true
		case ink:
			bands[len(bands)-1].y1 = y
		case !ink:
			inBand = false
		}
	}
	if len(bands) < 3 {
		t.Fatalf("rendered %d text bands, want three", len(bands))
	}
	out := make([]int, 0, 3)
	for _, b := range bands[:3] {
		first, last := -1, -1
		for x := 0; x < width; x++ {
			for y := b.y0; y <= b.y1; y++ {
				p := img.RGBAAt(x, y)
				if p.R < 200 && p.G < 200 && p.B < 200 {
					if first < 0 {
						first = x
					}
					last = x
					break
				}
			}
		}
		out = append(out, last-first+1)
	}
	return out
}

// A sheet title in a different face from the body it heads reads as a
// second, unrelated panel — the one thing the design's single workhorse
// face is there to prevent.
func TestSheetTitleKeepsTheBodyFace(t *testing.T) {
	label := "Workspace tools"
	got := inkWidths(t, label, func(c *ui.Context) {
		sheetTitle(c, label, palette(c.Theme()).InkStrong)
	})
	wantWorkhorseFace(t, "sheet title", got)
}

// The toolbar belongs to the workbench, not to the display headings.
func TestWindowTitleKeepsTheBodyFace(t *testing.T) {
	label := "fx — VC"
	got := inkWidths(t, label, func(c *ui.Context) {
		windowTitle(c, label, palette(c.Theme()).InkStrong)
	})
	wantWorkhorseFace(t, "window title", got)
}

// The provider choices are one compact control. There is no lede above
// them to match any more: the page shows what it does through its controls,
// so the check is that no radio row grows into a panel of its own.
func TestProviderChoicesShareOneSize(t *testing.T) {
	h, sh := testHost()
	h.Appearance = "light"
	h.Actions.ManageWorkspace = noop
	sh.Tools = ManagementState{Open: true, Page: "Account"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.SetDark(false)
	ts.Frame()
	var size float32
	for i, label := range []string{"Default", "vercel", "codex", "grok"} {
		choice, ok := ts.Find(label)
		if !ok {
			t.Fatalf("provider choice %q is missing", label)
		}
		if i == 0 {
			size = choice.H
			continue
		}
		if diff := choice.H - size; diff > 0.5 || diff < -0.5 {
			t.Fatalf("provider choice %q is %.2f high, want %.2f", label, choice.H, size)
		}
	}
	if size == 0 || size > 20 {
		t.Fatalf("provider choices are %.1f high, want a compact body size", size)
	}
}

// textLineInk scans a region and returns the ink width of the first text
// line it meets, skipping full-width rules (a field's hairline border) and
// empty gaps. Ink is anything well darker than the sunken field fill and
// the edge stroke, so both stay out of the measurement.
func textLineInk(t *testing.T, img *image.RGBA, x0, y0, x1, y1 int) int {
	t.Helper()
	inkAt := func(x, y int) bool {
		p := img.RGBAAt(x, y)
		return p.R < 120 && p.G < 120 && p.B < 120
	}
	rowInk := func(y int) (count, first, last int) {
		for x := x0; x < x1; x++ {
			if inkAt(x, y) {
				if first == 0 {
					first = x
				}
				last = x
				count++
			}
		}
		return count, first, last
	}
	for y := y0; y < y1; y++ {
		count, _, _ := rowInk(y)
		if count > (x1-x0)*3/5 {
			continue // a border rule, not text
		}
		if count == 0 {
			continue // still in the gap above the field body
		}
		bx0, bx1 := x1, x0
		for yy := y; yy < y1; yy++ {
			c, first, last := rowInk(yy)
			if c == 0 || c > (x1-x0)*3/5 {
				break // the line ended, or a border rule began
			}
			if first < bx0 {
				bx0 = first
			}
			if last > bx1 {
				bx1 = last
			}
		}
		return bx1 - bx0 + 1
	}
	t.Fatal("no text line found in the scanned region")
	return 0
}

// The multiline field bodies used to take the theme's 13px default while the
// single-line inputs beside them sit at the 12px input size, so a Launch
// page read its placeholders larger than its own values. A monospaced
// placeholder's ink width scales with its size: each textarea is measured
// against a reference area at 12px carrying the same placeholder text.
func TestTextAreasFollowTheInputTypeScale(t *testing.T) {
	for _, tc := range []struct{ page, caption, line string }{
		{"Launch", "Additional directories", "one absolute path per line"},
		{"Launch", "Context limit overrides", "NAME=BYTES|off, one per line"},
		{"MCP", "HTTP headers", "K=V, one per line"},
	} {
		t.Run(tc.page+"/"+tc.caption, func(t *testing.T) {
			h, sh := testHost()
			h.Appearance = "light"
			h.Actions.ManageWorkspace = noop
			sh.Tools = ManagementState{Open: true, Page: tc.page, Transport: "http"}
			ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 800, 1100)
			ts.SetDark(false)
			ts.Frame()
			cap, ok := ts.Find(tc.caption)
			if !ok {
				t.Fatalf("caption %q is missing", tc.caption)
			}
			got := textLineInk(t, ts.Image(), int(cap.X)+8, int(cap.Y+cap.H)+2, int(cap.X+cap.W)-8, int(cap.Y+cap.H)+44)

			// The reference renders in its own frame: the sheet draws a
			// scrim over the window behind it, and that veil darkens the
			// page underneath enough to pollute an ink scan.
			ref := ui.NewTester(func(c *ui.Context) {
				ApplyTheme(c, "light")
				ui.Column(c).Padding(20).Children(func() {
					ui.TextArea(c, new(string)).Label("ref-area").Placeholder(tc.line).Lines(1, 3).FillWidth().FontSize(12)
				})
			}, 800, 200)
			ref.SetDark(false)
			ref.Frame()
			area, ok := ref.Find("ref-area")
			if !ok {
				t.Fatal("the reference area is missing")
			}
			want := textLineInk(t, ref.Image(), int(area.X)+8, int(area.Y)+6, int(area.X+area.W)-8, int(area.Y)+44)
			if diff := got - want; diff > 2 || diff < -2 {
				t.Fatalf("textarea under %q draws %q %dpx wide, want the 12px input size's %dpx", tc.caption, tc.line, got, want)
			}
		})
	}
}

// wantWorkhorseFace checks that the element under test is drawn in the
// workhorse face: within a DIP of it, and clearly not in the display face.
func wantWorkhorseFace(t *testing.T, what string, got []int) {
	t.Helper()
	if abs(got[0]-got[1]) > 1 {
		t.Fatalf("%s is %d wide, want the workhorse face's %d (display face: %d)", what, got[0], got[1], got[2])
	}
	if got[1] == got[2] {
		t.Skip("the two faces are too close in width to tell apart")
	}
	if abs(got[0]-got[2]) < 3 {
		t.Fatalf("%s is %d wide, too close to the display face's %d", what, got[0], got[2])
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
