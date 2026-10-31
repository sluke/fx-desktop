package app

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

// The picker, welcome and preferences views are plain view functions: they
// build chrome without needing a live window, so they render in a tester.

func TestPickerViewRenders(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.fxVersion = "fx v0.0.13"
	ts := ui.NewTester(c.pickerView, 1280, 800)
	for _, want := range []string{"Open a project", "Choose folder…", "fx v0.0.13"} {
		if !ts.HasText(want) {
			t.Errorf("picker is missing %q\nhave: %v", want, ts.Texts())
		}
	}
	for _, name := range []string{"one", "two", "three", "four", "five", "six"} {
		c.projects.Touch(filepath.Join(t.TempDir(), name))
	}
	narrow := ui.NewTester(c.pickerView, 640, 480)
	if r, ok := narrow.Find("Choose folder…"); !ok || r.Y < 52 || r.Y+r.H > 456 {
		t.Fatalf("primary action hidden in a short picker: %+v, found=%v", r, ok)
	}

}

func TestPickerViewWithoutVersion(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	ts := ui.NewTester(c.pickerView, 1280, 800)
	if !ts.HasText("Select a folder. fx runs the agent in that directory") {
		t.Errorf("lede: %v", ts.Texts())
	}
}

// There is no native app loop in Tester. This click must return without
// opening a blocking native dialog inside the frame's action callback.
func TestPickerChooseFolderDefersNativeDialog(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	ts := ui.NewTester(c.pickerView, 1280, 800)
	ts.Click("Choose folder…")
	if !ts.HasText("Open a project") {
		t.Fatal("picker disappeared while the dialog was only queued")
	}
}

func TestPickerMissingRecentDefersErrorDialog(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.projects.Touch(filepath.Join(t.TempDir(), "missing-project"))
	ts := ui.NewTester(c.pickerView, 1280, 800)
	ts.Click("missing-project")
	if !ts.HasText("Open a project") {
		t.Fatal("picker must remain available until the native action runs")
	}
}

func TestSessionButtonsDeferNativeDialogs(t *testing.T) {
	changes := []chrome.Change{{Path: "a.go", Kind: "M"}}
	for _, tc := range []struct {
		name   string
		data   chrome.SidebarData
		button string
		// review opens the drawer, which is where the working-tree writes
		// live now (the rail only points at it).
		review bool
	}{
		{"commit", chrome.SidebarData{ProjectPath: "/p", Changes: changes}, "Commit", true},
		{"stage", chrome.SidebarData{ProjectPath: "/p", Changes: changes}, "Stage all", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testutil.IsolateHome(t)
			c := NewCoordinator()
			s := &Session{coord: c, project: tc.data.ProjectPath, shell: chrome.DefaultShell()}
			s.shell.ReviewOpen = tc.review
			ts := ui.NewTester(func(ctx *ui.Context) {
				chrome.Frame(ctx, chrome.Host{
					Shell: &s.shell, Actions: c.sessionActions(s), Data: tc.data,
					Review: chrome.ReviewData{
						Open:      tc.review,
						Workspace: chrome.WorkspaceRepo,
						Changes:   tc.data.Changes,
						Selected:  "a.go",
					},
				})
			}, 1280, 800)
			if !ts.HasText(tc.button) {
				t.Fatalf("missing button %q", tc.button)
			}
			ts.Click(tc.button)
		})
	}
}

func TestWelcomeViewRendersInstallStep(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	ts := ui.NewTester(c.welcomeView, 1280, 800)
	for _, want := range []string{"Install the fx CLI", "Download fx", "Documentation"} {
		if !ts.HasText(want) {
			t.Errorf("welcome is missing %q\nhave: %v", want, ts.Texts())
		}
	}
}

func TestWelcomeViewRendersBusyState(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.welcomeState.busy = true
	c.welcomeState.status = "Downloading…"
	ts := ui.NewTester(c.welcomeView, 1280, 800)
	if !ts.HasText("Downloading…") {
		t.Errorf("busy status: %v", ts.Texts())
	}
	if ts.HasText("Download fx") {
		t.Errorf("the button should be replaced while busy: %v", ts.Texts())
	}
}

// A binary that exists but cannot run is not the same as one that was never
// installed. The screen has to say which case this is, or the user is told
// to install something they already have.
func TestWelcomeViewExplainsBrokenBinary(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.fxBroken = true
	ts := ui.NewTester(c.welcomeView, 1280, 800)
	for _, want := range []string{"Repair the fx CLI", "could not be started", "Download fx"} {
		if !ts.HasText(want) {
			t.Errorf("repair screen is missing %q\nhave: %v", want, ts.Texts())
		}
	}
	if ts.HasText("The CLI is not installed yet") {
		t.Errorf("the repair screen must not claim fx was never installed: %v", ts.Texts())
	}
}

func TestPrefsViewRendersEverySection(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	ts := ui.NewTester(c.prefsView, 640, 720)
	for _, want := range []string{
		"Preferences",
		"Default project folder", "Font family", "Font size", "Appearance",
		"Restore last project", "Track recent projects", "Close window to menu bar",
		"Check on launch",
		"Save", "Revert",
	} {
		if !ts.HasText(want) {
			t.Errorf("preferences is missing %q\nhave: %v", want, ts.Texts())
		}
	}
}

// The preferences window declares a 520pt minimum. The footer must keep
// Save/Revert inside that window instead of letting the explanatory line
// push them out.
func TestPrefsViewFitsMinimumWindow(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	ts := ui.NewTester(c.prefsView, 520, 480)
	for _, label := range []string{"Save", "Revert"} {
		r, ok := ts.Find(label)
		if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 520.5 || r.Y+r.H > 480.5 {
			t.Fatalf("%s clipped in the minimum window: %+v, found=%v", label, r, ok)
		}
	}
}

func TestPrefsViewEditsACopyUntilSave(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	before := c.prefs.FontSize
	c.prefsState.p.FontSize = before + 3
	if c.prefs.FontSize != before {
		t.Fatal("editing the draft must not touch the saved prefs")
	}
}
