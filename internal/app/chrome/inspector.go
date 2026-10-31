package chrome

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

// inspectorPanel is the right-hand telemetry rail: context and memory first,
// then the runtime facts fx reported, the working tree, the windows this app
// has open, and the console. Every value comes from fx or the app's own state;
// a field fx did not report is left out rather than guessed.
//
// The panel is deliberately worded differently from the session rail. It is a
// readout beside the work, not a second copy of the navigation.
func inspectorPanel(c *ui.Context, h Host, sh *ShellState, p Palette, width float32) {
	tab := inspectorTabFor(h, sh)
	ui.Column(c).Shrink(0).Width(width).FillHeight().Background(p.Surface).
		BorderWidth(0, 1, 0, 0).BorderColor(p.Hairline).
		Children(func() {
			inspectorTabs(c, h, sh, p)
			switch tab {
			case InspectorTabFiles:
				fileBrowserPanel(c, h, sh, p)
			default:
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					ui.Column(c).FillWidth().Padding(0, 0, 12, 0).Gap(0).Children(func() {
						contextSection(c, h, p)
						runtimeSection(c, h, p)
						workspaceSection(c, h, p)
						windowsSection(c, h, p)
						consoleSection(c, h, p)
					})
				})
			}
			inspectorFoot(c, h, sh, p, tab)
		})
}

// inspectorTabFor is the tab the panel can actually draw. The file browser
// needs a workspace to browse: selecting it without one falls back to the
// readout rather than showing an empty frame with nothing to expand.
func inspectorTabFor(h Host, sh *ShellState) int {
	if sh == nil || h.Data.ProjectPath == "" {
		return InspectorTabTelemetry
	}
	if sh.InspectorTab == InspectorTabFiles {
		return InspectorTabFiles
	}
	return InspectorTabTelemetry
}

// inspectorTabs is the panel's destination row. Two tabs, not two copies of
// the same readout: telemetry beside the work, and the workspace's files when
// the question is "what is in this project".
func inspectorTabs(c *ui.Context, h Host, sh *ShellState, p Palette) {
	ui.Row(c).Shrink(0).FillWidth().Height(34).AlignItems(ui.Center).Gap(4).
		Padding(5, 8).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		inspectorTab(c, h, sh, p, InspectorTabTelemetry, IconGauge, "Inspector")
		if h.Data.ProjectPath != "" {
			inspectorTab(c, h, sh, p, InspectorTabFiles, IconExplorer, "Files")
		}
	})
}

// inspectorTab is one destination. The active tab is a lifted surface with a
// signal underline, the same mark the rail uses for its own selection.
func inspectorTab(c *ui.Context, h Host, sh *ShellState, p Palette, tab int, icon *ui.SVG, label string) {
	active := inspectorTabFor(h, sh) == tab
	col, bg := p.InkMuted, p.Surface
	if active {
		col, bg = p.InkStrong, p.SurfaceHi
	}
	btn := ui.ButtonBase(c).Label("Inspector tab: "+label).Tooltip(label).
		Height(24).Padding(0, 9).Radius(controlRadius).Background(bg).
		AlignItems(ui.Center).Shrink(0)
	if btn.Hovered() && !active {
		col, bg = p.Ink, p.SurfaceHi
		btn.Background(bg)
	}
	if active {
		btn.BorderWidth(0, 0, 2, 0).BorderColor(p.Accent)
	}
	btn.OnClick(func() { selectInspectorTab(h, sh, tab) })
	btn.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(5).Children(func() {
			ui.Icon(c, icon).FontSize(12).TextColor(col)
			mono(ui.Text(c, label)).FontSize(10).FontWeight(600).TextColor(col)
		})
	})
}

// selectInspectorTab moves the panel. The app owns the effect because the
// file browser has to start reading the workspace when it first appears; the
// chrome falls back to its own state where no action is wired.
func selectInspectorTab(h Host, sh *ShellState, tab int) {
	if sh == nil {
		return
	}
	if h.Actions.SelectInspectorTab != nil {
		h.Actions.SelectInspectorTab(tab)
		return
	}
	sh.InspectorTab = tab
}

// inspectorHead is the panel's section caption: a signal tick, a monospaced
// word, and an optional right-hand readout.
func inspectorHead(c *ui.Context, p Palette, title, detail string, col ui.Color) {
	ui.Row(c).FillWidth().Padding(14, panelPad, 7, panelPad).AlignItems(ui.Center).Gap(7).Children(func() {
		signalDot(c, col, 5)
		mono(ui.Text(c, title)).FontSize(10).FontWeight(600).LetterSpacing(0.5).TextColor(p.InkStrong)
		ui.Box(c).Grow(1)
		if detail != "" {
			mono(ui.Text(c, detail)).FontSize(9).TextColor(p.InkMuted).SingleLine()
		}
	})
}

