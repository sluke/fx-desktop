package chrome

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"fx-desktop/internal/conversation"
)

// The session configuration model is the conversation package's.
type ConfigOption = conversation.ConfigOption
type ConfigChoice = conversation.ConfigChoice
type ModeOption = conversation.ModeOption

// configLabel never renders an empty control: fx names its options, but an
// unnamed one has to stay usable.
func configLabel(o ConfigOption) string {
	if strings.TrimSpace(o.Name) != "" {
		return o.Name
	}
	return o.ID
}

// choiceLabel is the display text for a value. A value fx did not list (it
// changed underneath us) shows as itself rather than as blank.
func choiceLabel(o ConfigOption, value string) string {
	for _, ch := range o.Options {
		if ch.Value == value {
			if strings.TrimSpace(ch.Name) != "" {
				return ch.Name
			}
			return ch.Value
		}
	}
	return value
}

// shortChoice keeps a value readable in the strip: fx names models by a long
// path, and the tail is the part that identifies the model.
func shortChoice(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '/'); i >= 0 && i+1 < len(s) {
		return s[i+1:]
	}
	return s
}

// findConfig returns the option with this id.
func findConfig(options []ConfigOption, id string) (ConfigOption, bool) {
	return conversation.FindConfig(options, id)
}

// ModeConfig is the option fx uses for the ACP mode: the one whose category
// says so. Matching the category first keeps a renamed id working, and both
// the settings overview and the app resolve the same published option.
func ModeConfig(options []ConfigOption) (ConfigOption, bool) {
	return conversation.ModeConfig(options)
}

// selectableConfigs are the options that become a strip button. The mode is
// excluded from the compact strip; it remains in the complete settings overview.
func selectableConfigs(options []ConfigOption) []ConfigOption {
	out := make([]ConfigOption, 0, len(options))
	for _, o := range options {
		if o.Category == "mode" || o.ID == "mode" {
			continue
		}
		out = append(out, o)
	}
	return out
}

// toggleConfig opens one option's picker, or closes it when it is already
// open, so a strip button is a real toggle rather than a panel that has to be
// dismissed somewhere else.
func toggleConfig(state *ConversationState, id string) {
	state.ConfigListOpen = false
	if state.ConfigOpen == id {
		state.ConfigOpen = ""
		return
	}
	state.ConfigOpen = id
	state.ConfigQuery = ""
}

// closeConfig dismisses the open picker.
func closeConfig(state *ConversationState) {
	state.ConfigListOpen = false
	state.ConfigOpen = ""
	state.ConfigQuery = ""
}

// The strip's geometry, named because the composer decides what fits on its
// row from the same arithmetic the strip lays out with: a button carries its
// label at configLabelSize with configButtonPad on each side, a fixed folder
// button switches the workspace, and one gutter joins whatever
// the strip shows.
const (
	configLabelSize      = 11
	configStripGap       = 4
	configButtonPad      = 7
	workspaceButtonWidth = 28
)

// configStripLabels is the label each option's button carries, in fx's order.
// The composer measures them before the strip is built: an option is shown
// whole or left out, and which one it is cannot depend on a layout pass that
// has not run yet.
func configStripLabels(options []ConfigOption) []string {
	opts := selectableConfigs(options)
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, fmt.Sprintf("%s · %s", configLabel(o), shortChoice(choiceLabel(o, o.CurrentValue))))
	}
	return out
}

// configButtonWidth is the room one option's button asks for.
func configButtonWidth(c *ui.Context, label string) float32 {
	text, _ := c.MeasureText(0, ui.Span{Text: label, Size: configLabelSize})
	return text + 2*configButtonPad
}

