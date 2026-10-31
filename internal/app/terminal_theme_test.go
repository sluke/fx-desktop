package app

import (
	"testing"

	"fx-desktop/internal/app/chrome"
)

func TestTerminalThemesMatchWorkSurface(t *testing.T) {
	light, dark := terminalThemes()
	for i, theme := range []bool{false, true} {
		bg, fg, selection := chrome.TerminalColors(theme)
		got := light
		if i == 1 {
			got = dark
		}
		if got.Background != bg || got.Foreground != fg || got.Selection != selection || got.CursorText != bg || got.Cursor != fg {
			t.Fatalf("dark=%v terminal differs from shell", theme)
		}
	}
}

func TestTerminalAppearanceNotifiesOnlyChanges(t *testing.T) {
	var a terminalAppearance
	if changed, _ := a.observe(false); changed {
		t.Fatal("startup already probes the terminal; do not inject an unsolicited reply")
	}
	if changed, _ := a.observe(false); changed {
		t.Fatal("an ordinary redraw must not write to the process")
	}
	changed, first := a.observe(true)
	if !changed || string(appearanceReport(true)) != "\x1b[?997;1n" {
		t.Fatal("dark change must notify fx")
	}
	if changed, _ := a.observe(true); changed {
		t.Fatal("repeated build must not repeat the report")
	}
	changed, second := a.observe(false)
	if !changed || second <= first || string(appearanceReport(false)) != "\x1b[?997;2n" {
		t.Fatal("light change must supersede a queued dark notification")
	}
}
