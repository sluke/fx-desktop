package chrome

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestStatusWorkspacePathSwitchesAndFitsLongPaths(t *testing.T) {
	for _, width := range []int{720, 1280} {
		h, _ := testHost()
		calls := 0
		h.Actions.OpenFolder = func() { calls++ }
		ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, width, 800)
		if err := ts.Click("Switch workspace (status bar)"); err != nil {
			t.Fatal(err)
		}
		if calls != 1 {
			t.Fatalf("path click dispatched %d times", calls)
		}
		h.Data.ProjectPath = "/p/" + strings.Repeat("long folder/", 20) + "project"
		h.StatusRight = []string{ProjectSubtitle(h.Data.ProjectPath)}
		ts.Frame()
		r, ok := ts.Find("Switch workspace (status bar)")
		if !ok || r.W <= 0 || r.X < 0 || r.X+r.W > float32(width) || r.Y+r.H > 800 {
			t.Fatalf("path outside bar: %+v", r)
		}
		v, _ := ts.Find("fx v0.0.13")
		if v.W <= 0 || v.X+v.W > r.X {
			t.Fatalf("path displaced status: version=%+v path=%+v", v, r)
		}
		h.Data.ProjectPath = ""
		ts.Frame()
		if _, ok := ts.Find("Switch workspace (status bar)"); ok {
			t.Fatal("non-project text became a workspace action")
		}
	}
}

func TestComposerWorkspaceEntryReplacesPermissionSegments(t *testing.T) {
	for _, width := range []int{320, 380, 720, 1600} {
		for _, busy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/busy=%v", width, busy), func(t *testing.T) {
				calls := 0
				state := ConversationState{Draft: "keep this draft"}
				h := ConversationHost{Ready: !busy, Busy: busy, ConfigChanging: busy, Configs: overflowConfigs(), Modes: fixtureModes(), Mode: "ask", SwitchWorkspace: func() { calls++ }}
				ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, width, 700)
				ts.Frame()
				ts.Frame()
				mustNotHaveToken(t, ts, "Ask", "Code", "All session settings")
				if err := ts.Click("Switch workspace"); err != nil {
					t.Fatal(err)
				}
				if calls != 1 || state.Draft != "keep this draft" || state.ConfigListOpen || state.ConfigOpen != "" {
					t.Fatalf("switch changed conversation: calls=%d draft=%q", calls, state.Draft)
				}
				r, _ := ts.Find("Switch workspace")
				send, _ := ts.Find("Send message")
				if r.W != 28 || r.X < 0 || r.X+r.W > send.X {
					t.Fatalf("workspace entry squeezed or overlaps Send: %+v vs %+v", r, send)
				}
			})
		}
	}
}
