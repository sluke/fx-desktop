package prefs

import (
	"os"
	"testing"

	"fx-desktop/internal/configdir"
	"fx-desktop/internal/testutil"
)

// a toggle whose factory default is true cannot be saved as false.
// Save omits false (omitempty) and Load starts from Defaults(), so the
// missing key reads back as true on the next launch.
func TestRegressionR9_DisabledTogglesSurviveRestart(t *testing.T) {
	testutil.IsolateHome(t)
	p := Defaults()
	p.RestoreLastProject = false
	p.TrackRecentProjects = false
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	path, err := configdir.PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	t.Logf("prefs.json on disk:\n%s", raw)
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.RestoreLastProject {
		t.Errorf("RestoreLastProject: saved false, loaded true")
	}
	if got.TrackRecentProjects {
		t.Errorf("TrackRecentProjects: saved false, loaded true")
	}
}
