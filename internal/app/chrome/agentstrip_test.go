package chrome

import (
	"sort"
	"testing"

	"github.com/egoist/mygo/ui"
)

// stripRect is one text the agent strip drew, with the box it got.
type stripRect struct {
	text string
	r    ui.Rect
}

// agentStripTexts is every string the agent strip can draw, with the size it
// draws at: the test compares the box each one got against the width its own
// text needs.
func agentStripTexts() map[string]float32 {
	m := map[string]float32{
		"Running":             statValueSize,
		"Model":               statLabelSize,
		"Tokens":              statLabelSize,
		"Spend":               statLabelSize,
		"Elapsed":             statLabelSize,
		"128.4k":              statValueSize,
		"$1.84":               statValueSize,
		"04:12":               statValueSize,
		"deepseek-v4.1-flash": statValueSize,
	}
	m["zenmux/deepseek/deepseek-v4.1-flash"] = statValueSize
	return m
}

// The agent strip decides what it shows from the room it has, and it is inside
// the pane: with the drawer open the pane is far narrower than the window, so
// a window-wide breakpoint promises room that is not there. That is how
// "Model" came to be drawn as "Mo…" beside a model name cut to
// "zenmux/deepseek/deepseek-v4...." while a hole as wide as the name sat
// before Elapsed.
//
// The rule is whole blocks: what is shown is shown complete, and what does not
// fit is left out — except the model, which shortens to the tail that
// identifies it before it goes.
func TestAgentStripKeepsEveryShownBlockWhole(t *testing.T) {
	sizes := agentStripTexts()
	for _, w := range []int{720, 760, 800, 900, 1000, 1100, 1280, 1440, 1600} {
		for _, drawer := range []bool{false, true} {
			h, sh := testHost()
			sh.ReviewOpen = drawer
			h.Review = ReviewData{Open: drawer, Branch: "main", Workspace: WorkspaceRepo,
				Changes: []Change{{Path: "internal/app/chrome/layout.go", Kind: "M", Adds: 12, Dels: 3}}}
			natural := map[string]float32{}
			first := true
			ts := ui.NewTester(func(c *ui.Context) {
				if first {
					// The strip is drawn after Frame applies the shell's
					// family; measure the same face it draws with.
					ApplyTheme(c, "")
					for text, size := range sizes {
						natural[text], _ = c.MeasureText(0, ui.Span{Text: text, Size: size})
					}
					first = false
				}
				Frame(c, h)
			}, w, 800)
			ts.Frame()
			ts.Frame()
			var shown []stripRect
			for text := range sizes {
				if r, ok := ts.Find(text); ok {
					shown = append(shown, stripRect{text: text, r: r})
				}
			}
			sort.Slice(shown, func(i, j int) bool { return shown[i].r.X < shown[j].r.X })
			for _, s := range shown {
				if s.r.W+0.01 < natural[s.text] {
					t.Errorf("%dx%d (drawer=%v): %q drawn %v wide, its text needs %v — a squeezed block reads as a bug",
						w, 800, drawer, s.text, s.r.W, natural[s.text])
				}
			}
			for i := 1; i < len(shown); i++ {
				if prev := shown[i-1]; prev.r.X+prev.r.W > shown[i].r.X+0.01 {
					t.Errorf("%dx%d (drawer=%v): %q runs into %q (%v > %v)",
						w, 800, drawer, prev.text, shown[i].text, prev.r.X+prev.r.W, shown[i].r.X)
				}
			}
		}
	}
}

// What the strip keeps when the pane is tight: what the session is doing and
// how long it has run. The model shortens to its tail rather than being cut to
// "zenmux/deepseek/deepseek-v4....".
func TestAgentStripShortensTheModelBeforeDroppingIt(t *testing.T) {
	h, sh := testHost()
	sh.ReviewOpen = true
	h.Review = ReviewData{Open: true, Branch: "main", Workspace: WorkspaceRepo,
		Changes: []Change{{Path: "internal/app/chrome/layout.go", Kind: "M", Adds: 12, Dels: 3}}}
	// With the drawer open the pane is narrow enough that the full model
	// path cannot sit beside the run and elapsed time, but the identifying
	// tail can. 800 is that pane: the floor (720) now has room for only the
	// run, tokens and elapsed in the mono face.
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 800, 800)
	ts.Frame()
	ts.Frame()
	mustHave(t, ts, "Model", "deepseek-v4.1-flash", "Elapsed", "04:12")
	mustNotHave(t, ts, "zenmux/deepseek/deepseek-v4.1-flash")
}
