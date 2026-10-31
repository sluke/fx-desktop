package chrome

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A palette's search field is part of the sheet, rather than a rounded
// form control. Focus and hover must not recolor its structural hairline.
func TestPaletteSearchKeepsFlatHairlineOnFocusAndHover(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark=%v/scale=%v", dark, scale), func(t *testing.T) {
				h, sh := testHost()
				OpenPalette(sh)
				ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
				ts.SetDark(dark)
				ts.SetScale(scale)
				ts.Frame()
				p := palette(ui.LightTheme())
				if dark {
					p = palette(ui.DarkTheme())
				}
				check := func(state string) {
					t.Helper()
					field, ok := ts.Find("Search commands")
					if !ok || !ts.Focused("Search commands") {
						t.Fatal("palette search lost automatic focus")
					}
					img := ts.Image()
					left, right := int(math.Ceil(float64((field.X+8)*scale))), int((field.X+field.W-8)*scale)
					bottom := int(math.Ceil(float64((field.Y + field.H) * scale)))
					// Below the text/caret, the header may contain only Window
					// and Hairline (or their antialiasing). Neither default input
					// focus color nor the rounded input border belongs here.
					coverage := float64(0)
					for y := bottom - int(4*scale); y < bottom; y++ {
						center := img.RGBAAt((left+right)/2, y)
						coverage += math.Abs(float64(center.R)-float64(p.Window.R)) / math.Abs(float64(p.Hairline.R)-float64(p.Window.R))
						for x := left; x < right; x++ {
							got := img.RGBAAt(x, y)
							lo, hi := min(p.Window.R, p.Hairline.R), max(p.Window.R, p.Hairline.R)
							if got.R < lo || got.R > hi || got.G != got.R || got.B != got.R {
								t.Fatalf("%s: search separator pixel (%d,%d)=%v, want only Window/Hairline range [%d,%d]", state, x, y, got, lo, hi)
							}
						}
					}
					if math.Abs(coverage-float64(scale)) > 0.1 {
						t.Fatalf("%s: separator covers %.2f pixels, want 1 DIP at scale %v", state, coverage, scale)
					}
					// The separator is straight: the line's center and both
					// ends have identical pixels, including in the old radius.
					for y := bottom - int(2*scale); y < bottom; y++ {
						center := img.RGBAAt((left+right)/2, y)
						for _, x := range []int{int((field.X + 2) * scale), int((field.X + field.W - 3) * scale)} {
							if got := img.RGBAAt(x, y); got != center {
								t.Fatalf("%s: search divider curves at (%d,%d): %v != center %v", state, x, y, got, center)
							}
						}
					}
				}
				check("autofocus")
				field, _ := ts.Find("Search commands")
				ts.Move(field.X+field.W/2, field.Y+field.H/2)
				check("hover")
				ts.Type("doctor")
				check("typing")
				mustHave(t, ts, "fx: Run doctor")
			})
		}
	}
}
