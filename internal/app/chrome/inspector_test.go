package chrome

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestInspectorWorkspacePathOpensInFileManagerWithoutChangingSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "Documents", "VC")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}

	h, _ := testHost()
	h.Conversation = true
	h.Data.ProjectPath = project
	h.StatusRight = nil
	var opened string
	resumed, focused := false, false
	h.Actions.OpenInFileManager = func(path string) { opened = path }
	h.Actions.ResumeSession = func(string) { resumed = true }
	h.Actions.FocusSession = func(string) { focused = true }

	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	mustHave(t, ts, "~/Documents/VC")
	btn, ok := ts.Find("Open workspace folder in Finder")
	if !ok || btn.W <= 0 {
		t.Fatalf("path is not an actionable button: %+v", btn)
	}
	// Click the visible text, not the accessibility label: the whole path
	// hit area belongs to the button and the click must not reach the
	// session row behind the inspector.
	if err := ts.Click("~/Documents/VC"); err != nil {
		t.Fatal(err)
	}
	if opened != project {
		t.Fatalf("opened %q, want the absolute workspace %q", opened, project)
	}
	if resumed || focused {
		t.Fatalf("revealing the folder changed the session: resumed=%v focused=%v", resumed, focused)
	}
}

func TestInspectorWorkspacePathIsReadOnlyWithoutFileManagerAction(t *testing.T) {
	h, _ := testHost()
	h.Conversation = true
	h.StatusRight = nil
	h.Actions.OpenInFileManager = nil

	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	mustHave(t, ts, "/p/proj")
	if _, ok := ts.Find("Open workspace folder in Finder"); ok {
		t.Fatal("the path is clickable without a file-manager action")
	}
}

func TestInspectorWorkspacePathHidesWithoutProject(t *testing.T) {
	h, _ := testHost()
	h.Conversation = true
	h.Data.ProjectPath = ""
	h.StatusRight = nil
	h.Actions.OpenInFileManager = func(string) {}

	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	if _, ok := ts.Find("Open workspace folder in Finder"); ok {
		t.Fatal("an empty project path became a file-manager action")
	}
}
