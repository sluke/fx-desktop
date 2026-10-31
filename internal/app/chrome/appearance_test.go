package chrome

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func contrast(a, b ui.Color) float64 {
	luminance := func(c ui.Color) float64 {
		channel := func(v uint8) float64 {
			n := float64(v) / 255
			if n <= 0.04045 {
				return n / 12.92
			}
			return math.Pow((n+0.055)/1.055, 2.4)
		}
		return .2126*channel(c.R) + .7152*channel(c.G) + .0722*channel(c.B)
	}
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + .05) / (y + .05)
}

func TestPaletteTextContrast(t *testing.T) {
	for _, theme := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		p := palette(theme)
		for _, surface := range []ui.Color{p.Window, p.Surface, p.SurfaceHi} {
			for name, ink := range map[string]ui.Color{"primary": p.Ink, "strong": p.InkStrong, "secondary": p.InkMuted, "warn": p.Warn} {
				if ratio := contrast(ink, surface); ratio < 4.5 {
					t.Errorf("dark=%v %s text contrast %.2f <4.5", theme.Dark, name, ratio)
				}
			}
			if ratio := contrast(p.Edge, surface); ratio < 3 {
				t.Errorf("dark=%v control contrast %.2f <3", theme.Dark, ratio)
			}
		}
		if ratio := contrast(p.TermText, p.TermBG); ratio < 4.5 {
			t.Errorf("terminal contrast %.2f", ratio)
		}
		if ratio := contrast(p.OnInk, p.Ink); ratio < 4.5 {
			t.Errorf("button contrast %.2f", ratio)
		}
	}
}

func TestAppearanceOverridesAndReturnsToSystem(t *testing.T) {
	h, _ := testHost()
	var dark bool
	var bg ui.Color
	h.Main = func(c *ui.Context) { dark = c.Theme().Dark; bg = c.Theme().Background; ui.Text(c, "Work surface") }
	h.Appearance = "dark"
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	if !dark || bg != palette(ui.DarkTheme()).Window {
		t.Fatal("explicit dark appearance was not applied to content")
	}
	h.Appearance = "light"
	ts.SetDark(true)
	ts.Frame()
	if dark || bg != palette(ui.LightTheme()).Window {
		t.Fatal("explicit light must override a dark system")
	}
	h.Appearance = "system"
	ts.Frame()
	if !dark {
		t.Fatal("returning to System must follow the OS, not the previous override")
	}
	ts.SetDark(false)
	ts.Frame()
	if dark {
		t.Fatal("System must follow a later OS change")
	}
}

func TestNarrowShellKeepsWorkspaceAndPrimaryControlsVisible(t *testing.T) {
	h, _ := testHost()
	h.Agent.Permission = "full-access"
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 480)
	for _, label := range []string{"TERMINALVIEW", "New Session", "Settings", "Elapsed"} {
		r, ok := ts.Find(label)
		if !ok || r.X < 0 || r.Y < 0 || r.X+r.W > 720.5 || r.Y+r.H > 480.5 {
			t.Errorf("%s outside window: %+v, found=%v", label, r, ok)
		}
	}
	mustNotHaveToken(t, ts, "Tokens", "Spend")
	if err := ts.Click("Toggle Sidebar"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	mustHave(t, ts, "TERMINALVIEW", "Commands")
	mustNotHave(t, ts, "Sessions")
	if err := ts.Click("Toggle Sidebar"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	mustHave(t, ts, "Sessions")
}