func contextSection(c *ui.Context, h Host, p Palette) {
	a := h.Agent
	if a.ContextTotal <= 0 {
		return
	}
	sat := float64(a.ContextUsed) / float64(a.ContextTotal) * 100
	detailCol := p.Accent
	if sat >= 80 {
		detailCol = p.Warn
	}
	inspectorHead(c, p, "Context & Memory", "", p.Accent)
	ui.Column(c).FillWidth().Padding(0, panelPad, 10, panelPad).Gap(7).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.End).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				mono(ui.Text(c, "Tokens")).FontSize(9).TextColor(p.InkMuted)
				mono(ui.Text(c, countText(a.ContextUsed))).FontSize(15).FontWeight(700).TextColor(p.InkStrong)
			})
			ui.Box(c).Grow(1)
			mono(ui.Text(c, " / "+countText(a.ContextTotal)+" tkn")).FontSize(9).TextColor(p.InkMuted)
		})
		segmentedMeter(c, p, a.ContextUsed, a.ContextTotal, 28)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			mono(ui.Text(c, contextText(a))).FontSize(9).TextColor(p.InkMuted).SingleLine()
			ui.Box(c).Grow(1)
			mono(ui.Text(c, satText(sat)+" SAT")).FontSize(9).FontWeight(600).TextColor(detailCol)
		})
	})
	hairlineH(c, p)
}

// satText prints context saturation without pretending to more precision
// than the two counts carry.
func satText(sat float64) string {
	if sat < 10 {
		return itoa(int(sat)) + "%"
	}
	whole := int(sat)
	frac := int((sat - float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac) + "%"
}

func runtimeSection(c *ui.Context, h Host, p Palette) {
	a := h.Agent
	if a.Status == "" && a.Model == "" && a.Tokens == "" && a.Spend == "" && a.Elapsed == "" && a.Branch == "" && isDefaultPermission(a.Permission) {
		return
	}
	inspectorHead(c, p, "Runtime", "", p.OK)
	ui.Column(c).FillWidth().Padding(0, panelPad, 10, panelPad).Gap(0).Children(func() {
		if a.Status != "" {
			col := p.OK
			if strings.EqualFold(a.Status, "attention") {
				col = p.Warn
			} else if strings.EqualFold(a.Status, "exited") {
				col = p.InkMuted
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(1, 0).Gap(7).Children(func() {
				signalDot(c, col, 6)
				mono(ui.Text(c, a.Status)).FontSize(10).TextColor(p.InkMuted)
				ui.Box(c).Grow(1)
				if a.Elapsed != "" {
					mono(ui.Text(c, a.Elapsed)).FontSize(9).TextColor(p.InkMuted)
				}
			})
		}
		inspectorFact(c, p, "Model", shortChoice(a.Model))
		if a.Permission != "" && !isDefaultPermission(a.Permission) {
			inspectorFactColor(c, p, "Permission", a.Permission, p.Danger)
		}
		inspectorFact(c, p, "Tokens", a.Tokens)
		inspectorFact(c, p, "Spend", a.Spend)
		inspectorFact(c, p, "Branch", a.Branch)
	})
	hairlineH(c, p)
}

func inspectorFact(c *ui.Context, p Palette, label, value string) {
	inspectorFactColor(c, p, label, value, p.InkStrong)
}

func inspectorFactColor(c *ui.Context, p Palette, label, value string, col ui.Color) {
	if value == "" {
		return
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(2, 0).Gap(8).Children(func() {
		mono(ui.Text(c, label)).FontSize(9).TextColor(p.InkMuted).Width(70).Shrink(0)
		mono(ui.Text(c, value)).FontSize(10).TextColor(col).SingleLine().MinWidth(0)
	})
}

func workspaceSection(c *ui.Context, h Host, p Palette) {
	if h.Data.ProjectPath == "" {
		return
	}
	inspectorHead(c, p, "Workspace", "", p.Purple)
	add, del := changeTotals(h.Data.Changes)
	ui.Column(c).FillWidth().Padding(0, panelPad, 10, panelPad).Gap(5).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			mono(ui.Text(c, filepath.Base(h.Data.ProjectPath))).FontSize(11).TextColor(p.InkStrong).SingleLine().MinWidth(0)
		})
		switch {
		case len(h.Data.Changes) > 0:
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(1, 0).Children(func() {
				mono(ui.Text(c, itoa(len(h.Data.Changes))+" modified")).FontSize(10).TextColor(p.InkMuted).SingleLine()
				ui.Box(c).Grow(1)
				mono(ui.Text(c, "+"+itoa(add))).FontSize(9).TextColor(p.OK)
				mono(ui.Text(c, "−"+itoa(del))).FontSize(9).TextColor(p.Danger)
			})
		case h.Data.Workspace == WorkspaceRepo:
			mono(ui.Text(c, "Working tree clean")).FontSize(10).TextColor(p.InkMuted)
		case h.Data.Workspace == WorkspaceNotRepo:
			mono(ui.Text(c, "Not a git repository")).FontSize(10).TextColor(p.InkMuted)
		case h.Data.Workspace == WorkspaceFailed:
			mono(ui.Text(c, "Could not read the workspace")).FontSize(10).TextColor(p.Danger)
		default:
			mono(ui.Text(c, "Reading workspace…")).FontSize(10).TextColor(p.InkMuted)
		}
		if len(h.Data.Changes) > 0 && h.Actions.ToggleReview != nil {
			ui.Button(c, "Open in review").Label("Open changes in review").FontSize(9).
				BorderWidth(1).BorderColor(p.Hairline).Background(p.Surface).TextColor(p.InkMuted).
				Padding(3, 8).Radius(3).OnClick(h.Actions.ToggleReview)
		}
	})
	hairlineH(c, p)
}

