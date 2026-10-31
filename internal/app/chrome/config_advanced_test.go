package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAdvancedSettingsAppliesEffortFastAndUltraAsOneBatch(t *testing.T) {
	state := ConversationState{AdvancedOpen: true, AdvancedEffort: "high", AdvancedFast: true, AdvancedUltra: false}
	var effort string
	var fast, ultra bool
	h := ConversationHost{
		Ready: true, Effort: "low", Fast: false, Ultra: false,
		SetAdvanced: func(e string, f, u bool) { effort, fast, ultra = e, f, u },
	}
	ts := ui.NewTester(func(c *ui.Context) { advancedSettings(c, &state, h, palette(c.Theme())) }, 520, 420)
	ts.Frame()
	mustHave(t, ts, "Reasoning and speed", "Reasoning effort", "Fast mode", "Ultra mode")
	if err := ts.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	if effort != "high" || !fast || ultra {
		t.Fatalf("advanced=(%q,%v,%v)", effort, fast, ultra)
	}
	if state.AdvancedOpen {
		t.Fatal("advanced panel stayed open after Apply")
	}
}

func TestAdvancedSettingsShowsModelRejectionWithoutClosing(t *testing.T) {
	state := ConversationState{AdvancedOpen: true, AdvancedEffort: "auto"}
	h := ConversationHost{Ready: true, AdvancedProblem: "model does not support fast mode", SetAdvanced: func(string, bool, bool) {}}
	ts := ui.NewTester(func(c *ui.Context) { advancedSettings(c, &state, h, palette(c.Theme())) }, 520, 420)
	ts.Frame()
	mustHave(t, ts, "model does not support fast mode")
}