// configStrip is the session's live configuration under the composer: the
// workspace switch first, followed by non-mode fx options, up to shown of them — the composer measures how many
// fit and passes the count in. ClipX is the fence behind that arithmetic: a
// button the strip did not count is cut at its own edge rather than drawn
// across the send arrow beside it.
func configStrip(c *ui.Context, state *ConversationState, h ConversationHost, p Palette, shown int) {
	if len(h.Configs) == 0 && h.SwitchWorkspace == nil {
		return
	}
	// A picker for an option this session no longer has (a resume moved the
	// conversation) must not linger: an empty panel is worse than none.
	if state.ConfigOpen != "" {
		if _, ok := findConfig(h.Configs, state.ConfigOpen); !ok {
			closeConfig(state)
		}
	}
	ui.Row(c).ClipX().AlignItems(ui.Center).Gap(configStripGap).Label("Composer options").Children(func() {
		if h.SwitchWorkspace != nil {
			ui.Button(c, "").Label("Switch workspace").Tooltip("Switch workspace (⌘O)").
				Size(workspaceButtonWidth, workspaceButtonWidth).Padding(0).Shrink(0).Radius(controlRadius).
				BorderWidth(0).Background(p.Surface).OnClick(h.SwitchWorkspace).
				Children(func() { ui.Icon(c, IconExplorer).FontSize(14).TextColor(p.InkMuted) })
		}
		for i, option := range selectableConfigs(h.Configs) {
			if i >= shown {
				break
			}
			option := option
			open := state.ConfigOpen == option.ID
			value := choiceLabel(option, option.CurrentValue)
			label := fmt.Sprintf("%s · %s", configLabel(option), shortChoice(value))
			button := ui.Button(c, label).FontSize(configLabelSize).BorderWidth(0).Padding(4, configButtonPad).Radius(controlRadius).
				Tooltip(fmt.Sprintf("%s: %s", configLabel(option), value)).
				Disabled(!h.Ready || h.ConfigChanging).
				OnClick(func() { toggleConfig(state, option.ID) })
			if open {
				button.Background(p.Sunken).TextColor(p.InkStrong)
			} else {
				button.Background(p.Surface).TextColor(p.InkMuted)
			}
		}
	})
}

// configOverview preserves access to every option when the compact strip
// yields its buttons. Values remain the authoritative snapshot from fx.
func configOverview(c *ui.Context, state *ConversationState, h ConversationHost, p Palette) {
	if len(h.Configs) == 0 {
		closeConfig(state)
		return
	}
	ui.Column(c).FillWidth().MaxHeight(260).Radius(6).Background(p.Surface).
		BorderWidth(1).BorderColor(p.Hairline).Clip().Label("All session settings panel").Children(func() {
		ui.Row(c).FillWidth().Padding(8, 10).AlignItems(ui.Center).Children(func() {
			strongText(c, "Session settings", p.InkStrong)
			ui.Box(c).Grow(1)
			ui.Button(c, "Close").FontSize(10).BorderWidth(0).Background(p.Surface).OnClick(func() { closeConfig(state) })
		})
		ui.Scroll(c).FillWidth().MaxHeight(210).Children(func() {
			for _, option := range h.Configs {
				option := option
				c.Key(option.ID)
				ui.ButtonBase(c).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).
					Label("Configure " + configLabel(option)).Disabled(!h.Ready || h.ConfigChanging || len(option.Options) == 0).
					OnClick(func() { toggleConfig(state, option.ID) }).Children(func() {
					ui.Text(c, configLabel(option)).FontSize(12).TextColor(p.Ink).Shrink(0)
					ui.Box(c).Grow(1)
					ui.Text(c, shortChoice(choiceLabel(option, option.CurrentValue))).FontSize(11).TextColor(p.InkMuted).SingleLine().MinWidth(0)
					ui.Icon(c, IconChevron).FontSize(12).TextColor(p.InkMuted)
				})
			}
			ui.ButtonBase(c).FillWidth().Padding(8, 10).Gap(8).AlignItems(ui.Center).
				Label("Reasoning and speed").Disabled(!h.Ready || h.ConfigChanging || h.SetAdvanced == nil).
				OnClick(func() {
					state.ConfigListOpen = false
					state.ConfigOpen = ""
					state.AdvancedOpen = true
					if h.Effort == "" {
						state.AdvancedEffort = "auto"
					} else {
						state.AdvancedEffort = h.Effort
					}
					state.AdvancedFast, state.AdvancedUltra = h.Fast, h.Ultra
				}).Children(func() {
				ui.Text(c, "Reasoning and speed").FontSize(12).TextColor(p.Ink).Shrink(0)
				ui.Box(c).Grow(1)
				ui.Icon(c, IconChevron).FontSize(12).TextColor(p.InkMuted)
			})
		})
	})
}

