package chrome

import (
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

// The provider choices are set at the body's size, like the note above
// them: a radio list set larger than the prose beside it reads as another
// panel rather than as part of this one.
func TestProviderChoicesMatchTheNoteSize(t *testing.T) {
	h, sh := testHost()
	h.Appearance = "light"
	h.Actions.ManageWorkspace = noop
	sh.Tools = ManagementState{Open: true, Page: "Account"}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.SetDark(false)
	ts.Frame()
	note, ok := ts.Find("Complete provider sign-in and configuration in the fx console.")
	if !ok {
		t.Fatal("the provider note is missing")
	}
	for _, label := range []string{"Default", "vercel", "codex", "grok"} {
		choice, ok := ts.Find(label)
		if !ok {
			t.Fatalf("provider choice %q is missing", label)
		}
		if diff := choice.H - note.H; diff > 0.5 || diff < -0.5 {
			t.Fatalf("provider choice %q is %.2f high, want the note's %.2f", label, choice.H, note.H)
		}
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
