package app

import (
	"github.com/egoist/mygo"

	"fx-desktop/internal/app/chrome"
)

// Native calibration: default and zero-offset TitleBar.Left are 69 and
// 60 DIP, so X=9 preserves the system's horizontal placement. The close
// button is 14 DIP tall; Y=19 centers it in our 52 DIP toolbar. MyGo applies
// this option only on macOS; Linux uses TitleBarHeight instead.
func windowTrafficLights() *mygo.Point {
	return &mygo.Point{X: 9, Y: (chrome.TitleBarHeight - 14) / 2}
}
