package app

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/prefs"
	"fx-desktop/internal/testutil"
)

// Through the production Preferences view: switch the toggle off, press
// Save, start the app again.
func TestRegressionR9_PrefsToggleOffSurvivesRestartThroughUI(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.prefsState.p = c.prefs
	ts := ui.NewTester(c.prefsView, 520, 560)
	if !c.prefsState.p.TrackRecentProjects || !c.prefsState.p.RestoreLastProject {
		t.Fatalf("unexpected defaults: %+v", c.prefsState.p)
	}
	// Click the Switch itself: the caption shares its label, and the Tester
	// would otherwise aim at the caption text.
	for _, name := range []string{"Track recent projects", "Restore last project"} {
		r, ok := ts.Find(name)
		if !ok {
			t.Fatalf("%s not found", name)
		}
		clicked := false
		for x := float32(505); x > 440 && !clicked; x -= 6 {
			before := c.prefsState.p
			ts.ClickAt(x, r.Y+r.H/2)
			clicked = c.prefsState.p != before
		}
		if !clicked {
			t.Fatalf("could not operate the %s switch", name)
		}
	}
	if c.prefsState.p.TrackRecentProjects || c.prefsState.p.RestoreLastProject {
		t.Fatalf("switches did not turn the draft off: %+v", c.prefsState.p)
	}
	if err := ts.Click("Save"); err != nil {
		t.Fatal(err)
	}
	if c.prefs.TrackRecentProjects || c.prefs.RestoreLastProject {
		t.Fatalf("Save did not apply the draft in memory: %+v", c.prefs)
	}
	// Next launch.
	next := NewCoordinator()
	if next.prefs.TrackRecentProjects {
		t.Errorf("after restart TrackRecentProjects = true; the user switched it off and saved")
	}
	if next.prefs.RestoreLastProject {
		t.Errorf("after restart RestoreLastProject = true; the user switched it off and saved")
	}
}

// the Preferences draft is a whole-struct copy taken when the window
// opens, and Save writes the whole struct back. LastProject is not a
// preference the window edits, but it is in the struct, so the value
// OpenProject stored while the window was open is overwritten.
func TestRegressionR9_PrefsSaveKeepsLastProjectWrittenMeanwhile(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.prefs.LastProject = "/work/A"
	if err := c.prefs.Save(); err != nil {
		t.Fatal(err)
	}
	// showPreferences: c.prefsState.p = c.prefs
	c.prefsState.p = c.prefs
	ts := ui.NewTester(c.prefsView, 520, 560)
	// openProjectWithFlags (coordinator.go:606-607) while Preferences is open.
	c.prefs.LastProject = "/work/B"
	if err := c.prefs.Save(); err != nil {
		t.Fatal(err)
	}
	if err := ts.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Click("Save"); err != nil {
		t.Fatal(err)
	}
	saved, err := prefs.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.Appearance != prefs.AppearanceDark {
		t.Fatalf("Save lost the edited field: %+v", saved)
	}
	if saved.LastProject != "/work/B" {
		t.Errorf("LastProject = %q after Preferences Save, want %q (the project opened while the window was open)", saved.LastProject, "/work/B")
	}
}

func TestRegressionR9_MergePrefsDraftKeepsWhatTheWindowDoesNotEdit(t *testing.T) {
	saved := prefs.Defaults()
	saved.LastProject = "/work/B"
	draft := prefs.Defaults()
	draft.LastProject = "/work/A"
	draft.Appearance = prefs.AppearanceDark
	draft.CloseToTray = true
	got := mergePrefsDraft(saved, draft)
	if got.LastProject != "/work/B" {
		t.Errorf("LastProject = %q, want the saved %q", got.LastProject, "/work/B")
	}
	if got.Appearance != prefs.AppearanceDark || !got.CloseToTray {
		t.Errorf("the edited fields were not applied: %+v", got)
	}
}
