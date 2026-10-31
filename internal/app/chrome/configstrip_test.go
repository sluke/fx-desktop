package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// configStripTexts is every string the config strip can draw, with the size it
// draws at: the test compares the box each one got against the width its own
// text needs.
func configStripTexts() map[string]float32 {
	return map[string]float32{
		"Provider · gateway":    configLabelSize,
		"Model · glm-5.3-flash": configLabelSize,
	}
}

// The composer's row is measured, not squeezed: what fits is decided from the
// widths of the things that go on it, and an item that does not fit is left
// out whole. Squeezed by a few points instead, the mode control drew "Cod" for
// "Code" — a half-word in a control reads as a bug, and the mode is what
// decides whether fx may act without asking.
func TestConversationConfigStripKeepsEveryShownItemWhole(t *testing.T) {
	sizes := configStripTexts()
	widths := []int{1600, 1400, 1200, 1000, 900, 820, 760, 700, 660, 620, 560, 500, 460, 420, 380, 360}
	hosts := []struct {
		name string
		h    ConversationHost
	}{
		{"full", ConversationHost{Ready: true, Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask"}},
		// One mode is not a choice: the strip drops the control and the row
		// has that much more room for fx's options.
		{"one mode", ConversationHost{Ready: true, Configs: fixtureConfigs(), Modes: fixtureModes()[:1], Mode: "ask"}},
		// No modes at all: the strip is only fx's options.
		{"no modes", ConversationHost{Ready: true, Configs: fixtureConfigs()}},
	}
	for _, host := range hosts {
		for _, w := range widths {
			for _, busy := range []bool{false, true} {
				state := &ConversationState{Draft: "Hello"}
				h := host.h
				h.SwitchWorkspace = func() {}
				h.Busy, h.CanSteer = busy, busy
				natural := map[string]float32{}
				first := true
				ts := ui.NewTester(func(c *ui.Context) {
					// Conversation inherits the shell family; measure and
					// draw the face the controls are actually built in.
					ApplyTheme(c, "")
					if first {
						for text, size := range sizes {
							natural[text], _ = c.MeasureText(0, ui.Span{Text: text, Size: size})
						}
						first = false
					}
					Conversation(c, state, h)
				}, w, 700)
				ts.Frame()
				ts.Frame()
				strip, ok := ts.Find("Composer options")
				if !ok {
					t.Fatalf("%s %d: the strip is missing with fx's options published", host.name, w)
				}
				for _, item := range []string{"Provider · gateway", "Model · glm-5.3-flash"} {
					r, ok := ts.Find(item)
					if !ok {
						continue // left out whole, which is the rule
					}
					if r.W+0.01 < natural[item] {
						t.Errorf("%s %dx700 (busy=%v): %q drawn %v wide, its text needs %v — a squeezed control reads as a bug",
							host.name, w, busy, item, r.W, natural[item])
					}
					if r.X+r.W > strip.X+strip.W+0.01 {
						t.Errorf("%s %dx700 (busy=%v): %q ends at %v, past the strip's edge at %v — it is being cut",
							host.name, w, busy, item, r.X+r.W, strip.X+strip.W)
					}
				}
			}
		}
	}
}

// The strip yields whole options rather than squeezing them, and it yields the
// option, not the status: whether a status shows is decided against the row's
// floor, so a drag of the window's edge never makes one blink back (see
// TestConversationStatusIsShownWholeOrNotAtAll). What the strip never yields
// is the workspace switch.
func TestConversationConfigStripYieldsBeforeWorkspaceAndStatus(t *testing.T) {
	state := &ConversationState{Draft: "Hello"}
	h := ConversationHost{Ready: true, Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask", SwitchWorkspace: func() {}}
	dropped := false
	for _, w := range []int{320, 340, 360, 380, 400, 420, 460, 500, 560, 620, 700, 760, 820} {
		ts := ui.NewTester(func(c *ui.Context) { Conversation(c, state, h) }, w, 700)
		ts.Frame()
		ts.Frame()
		provider := ts.HasText("Provider · gateway")
		model := ts.HasText("Model · glm-5.3-flash")
		if !model {
			dropped = true
		}
		// An option is only ever kept at the cost of a narrower strip, never
		// of the workspace switch or of what the row has to say about itself.
		if provider || model {
			for _, want := range []string{"Switch workspace", "Ready"} {
				if !ts.HasText(want) {
					t.Errorf("%d wide: %q is gone while an option is still shown\nhave: %v",
						w, want, ts.Texts())
				}
			}
		}
		if model && !provider {
			t.Errorf("%d wide: the model is shown without the provider — options go in fx's order\nhave: %v",
				w, ts.Texts())
		}
	}
	if !dropped {
		t.Error("no width in the sweep dropped a later option — the sweep proves nothing")
	}
}
