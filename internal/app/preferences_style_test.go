package app

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/prefs"
	"fx-desktop/internal/testutil"
)

func TestPrefsUseFlatGroupsAndCompactControls(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, size := range [][2]int{{520, 480}, {520, 560}, {720, 720}} {
			t.Run(fmt.Sprintf("dark=%v/%dx%d", dark, size[0], size[1]), func(t *testing.T) {
				testutil.IsolateHome(t)
				c := NewCoordinator()
				c.prefs.Appearance = prefs.AppearanceLight
				want := ui.Hex("#FFFFFF")
				if dark {
					c.prefs.Appearance = prefs.AppearanceDark
					want = ui.Hex("#090909")
				}
				c.prefsState.p = c.prefs
				ts := ui.NewTester(c.prefsView, size[0], size[1])
				ts.Frame()
				heading, ok := ts.Find("General")
				if !ok {
					t.Fatal("missing General heading")
				}
				pixel := ts.Image().RGBAAt(size[0]-32, int(heading.Y+heading.H/2))
				if pixel.R != want.R || pixel.G != want.G || pixel.B != want.B {
					t.Fatalf("group has a separate card fill: got %v, want window %v", pixel, want)
				}
				choose, ok := ts.Find("Choose…")
				if !ok || choose.H > 28.5 {
					t.Fatalf("Choose control too tall: %+v", choose)
				}
				general, _ := ts.Find("General")
				appearance, _ := ts.Find("Appearance")
				if appearance.Y-general.Y > 270 {
					t.Fatalf("general section too tall: %v", appearance.Y-general.Y)
				}
				for _, label := range []string{"Save", "Revert"} {
					r, ok := ts.Find(label)
					if !ok || r.H > 28.5 || r.Y < float32(size[1]-88) || r.Y+r.H > float32(size[1]-24) {
						t.Fatalf("%s lost compact fixed footer: %+v", label, r)
					}
				}
				// Tester includes off-screen content geometry, so this also
				// checks the theme at the minimum window without fake scrolling.
				system, _ := ts.Find("System")
				for _, label := range []string{"Dark", "Light"} {
					r, ok := ts.Find(label)
					if !ok || r.Y != system.Y || r.X <= system.X {
						t.Fatalf("theme choices are not horizontal: %s %+v vs %+v", label, r, system)
					}
				}
			})
		}
	}
}

// Choosing a theme previews it in the preferences window immediately; Save
// still owns what the other windows and the next launch use.
func TestPrefsThemeRadioPreviewsImmediately(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.prefs.Appearance = prefs.AppearanceLight
	c.prefsState.p = c.prefs
	ts := ui.NewTester(c.prefsView, 520, 560)
	ts.SetDark(false)
	ts.Frame()
	light, dark := ui.Hex("#FFFFFF"), ui.Hex("#090909")
	check := func(name string, want ui.Color) {
		t.Helper()
		got := ts.Image().RGBAAt(8, 300)
		if got.R != want.R || got.G != want.G || got.B != want.B {
			t.Fatalf("%s: window background %v, want %v", name, got, want)
		}
	}
	check("saved light", light)
	if err := ts.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	check("dark preview", dark)
	if c.prefs.Appearance != prefs.AppearanceLight {
		t.Fatal("preview wrote the saved preferences")
	}
	if err := ts.Click("System"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	check("system while the OS is light", light)
	ts.SetDark(true)
	ts.Frame()
	check("system while the OS is dark", dark)
	if err := ts.Click("Light"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	check("light preview", light)
	if err := ts.Click("System"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	check("system after a light preview", dark)
}

func TestPrefsCompactThemeAndFieldsKeepSaveRevertSemantics(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	c.prefsState.p = c.prefs
	ts := ui.NewTester(c.prefsView, 520, 560)
	path := "/a long project path/with spaces"
	if err := ts.Click("Project folder path"); err != nil {
		t.Fatal(err)
	}
	ts.Type(path)
	if err := ts.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if c.prefsState.p.Appearance != prefs.AppearanceDark || c.prefsState.p.DefaultProjectDir != path {
		t.Fatalf("controls lost draft: %+v", c.prefsState.p)
	}
	if c.prefs.Appearance != prefs.AppearanceSystem || c.prefs.DefaultProjectDir != "" {
		t.Fatal("editing wrote saved preferences")
	}
	// RadioBase keeps selection and the radio group's arrow navigation.
	ts.Key(0, ui.KeyRight)
	if c.prefsState.p.Appearance != prefs.AppearanceLight {
		t.Fatal("theme radio group lost arrow navigation")
	}
	if err := ts.Click("Revert"); err != nil {
		t.Fatal(err)
	}
	if c.prefsState.p != c.prefs {
		t.Fatal("Revert did not restore all draft values")
	}
	if err := ts.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Click("Project folder path"); err != nil {
		t.Fatal(err)
	}
	ts.Type(path)
	if err := ts.Click("Save"); err != nil {
		t.Fatal(err)
	}
	saved, err := prefs.Load()
	if err != nil || saved.Appearance != prefs.AppearanceDark || saved.DefaultProjectDir != path || c.prefs != saved {
		t.Fatalf("Save did not preserve settings: %+v, %v", saved, err)
	}
}
