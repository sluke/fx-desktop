package app

import (
	"fx-desktop/internal/app/chrome"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// Both themes are configured once. The terminal chooses between them from the
// frame theme, so System/Light/Dark applies to running sessions immediately.
func terminalThemes() (light, dark *terminal.Theme) {
	light, dark = terminal.LightTheme(), terminal.DarkTheme()
	for i, t := range []*terminal.Theme{light, dark} {
		t.Background, t.Foreground, t.Selection = chrome.TerminalColors(i == 1)
		t.Cursor, t.CursorText, t.SelectionText = t.Foreground, t.Background, t.Foreground
	}
	// Keep ANSI colors semantic and readable on a white working surface.
	light.Palette = [16]ui.Color{
		ui.Hex("#242424"), ui.Hex("#B33737"), ui.Hex("#28734A"), ui.Hex("#826411"),
		ui.Hex("#295FAA"), ui.Hex("#87449A"), ui.Hex("#23727D"), ui.Hex("#656565"),
		ui.Hex("#656565"), ui.Hex("#B33737"), ui.Hex("#28734A"), ui.Hex("#826411"),
		ui.Hex("#295FAA"), ui.Hex("#87449A"), ui.Hex("#23727D"), ui.Hex("#858585"),
	}
	return
}

// terminalAppearance tracks the last rendered scheme. fx listens for CSI ?997
// notifications, then probes OSC 11 and redraws its own truecolor output.
// MyGo v0.5.1 answers the probe but does not emit change notifications itself.
// See https://github.com/vercel-labs/fx/blob/main/src/ui/terminal/theme_monitor.zig.
type terminalAppearance struct {
	known      bool
	dark       bool
	generation uint64
}

func (a *terminalAppearance) observe(dark bool) (changed bool, generation uint64) {
	if a.known && a.dark == dark {
		return false, a.generation
	}
	changed = a.known
	a.known, a.dark = true, dark
	a.generation++
	return changed, a.generation
}

func appearanceReport(dark bool) []byte {
	if dark {
		return []byte("\x1b[?997;1n")
	}
	return []byte("\x1b[?997;2n")
}
