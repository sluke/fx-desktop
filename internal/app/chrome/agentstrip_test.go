package chrome

import (
	"fmt"
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
//
// This is the fixed-width half of the N6/R4 contract, and it states its premise
// from the layout: the room the strip really has at 800 with the drawer open is
// a property of the resolved mono face, so a host whose face fits the full name
// there is a host where the premise does not hold — and the test then asserts
// that branch instead of failing for a font it did not choose. The rule itself
// is asserted independently in TestAgentStripFitShortensThenDropsByMeasuredRoom.
func TestAgentStripShortensTheModelBeforeDroppingIt(t *testing.T) {
	const width, height = 800, 800
	h, sh := testHost()
	sh.ReviewOpen = true
	h.Review = ReviewData{Open: true, Branch: "main", Workspace: WorkspaceRepo,
		Changes: []Change{{Path: "internal/app/chrome/layout.go", Kind: "M", Adds: 12, Dels: 3}}}

	a := Agent{Model: "zenmux/deepseek/deepseek-v4.1-flash", Status: "Running", Elapsed: "04:12"}
	tail := shortChoice(a.Model)

	// The room the strip got, and the widths of the blocks the rule weighs,
	// all measured in the frame the strip itself drew.
	var room, base, fullWidth, tailWidth float32
	agentStripRoom = func(r float32) { room = r }
	t.Cleanup(func() { agentStripRoom = nil })
	first := true
	ts := ui.NewTester(func(c *ui.Context) {
		if first {
			ApplyTheme(c, "")
			base = statWidth(c, "", a.Status) + statWidth(c, "Elapsed", a.Elapsed)
			fullWidth = statWidth(c, "Model", a.Model)
			tailWidth = statWidth(c, "Model", tail)
			first = false
		}
		Frame(c, h)
	}, width, height)
	ts.Frame()
	ts.Frame()

	measurement := fmt.Sprintf("room=%v base=%v full=%q(%v) tail=%q(%v)", room, base, a.Model, fullWidth, tail, tailWidth)
	// Recorded so a failure on another host says which face it resolved and
	// how much room that face left, instead of only that the names differ.
	t.Log(measurement)
	switch {
	case room <= 0:
		t.Fatalf("the strip never measured its room: %s", measurement)
	case room >= base+fullWidth:
		// This host's face fits the whole path in this pane. The rule says
		// show it whole — not clipped, not shortened — which is what the
		// fixed-width assertion asserted the opposite of.
		mustHave(t, ts, "Model", a.Model, "Elapsed")
		if shown, ok := ts.Find(a.Model); ok {
			if shown.W+0.01 < fullWidth {
				t.Fatalf("%s: the full name was drawn squeezed (%v < %v)", measurement, shown.W, fullWidth)
			}
		}
	case room >= base+tailWidth:
		// The premise the 800 layout was designed for: the path does not
		// fit, the identifying tail does.
		mustHave(t, ts, "Model", tail, "Elapsed")
		mustNotHave(t, ts, a.Model)
	default:
		// Not even the tail fits; the model goes, and the run stays.
		mustHave(t, ts, "Elapsed")
		mustNotHave(t, ts, a.Model, tail)
	}
}

// The shortening rule does not depend on which face the host resolves for
// "JetBrains Mono, Noto Sans Mono CJK SC, PingFang SC". The rooms below are
// derived from the same measurement the layout uses: at the room the full name
// needs, it prints the full name; one point narrower, the identifying tail; one
// point narrower than the tail, nothing rather than a clipped word.
//
// This is the font-independent half of the N6 contract. The fixed 800-wide
// assertion above still runs on hosts whose mono face makes the pane narrow
// enough for its premise; on a host that resolves a narrower face the full
// name legitimately fits, which is why the rule itself is asserted here.
func TestAgentStripFitShortensThenDropsByMeasuredRoom(t *testing.T) {
	a := Agent{Model: "zenmux/deepseek/deepseek-v4.1-flash", Status: "Running", Elapsed: "04:12"}
	tail := shortChoice(a.Model)
	if tail == a.Model {
		t.Fatalf("the model has no tail to shorten to: %q", a.Model)
	}
	var fullFit, tailFit, dropped agentFit
	var fullWidth, tailWidth, base float32
	ts := ui.NewTester(func(c *ui.Context) {
		ApplyTheme(c, "")
		base = statWidth(c, "", a.Status) + statWidth(c, "Elapsed", a.Elapsed)
		fullWidth = statWidth(c, "Model", a.Model)
		tailWidth = statWidth(c, "Model", tail)
		fullFit = fitAgent(c, a, base+fullWidth)
		tailFit = fitAgent(c, a, base+fullWidth-1)
		dropped = fitAgent(c, a, base+tailWidth-1)
	}, 800, 800)
	ts.Frame()
	if tailWidth >= fullWidth {
		t.Fatalf("the tail (%v) is not narrower than the full name (%v)", tailWidth, fullWidth)
	}
	if fullFit.Model != a.Model {
		t.Fatalf("the full name did not fit the room it needs: %q", fullFit.Model)
	}
	if tailFit.Model != tail {
		t.Fatalf("one point short of the full name showed %q, want the tail %q", tailFit.Model, tail)
	}
	if dropped.Model != "" {
		t.Fatalf("less room than the tail still showed %q", dropped.Model)
	}
}
