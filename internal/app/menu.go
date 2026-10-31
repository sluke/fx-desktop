package app

import (
	"github.com/egoist/mygo"
)

func (c *Coordinator) setupMenus() {
	c.menuRecent = &mygo.MenuItem{
		ID:    "recent",
		Label: "Open Recent",
		Submenu: []*mygo.MenuItem{
			{ID: "recent-empty", Label: "No recent projects", Disabled: true},
		},
	}
	c.rebuildRecentMenu()

	fileMenu := &mygo.MenuItem{
		Label: "File",
		Submenu: []*mygo.MenuItem{
			{
				ID:          "open-folder",
				Label:       "Open Folder…",
				Accelerator: "CmdOrCtrl+O",
				Click:       func(*mygo.MenuItem, *mygo.Window) { c.pickFolder(nil) },
			},
			c.menuRecent,
			{Type: mygo.MenuItemSeparator},
			{
				ID:          "new-window",
				Label:       "New Session",
				Accelerator: "CmdOrCtrl+T",
				Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.newSession(w) },
			},
			{Type: mygo.MenuItemSeparator},
			{Role: mygo.RoleQuit},
		},
	}
	fxMenu := &mygo.MenuItem{
		Label: "fx",
		Submenu: []*mygo.MenuItem{
			{
				Label: "Check for Updates",
				Click: func(*mygo.MenuItem, *mygo.Window) { c.checkUpdates() },
			},
			{
				Label: "Open Config Folder",
				Click: func(*mygo.MenuItem, *mygo.Window) { c.openFxConfig() },
			},
			{
				Label: "Doctor",
				Click: func(_ *mygo.MenuItem, w *mygo.Window) { c.runDoctorFocused(w) },
			},
		},
	}
	editMenu := &mygo.MenuItem{Role: mygo.RoleEditMenu}
	prefsItem := &mygo.MenuItem{
		Label:       "Preferences…",
		Accelerator: "CmdOrCtrl+,",
		Click:       func(*mygo.MenuItem, *mygo.Window) { c.showPreferences() },
	}
	// View binds the accelerators the shell prints: ⌘B, ⌘K, ⌘. and ⌘].
	viewMenu := &mygo.MenuItem{
		Label:   "View",
		Submenu: c.viewMenuItems(),
	}
	helpMenu := &mygo.MenuItem{
		Label: "Help",
		Submenu: []*mygo.MenuItem{
			{
				Label: "Documentation",
				Click: func(*mygo.MenuItem, *mygo.Window) {
					_ = mygo.Shell.OpenExternal("https://fx.sh/docs")
				},
			},
		},
	}

	c.rootMenu = mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		fileMenu,
		editMenu,
		viewMenu,
		prefsItem,
		fxMenu,
		{Role: mygo.RoleWindowMenu},
		helpMenu,
	})
	mygo.App.SetMenu(c.rootMenu)
}

// viewMenuItems binds the accelerators the shell advertises: ⌘B, ⌘K, ⌘.,
// and ⌘] (raise the next session that wants the user). Kept as pure data —
// no Menu is created — so it can be asserted without an app loop.
func (c *Coordinator) viewMenuItems() []*mygo.MenuItem {
	return []*mygo.MenuItem{
		{
			ID:          "toggle-sidebar",
			Label:       "Toggle Sidebar",
			Accelerator: "CmdOrCtrl+B",
			Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.toggleSidebar(w) },
		},
		{
			ID:          "command-palette",
			Label:       "Command Palette",
			Accelerator: "CmdOrCtrl+K",
			Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.openPalette(w) },
		},
		{
			// The palette and the Stop button both advertise ⌘.; without
			// this entry the hint would be a lie.
			ID:          "session-interrupt",
			Label:       "Session: Interrupt",
			Accelerator: "CmdOrCtrl+.",
			Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.interruptFocused(w) },
		},
		{
			ID:          "focus-attention",
			Label:       "Focus next session needing you",
			Accelerator: "CmdOrCtrl+]",
			Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.focusAttentionFocused(w) },
		},
		{
			// The status strip and the rail both print ⌘R, so it has to be
			// bound: a printed shortcut with nothing behind it is a lie.
			ID:          "review-changes",
			Label:       "Review Changes",
			Accelerator: "CmdOrCtrl+R",
			Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.toggleReviewFocused(w) },
		},
		{
			// The drawer keeps its runs when it is hidden, and the status
			// strip says so — which only works if ⌘J is really bound.
			ID:          "toggle-console",
			Label:       "Toggle Console",
			Accelerator: "CmdOrCtrl+J",
			Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.toggleConsoleFocused(w) },
		},
		// Attaching images lives here with the other session actions: the
		// Edit menu is a system role, so a shell item cannot live in it, and
		// the accelerator has to exist as a menu item or the palette's
		// "⇧⌘V" would be a promise the app does not keep.
		{
			ID:    "attach-image",
			Label: "Attach Image…",
			Click: func(_ *mygo.MenuItem, w *mygo.Window) { c.attachImageFocused(w) },
		},
		{
			ID:          "paste-image",
			Label:       "Paste Image",
			Accelerator: "CmdOrCtrl+Shift+V",
			Click:       func(_ *mygo.MenuItem, w *mygo.Window) { c.pasteImageFocused(w) },
		},
	}
}

// attachImageFocused attaches to the focused window's session; a window with
// no fx session (a terminal, the picker) has nowhere to put an image, so the
// menu item does nothing there rather than guessing.
func (c *Coordinator) attachImageFocused(w *mygo.Window) {
	if s := c.sessionForWindow(w); s != nil {
		c.pickImage(s)
	}
}

// toggleReviewFocused opens the drawer on the focused session. A window with
// no workspace (a command window) has nothing to review, so it does nothing
// there.
func (c *Coordinator) toggleReviewFocused(w *mygo.Window) {
	if s := c.sessionForWindow(w); s != nil && s.project != "" {
		c.toggleReview(s)
	}
}

func (c *Coordinator) pasteImageFocused(w *mygo.Window) {
	if s := c.sessionForWindow(w); s != nil {
		c.pasteImage(s)
	}
}

func (c *Coordinator) rebuildRecentMenu() {
	items := []*mygo.MenuItem{}
	for _, e := range c.projects.List() {
		path := e.Path
		items = append(items, &mygo.MenuItem{
			Label: path,
			Click: func(*mygo.MenuItem, *mygo.Window) { c.OpenProject(path) },
		})
	}
	if len(items) == 0 {
		items = []*mygo.MenuItem{{Label: "No recent projects", Disabled: true}}
	}
	items = append(items, mygo.Separator(), &mygo.MenuItem{
		Label: "Clear Menu",
		Click: func(*mygo.MenuItem, *mygo.Window) {
			c.projects.Clear()
			_ = c.projects.Save()
			c.rebuildRecentMenu()
		},
	})
	c.menuRecent.Submenu = items
	if c.rootMenu != nil {
		mygo.App.SetMenu(c.rootMenu)
	}
}