// advancedSettings edits the model-dependent settings fx accepts through
// session/set_config_option but does not publish in session/new. They are
// deliberately kept out of the ordinary option list: a model that rejects
// one should show a setting error, not look like a broken conversation.
func advancedSettings(c *ui.Context, state *ConversationState, h ConversationHost, p Palette) {
	ui.Column(c).FillWidth().MaxHeight(340).Radius(6).Background(p.Surface).
		BorderWidth(1).BorderColor(p.Hairline).Clip().Label("Reasoning and speed panel").Children(func() {
		ui.Row(c).FillWidth().Padding(8, 10).AlignItems(ui.Center).Children(func() {
			strongText(c, "Reasoning and speed", p.InkStrong)
			ui.Box(c).Grow(1)
			ui.Button(c, "Close").FontSize(10).BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).
				OnClick(func() { state.AdvancedOpen = false; state.Input.Focus(c) })
		})
		ui.Scroll(c).FillWidth().MaxHeight(245).Children(func() {
			ui.Column(c).FillWidth().Padding(2, 10, 10, 10).Gap(8).Children(func() {
				// A model that rejects one of these reports its own error
				// below; the panel does not need to predict it in prose.
				mono(ui.Text(c, "Reasoning effort")).FontSize(9).TextColor(p.InkMuted)
				ui.RadioGroup(c, func() {
					for _, level := range []string{"auto", "minimal", "low", "medium", "high", "xhigh"} {
						ui.Radio(c, &state.AdvancedEffort, level, level).FontSize(11)
					}
				})
				ui.Column(c).FillWidth().Gap(2).Children(func() {
					ui.Checkbox(c, &state.AdvancedFast, "Fast mode").FontSize(11)
					ui.Checkbox(c, &state.AdvancedUltra, "Ultra mode").FontSize(11)
				})
				if h.AdvancedProblem != "" {
					ui.Text(c, h.AdvancedProblem).FontSize(10).TextColor(p.Danger).Wrap().Selectable()
				}
			})
		})
		ui.Row(c).FillWidth().Padding(8, 10, 10, 10).Gap(8).Justify(ui.End).
			BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
			ui.Button(c, "Apply").FontSize(11).Padding(6, 9).Disabled(!h.Ready || h.ConfigChanging || h.SetAdvanced == nil).
				OnClick(func() {
					if h.SetAdvanced == nil {
						return
					}
					h.SetAdvanced(state.AdvancedEffort, state.AdvancedFast, state.AdvancedUltra)
					state.AdvancedOpen = false
					state.Input.Focus(c)
				})
		})
	})
}

// configDropdown is the picker for one config option. It opens directly above
// the composer instead of as a window-level sheet: it belongs to this
// conversation, and a long model list scrolls inside it.
func configDropdown(c *ui.Context, state *ConversationState, h ConversationHost, p Palette) {
	option, ok := findConfig(h.Configs, state.ConfigOpen)
	if !ok {
		closeConfig(state)
		return
	}
	ui.Column(c).FillWidth().MaxHeight(260).Radius(6).Background(p.Surface).
		BorderWidth(1).BorderColor(p.Hairline).Clip().
		Children(func() {
			ui.Row(c).FillWidth().Padding(8, 10).AlignItems(ui.Center).Gap(8).Children(func() {
				mono(ui.Text(c, configLabel(option))).FontSize(10).FontWeight(500).TextColor(p.InkStrong).SingleLine()
				ui.Box(c).Grow(1)
				ui.Button(c, "Close").FontSize(10).BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).
					OnClick(func() { closeConfig(state) })
			})
			// A filter only earns its space when the list is long enough to
			// need one; three permission modes do not.
			if len(option.Options) > 8 {
				ui.Row(c).FillWidth().Padding(0, 10, 6, 10).Children(func() {
					ui.TextInput(c, &state.ConfigQuery).Placeholder("Filter…").FontSize(12).BorderWidth(0).Background(p.Surface).FillWidth()
				})
			}
			ui.Scroll(c).FillWidth().MaxHeight(200).Children(func() {
				q := strings.ToLower(strings.TrimSpace(state.ConfigQuery))
				shown := 0
				for _, choice := range option.Options {
					choice := choice
					text := choiceLabel(option, choice.Value)
					if q != "" && !strings.Contains(strings.ToLower(text+" "+choice.Value), q) {
						continue
					}
					shown++
					active := choice.Value == option.CurrentValue
					ui.ButtonBase(c).FillWidth().Padding(6, 10).AlignItems(ui.Center).Gap(8).
						Disabled(!h.Ready || h.ConfigChanging || h.SetConfig == nil).
						OnClick(func() {
							closeConfig(state)
							if !active && h.SetConfig != nil {
								h.SetConfig(option.ID, choice.Value)
							}
						}).
						Children(func() {
							ui.Text(c, text).FontSize(12).TextColor(p.Ink).SingleLine()
							if choice.Name != choice.Value && choice.Name != "" {
								ui.Text(c, choice.Value).FontSize(10).TextColor(p.InkMuted).SingleLine()
							}
							ui.Box(c).Grow(1)
							if active {
								mono(ui.Text(c, "current")).FontSize(9).TextColor(p.Accent)
							}
						})
				}
				if shown == 0 {
					ui.Text(c, "No matching value").FontSize(12).TextColor(p.InkMuted).Padding(12, 10)
				}
			})
		})
}
