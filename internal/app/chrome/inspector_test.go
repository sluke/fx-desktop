package chrome

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestFileRailTracksSessionContext(t *testing.T) {
	h, _ := testHost()
	h.Conversation = true
	h.Files.Root = h.Data.ProjectPath
	for i := 0; i < 80; i++ {
		name := "file-" + itoa(i)
		h.Files.Rows = append(h.Files.Rows, FileRow{Name: name, Path: name, Abs: "/p/proj/" + name})
	}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	for _, used := range []int{100, 800} {
		h.Agent.ContextUsed, h.Agent.ContextTotal = used, 1000
		ts.Frame()
		mustHave(t, ts, "Context & Memory", countText(used)+" / 1.0k tokens")
		list, ok := ts.Find("Workspace files")
		footer, found := ts.Find("Context & Memory")
		if !ok || !found || list.H <= 0 || list.Y+list.H > footer.Y || footer.Y < 600 {
			t.Fatalf("file list and footer bounds: %+v %+v", list, footer)
		}
		mustNotHave(t, ts, "fx telemetry", "Runtime", "Open windows")
	}
	h.Agent.ContextTotal = 0
	h.Agent.Tokens = "4.5M"
	ts.Frame()
	mustHave(t, ts, "Context & Memory", "Context usage unavailable", "Session tokens", "4.5M")
	mustNotHave(t, ts, "800 / 1.0k tokens")
	h.Agent.Tokens = ""
	ts.Frame()
	mustHave(t, ts, "Session tokens", "—")
	mustNotHave(t, ts, "4.5M")
}
