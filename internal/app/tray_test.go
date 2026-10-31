package app

import (
	"bytes"
	"image/png"
	"testing"
)

// TestTrayIconIsBlackTemplate guards the menu-bar asset: macOS only recolors a
// black-and-transparent image, so the day someone drops in the colored app tile
// the tray would turn into a dark smudge. The black-and-white pack draws a
// square badge, so the aspect also catches a stray wordmark or app tile.
func TestTrayIconIsBlackTemplate(t *testing.T) {
	cfg, err := png.DecodeConfig(bytes.NewReader(trayIcon))
	if err != nil {
		t.Fatalf("decode tray icon config: %v", err)
	}
	if cfg.Height < 36 {
		t.Fatalf("tray icon is %dx%d, want at least 36px tall for Retina", cfg.Width, cfg.Height)
	}
	ratio := float64(cfg.Width) / float64(cfg.Height)
	if ratio < 0.95 || ratio > 1.05 {
		t.Fatalf("tray icon aspect %.2f, want the square FX badge", ratio)
	}

	img, err := png.Decode(bytes.NewReader(trayIcon))
	if err != nil {
		t.Fatalf("decode tray icon: %v", err)
	}
	var ink, clear bool
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 {
				clear = true
				continue
			}
			ink = true
			if r != 0 || g != 0 || bl != 0 {
				t.Fatalf("tray icon pixel at %d,%d is not black: %d,%d,%d", x, y, r, g, bl)
			}
		}
	}
	if !ink || !clear {
		t.Fatalf("tray icon needs black ink on transparent background (ink=%v clear=%v)", ink, clear)
	}
}
