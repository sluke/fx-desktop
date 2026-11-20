package chrome

import "github.com/egoist/mygo/ui"

func inspectorPanel(c *ui.Context, h Host, sh *ShellState, p Palette, width float32) {
	ui.Column(c).Shrink(0).Width(width).FillHeight().Background(p.Surface).
		BorderWidth(0, 1, 0, 0).BorderColor(p.Hairline).Children(func() {
		ui.Column(c).FillWidth().Grow(1).MinHeight(0).Children(func() {
			if h.Data.ProjectPath != "" {
				fileBrowserPanel(c, h, sh, p)
			}
		})
		contextSection(c, h, p)
	})
}

func contextSection(c *ui.Context, h Host, p Palette) {
	a := h.Agent
	sessionTokens := a.Tokens
	if sessionTokens == "" {
		sessionTokens = "—"
	}
	ui.Column(c).Shrink(0).FillWidth().Padding(Space4, panelPad).Gap(Space3).BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			mono(ui.Text(c, "Context & Memory")).FontSize(TypeLabel).TextColor(p.InkStrong)
			ui.Box(c).Grow(1).MinWidth(0)
			if a.ContextTotal > 0 {
				sat := float64(a.ContextUsed) / float64(a.ContextTotal) * 100
				col := p.Accent
				if sat >= 80 {
					col = p.Warn
				}
				mono(ui.Text(c, satText(sat))).FontSize(TypeBadge).TextColor(col)
			}
		})
		if a.ContextTotal > 0 {
			segmentedMeter(c, p, a.ContextUsed, a.ContextTotal, 28)
			mono(ui.Text(c, countText(a.ContextUsed)+" / "+countText(a.ContextTotal)+" tokens")).FontSize(TypeLabel).TextColor(p.InkMuted)
		} else {
			mono(ui.Text(c, "Context usage unavailable")).FontSize(TypeLabel).TextColor(p.InkMuted)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			mono(ui.Text(c, "Session tokens")).FontSize(TypeLabel).TextColor(p.InkMuted)
			ui.Box(c).Grow(1).MinWidth(0)
			mono(ui.Text(c, sessionTokens)).FontSize(TypeLabel).TextColor(p.InkStrong)
		})
	})
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
