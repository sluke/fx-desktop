package chrome

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The right rail is two destinations now. The Files tab lists the workspace
// the app flattened, expands folders in place, and hands a row to the default
// application — without changing what the window is working on.
func TestInspectorFilesTabBrowsesWorkspace(t *testing.T) {
	h, sh := testHost()
	h.Conversation = true
	h.StatusRight = nil
	h.Data.ProjectPath = "/p/proj"
	var selected = -1
	var toggled, opened string
	h.Actions.SelectInspectorTab = func(tab int) {
		selected = tab
		sh.InspectorTab = tab
	}
	h.Actions.ToggleFileDir = func(path string) { toggled = path }
	h.Actions.OpenFile = func(path string) { opened = path }
	h.Files = FileBrowserData{Root: "/p/proj", Rows: []FileRow{
		{Name: "src", Path: "src", Abs: "/p/proj/src", Dir: true, Expanded: true},
		{Name: "main.go", Path: "src/main.go", Abs: "/p/proj/src/main.go", Depth: 1},
		{Name: "README.md", Path: "README.md", Abs: "/p/proj/README.md"},
	}}

	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	mustNotHave(t, ts, "main.go", "README.md")

	if err := ts.Click("Inspector tab: Files"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if selected != InspectorTabFiles || sh.InspectorTab != InspectorTabFiles {
		t.Fatalf("Files tab did not become the panel: selected=%d tab=%d", selected, sh.InspectorTab)
	}
	mustHave(t, ts, "src", "main.go", "README.md", "workspace files")

	if err := ts.Click("/p/proj/src"); err != nil {
		t.Fatal(err)
	}
	if toggled != "src" {
		t.Fatalf("folder click dispatched %q, want src", toggled)
	}

	// The external-open button on a folder opens it; it must not also toggle
	// the row underneath it.
	toggled = ""
	if err := ts.Click("Open src with the default application"); err != nil {
		t.Fatal(err)
	}
	if opened != "src" {
		t.Fatalf("folder external open dispatched %q, want src", opened)
	}
	if toggled != "" {
		t.Fatalf("external open also toggled the folder: %q", toggled)
	}

	if err := ts.Click("/p/proj/src/main.go"); err != nil {
		t.Fatal(err)
	}
	if opened != "src/main.go" {
		t.Fatalf("file click opened %q, want src/main.go", opened)
	}

	// The row's own affordance opens with the default application too: this
	// is the one path that works for a folder without expanding it first.
	if err := ts.Click("Open README.md with the default application"); err != nil {
		t.Fatal(err)
	}
	if opened != "README.md" {
		t.Fatalf("external open dispatched %q, want README.md", opened)
	}

	if err := ts.Click("Inspector tab: Inspector"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	mustHave(t, ts, "Runtime")
	mustNotHave(t, ts, "main.go", "README.md")
}

func TestInspectorHidesFilesTabWithoutWorkspace(t *testing.T) {
	h, sh := testHost()
	h.Conversation = true
	h.Data.ProjectPath = ""
	sh.InspectorTab = InspectorTabFiles

	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()
	if _, ok := ts.Find("Inspector tab: Files"); ok {
		t.Fatal("a window without a workspace offered the file browser")
	}
	// A stale selection must not leave the panel empty.
	mustHave(t, ts, "Runtime")
}

// A file row's context menu chooses the kind of application: the text editor
// or whatever owns the file's type. A folder has no text editor to open in,
// so its menu offers only the default application.
func TestInspectorFileRowContextMenu(t *testing.T) {
	h, sh := testHost()
	h.Conversation = true
	h.StatusRight = nil
	h.Data.ProjectPath = "/p/proj"
	sh.InspectorTab = InspectorTabFiles
	var opened, edited string
	h.Actions.OpenFile = func(path string) { opened = path }
	h.Actions.OpenFileInEditor = func(path string) { edited = path }
	h.Files = FileBrowserData{Root: "/p/proj", Rows: []FileRow{
		{Name: "src", Path: "src", Abs: "/p/proj/src", Dir: true},
		{Name: "main.go", Path: "src/main.go", Abs: "/p/proj/src/main.go"},
	}}

	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	ts.Frame()

	if err := ts.RightClick("/p/proj/src/main.go"); err != nil {
		t.Fatal(err)
	}
	if got := ts.Menu(); len(got) != 2 || got[0] != "Open with Text Editor" || got[1] != "Open with Default Application" {
		t.Fatalf("file menu = %v", got)
	}
	if err := ts.ChooseMenuItem("Open with Text Editor"); err != nil {
		t.Fatal(err)
	}
	if edited != "src/main.go" {
		t.Fatalf("text editor dispatched %q, want src/main.go", edited)
	}

	if err := ts.RightClick("/p/proj/src/main.go"); err != nil {
		t.Fatal(err)
	}
	if err := ts.ChooseMenuItem("Open with Default Application"); err != nil {
		t.Fatal(err)
	}
	if opened != "src/main.go" {
		t.Fatalf("default application dispatched %q, want src/main.go", opened)
	}

	if err := ts.RightClick("/p/proj/src"); err != nil {
		t.Fatal(err)
	}
	if got := ts.Menu(); len(got) != 1 || got[0] != "Open with Default Application" {
		t.Fatalf("folder menu = %v", got)
	}
	if err := ts.ChooseMenuItem("Open with Default Application"); err != nil {
		t.Fatal(err)
	}
	if opened != "src" {
		t.Fatalf("folder default application dispatched %q, want src", opened)
	}

	// The ↗ button inside the row has no menu of its own; the row's menu is
	// still the one that opens over it.
	if err := ts.RightClick("Open src with the default application"); err != nil {
		t.Fatal(err)
	}
	if got := ts.Menu(); len(got) != 1 || got[0] != "Open with Default Application" {
		t.Fatalf("nested button menu = %v", got)
	}
	ts.CloseMenu()
}
