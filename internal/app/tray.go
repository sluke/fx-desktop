package app

import (
	_ "embed"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

// trayIcon is the menu-bar mark: a square FX badge, black on transparent so
// macOS tints it for light and dark menu bars. Embedding keeps the tray
// independent of where the app runs; this PNG is the rendered copy that the
// binary carries.
//
//go:embed trayicon.png
var trayIcon []byte

func (c *Coordinator) setupTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           trayIcon,
		IconIsTemplate: runtime.GOOS == "darwin",
		ToolTip:        "fx",
	})
	if err != nil {
		log.Println("tray:", err)
		return
	}
	c.tray = tray
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open", Click: func(*mygo.MenuItem, *mygo.Window) { c.showAllWindows() }},
		{Label: "Open Folder…", Click: func(*mygo.MenuItem, *mygo.Window) { c.pickFolder(nil) }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
	if runtime.GOOS == "linux" {
		tray.SetMenu(menu)
	} else {
		tray.OnClick(c.showAllWindows)
		tray.OnRightClick(func() {
			tray.SetMenu(menu)
			defer tray.SetMenu(nil)
			tray.PopUpMenu()
		})
	}
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		if c.tray != nil {
			c.tray.Destroy()
		}
	})
}