func windowsSection(c *ui.Context, h Host, p Palette) {
	if len(h.Sessions) == 0 {
		return
	}
	inspectorHead(c, p, "Open windows", itoa(len(h.Sessions)), p.InkMuted)
	ui.Column(c).FillWidth().Padding(0, panelPad, 10, panelPad).Gap(3).Children(func() {
		for _, sum := range h.Sessions[:min(4, len(h.Sessions))] {
			sum := sum
			row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Padding(3, 4).Radius(3)
			if sum.ID == h.Active {
				row.Background(p.SurfaceHi)
			}
			row.Children(func() {
				signalDot(c, attentionColor(p, sum.Status), 5)
				mono(ui.Text(c, sum.Title)).FontSize(10).TextColor(p.Ink).SingleLine().MinWidth(0)
				ui.Box(c).Grow(1)
				if label := attentionLabel(sum.Status); label != "" {
					mono(ui.Text(c, label)).FontSize(9).TextColor(p.InkMuted).Shrink(0)
				}
			})
		}
		if len(h.Sessions) > 4 {
			mono(ui.Text(c, "+"+itoa(len(h.Sessions)-4)+" more")).FontSize(9).TextColor(p.InkFaint)
		}
	})
	hairlineH(c, p)
}

func consoleSection(c *ui.Context, h Host, p Palette) {
	if len(h.Console.Runs) == 0 {
		return
	}
	inspectorHead(c, p, "Console", itoa(len(h.Console.Runs)), p.Warn)
	ui.Column(c).FillWidth().Padding(0, panelPad, 10, panelPad).Gap(3).Children(func() {
		for _, run := range h.Console.Runs[:min(3, len(h.Console.Runs))] {
			run := run
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Padding(2, 0).Children(func() {
				mono(ui.Text(c, run.Title)).FontSize(10).TextColor(p.Ink).SingleLine().MinWidth(0)
				ui.Box(c).Grow(1)
				mono(ui.Text(c, consoleRunState(run))).FontSize(9).TextColor(consoleRunColor(run, p)).Shrink(0)
			})
		}
		if h.Actions.ToggleConsole != nil {
			ui.Button(c, "Open console").Label("Open console").FontSize(9).
				BorderWidth(1).BorderColor(p.Hairline).Background(p.Surface).TextColor(p.InkMuted).
				Padding(3, 8).Radius(3).OnClick(h.Actions.ToggleConsole)
		}
	})
	hairlineH(c, p)
}

// inspectorFoot is the panel's quiet baseline: the project path fx is
// operating on, and the way out of the panel.
func inspectorFoot(c *ui.Context, h Host, sh *ShellState, p Palette, tab int) {
	label := "fx telemetry"
	if tab == InspectorTabFiles {
		label = "workspace files"
	}
	ui.Column(c).Shrink(0).FillWidth().Padding(8, panelPad).Gap(3).
		BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			mono(ui.Text(c, label)).FontSize(9).TextColor(p.InkMuted)
			ui.Box(c).Grow(1)
			ui.Button(c, "Hide").Label("Hide inspector").FontSize(9).BorderWidth(0).
				Background(p.Surface).TextColor(p.InkMuted).Padding(0, 4).
				OnClick(func() { sh.InspectorOpen = false })
		})
		if path := ProjectSubtitle(h.Data.ProjectPath); path != "" {
			if h.Actions.OpenInFileManager == nil {
				mono(ui.Text(c, path)).FontSize(9).TextColor(p.InkFaint).SingleLine()
				return
			}
			// The path is the way to the folder itself, not another way to
			// switch workspaces: clicking it only reveals the directory the
			// window already has open.
			btn := ui.Button(c, "").Label("Open workspace folder in Finder").
				Tooltip(h.Data.ProjectPath+"\nOpen in Finder").
				FillWidth().Height(15).Padding(0, 2).MinWidth(0).ClipX().Justify(ui.Start).
				BorderWidth(0).Background(p.Surface).Radius(3).
				OnClick(func() { h.Actions.OpenInFileManager(h.Data.ProjectPath) })
			col := p.InkMuted
			hovered := btn.Hovered()
			if hovered {
				btn.Background(p.SurfaceHi)
				col = p.Ink
			}
			btn.Children(func() {
				text := mono(ui.Text(c, path)).FontSize(9).TextColor(col).SingleLine().MinWidth(0)
				if hovered {
					text.Underline()
				}
			})
		}
	})
}

func changeTotals(changes []Change) (add, del int) {
	for _, ch := range changes {
		add += ch.Adds
		del += ch.Dels
	}
	return add, del
}
