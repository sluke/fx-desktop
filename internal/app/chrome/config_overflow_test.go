package chrome

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func overflowConfigs() []ConfigOption {
	return append(fixtureConfigs(),
		ConfigOption{ID: "effort", Name: "Reasoning effort", Type: "select", CurrentValue: "low", Options: []ConfigChoice{{Value: "low", Name: "Low"}, {Value: "high", Name: "High"}}},
		ConfigOption{ID: "ultrafast", Name: "Ultrafast", Type: "select", CurrentValue: "off", Options: []ConfigChoice{{Value: "off", Name: "Off"}, {Value: "on", Name: "On"}}},
	)
}

func TestNarrowConversationAllSettingsRetainsEveryOption(t *testing.T) {
	for _, tc := range []struct{ id, name, value, choice string }{{"provider", "Provider", "codex", "codex"}, {"effort", "Reasoning effort", "high", "High"}, {"ultrafast", "Ultrafast", "on", "On"}} {
		t.Run(tc.id, func(t *testing.T) {
			state := ConversationState{Draft: "keep my draft"}
			var got [][2]string
			h := ConversationHost{Ready: true, Configs: overflowConfigs(), Modes: fixtureModes(), Mode: "ask", SetConfig: func(id, value string) { got = append(got, [2]string{id, value}) }}
			ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 360, 700)
			ts.Frame()
			state.ConfigListOpen = true
			ts.Frame()
			for _, label := range []string{"Configure Provider", "Configure Model", "Configure Reasoning effort", "Configure Ultrafast"} {
				if _, ok := ts.Find(label); !ok {
					t.Errorf("all settings lost %s", label)
				}
			}
			if err := ts.Click("Configure " + tc.name); err != nil {
				t.Fatal(err)
			}
			if err := ts.Click(tc.choice); err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0] != [2]string{tc.id, tc.value} {
				t.Fatalf("config dispatch: %v", got)
			}
			if state.Draft != "keep my draft" {
				t.Fatal("opening config replaced the draft")
			}
			option, _ := findConfig(h.Configs, tc.id)
			if option.CurrentValue == tc.value {
				t.Fatal("UI optimistically mutated fx's configuration")
			}
		})
	}
}

func TestWorkspaceEntryKeepsRoomDuringResizeAndRun(t *testing.T) {
	for _, width := range []int{320, 360, 380, 420, 500, 620, 900, 1600} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("width=%d/busy=%v", width, busy), func(t *testing.T) {
				state := ConversationState{Draft: "Hello"}
				h := ConversationHost{Ready: true, Busy: busy, CanSteer: true, Configs: overflowConfigs(), Modes: fixtureModes(), Mode: "ask", SwitchWorkspace: func() {}}
				ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, width, 700)
				ts.Frame()
				ts.Frame()
				labels := []string{"Switch workspace", "Send message"}
				if busy {
					labels = append(labels, "Stop response")
				}
				boxes := []ui.Rect{}
				for _, label := range labels {
					r, ok := ts.Find(label)
					if !ok || r.W <= 0 || r.X < 0 || r.X+r.W > float32(width) {
						t.Fatalf("%s outside composer: %+v", label, r)
					}
					for _, other := range boxes {
						if r.X < other.X+other.W && r.X+r.W > other.X && r.Y < other.Y+other.H && r.Y+r.H > other.Y {
							t.Errorf("%s overlaps another control: %+v vs %+v", label, r, other)
						}
					}
					boxes = append(boxes, r)
				}
			})
		}
	}
}

func TestAllSettingsClosesAndRespectsLiveCapabilityChanges(t *testing.T) {
	state := ConversationState{}
	calls := 0
	h := ConversationHost{Ready: true, Configs: overflowConfigs(), SetConfig: func(string, string) { calls++ }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 420, 700)
	state.ConfigListOpen = true
	ts.Frame()
	ts.Key(0, ui.KeyEscape)
	if state.ConfigListOpen {
		t.Fatal("Escape did not close settings")
	}
	state.ConfigListOpen = true
	ts.Frame()
	_ = ts.Click("Configure Reasoning effort")
	h.ConfigChanging = true
	ts.Frame()
	_ = ts.Click("High")
	if calls != 0 {
		t.Fatal("config changed while waiting for fx")
	}
	h.ConfigChanging = false
	h.Configs = nil
	ts.Frame()
	if state.ConfigOpen != "" || state.ConfigListOpen {
		t.Fatal("vanished capability left settings open")
	}
}
