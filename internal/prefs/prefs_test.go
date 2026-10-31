package prefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fx-desktop/internal/configdir"
	"fx-desktop/internal/testutil"
)

func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.FontSize != 14 || d.Appearance != AppearanceSystem {
		t.Fatalf("unexpected defaults: %+v", d)
	}
	if !d.RestoreLastProject || !d.TrackRecentProjects {
		t.Fatalf("unexpected flags: %+v", d)
	}
	if d.CheckUpdatesOnStart {
		t.Fatal("CheckUpdatesOnStart should default to false")
	}
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	testutil.IsolateHome(t)
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.FontSize != 14 || p.Appearance != AppearanceSystem {
		t.Fatalf("got %+v", p)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	testutil.IsolateHome(t)
	orig := Defaults()
	orig.DefaultProjectDir = "/tmp/work"
	orig.FontFamily = "JetBrains Mono"
	orig.FontSize = 16
	orig.Appearance = AppearanceDark
	orig.CloseToTray = true
	orig.LastProject = "/tmp/work"
	if err := orig.Save(); err != nil {
		t.Fatal(err)
	}
	path, err := configdir.PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultProjectDir != orig.DefaultProjectDir ||
		got.FontFamily != orig.FontFamily ||
		got.FontSize != orig.FontSize ||
		got.Appearance != orig.Appearance ||
		got.CloseToTray != orig.CloseToTray ||
		got.LastProject != orig.LastProject {
		t.Fatalf("got %+v want %+v", got, orig)
	}
}

func TestLoadInvalidJSONFallsBackToDefaults(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := configdir.PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load()
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
	if p.FontSize != 14 {
		t.Fatalf("expected defaults after bad json, got %+v", p)
	}
}

func TestSaveCreatesPrefsFile(t *testing.T) {
	testutil.IsolateHome(t)
	p := Defaults()
	p.CloseToTray = true
	p.Appearance = AppearanceLight
	if err := p.Save(); err != nil {
		t.Fatal(err)
	}
	path, err := configdir.PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "closeToTray") || !strings.Contains(string(b), "light") {
		t.Fatalf("unexpected file content: %s", b)
	}
}

func TestLoadRepairsZeroFontSize(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := configdir.PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	payload := `{"fontSize":0,"appearance":"light"}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.FontSize != 14 || p.Appearance != AppearanceLight {
		t.Fatalf("got %+v", p)
	}
}

func TestSaveReportsErrorWhenConfigDirIsBlocked(t *testing.T) {
	home := testutil.IsolateHome(t)
	// A file where the app directory belongs makes MkdirAll fail, and Save
	// must report it instead of silently losing the user's settings.
	root := filepath.Join(home, "Library", "Application Support", "fx-desktop")
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (Prefs{}).Save(); err == nil {
		t.Fatal("expected a save error")
	}
}

func TestLoadRepairsMissingAppearance(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := configdir.PrefsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"fontSize":13}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.Appearance != AppearanceSystem {
		t.Fatalf("appearance should fall back to system: %+v", p)
	}
	if p.FontSize != 13 {
		t.Fatalf("font size should survive: %+v", p)
	}
}
