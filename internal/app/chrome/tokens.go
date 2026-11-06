package chrome

import (
	"github.com/egoist/mygo/ui"
)

// A quiet macOS shell: one sidebar, an open work surface, and compact metadata.
const (
	TitleBarHeight     = 52
	titleBarHeight     = TitleBarHeight
	sidebarDefaultWide = 248
	// ui.Split only guarantees a ~10 DIP pane on either side of its divider.
	// Keep the app's sidebar useful and leave the conversation at least
	// mainMinWide; without this a drag can collapse either pane.
	sidebarMinWide   = 200
	sidebarMaxWide   = 360
	mainMinWide      = 420
	sessionBarHeight = 32
	statusBarHeight  = 24
	agentBarHeight   = 32
	terminalInset    = 12
	sheetRadius      = 10
	sheetBorderWidth = 1
	contentMaxWidth  = 560
	controlRadius    = 5
	panelPad         = 14

	// iconRailWide is the vertical navigation rail inside the sidebar. It
	// is part of the sidebar, so the splitter seam stays exactly at
	// SidebarWidth and the conversation keeps the width it always had.
	iconRailWide = 44
	// inspectorWide is the right-hand telemetry panel. It appears only when
	// the window can afford it beside a usable conversation.
	inspectorWide = 284
)

// Font families follow the design's dual-family architecture. JetBrains Mono
// is the workhorse and, inside a surface, the only family: the window title,
// sheet titles, panel headers, buttons, labels, readouts and code all share
// it, so a panel never changes face halfway down. Geist is reserved for the
// start screens' display headings — the large wordmark and page title — the
// one place the design's headline scale is used. The families after each
// Latin face keep Han text on the same spec: a proportional CJK companion
// for Geist, a monospaced CJK companion for JetBrains Mono. MyGo takes the
// first family with a glyph, so each script stays in its lane.
const (
	SansFont = "Geist, PingFang SC, Noto Sans CJK SC"
	MonoFont = "JetBrains Mono, Noto Sans Mono CJK SC, PingFang SC"
)

// Palette separates navigation from the working surface using near-black
// neutral surfaces. Chromatic tones are telemetry only: a running process,
// a clean exit, a saturated context window, a failure. InkMuted remains
// readable on every surface; InkFaint is decoration only.
type Palette struct {
	Window      ui.Color // bedrock: window background
	Surface     ui.Color // raised rails, panels, cards
	SurfaceHi   ui.Color // hovered or pressed rows, bubbles
	Sunken      ui.Color // meter wells, input fields
	Ink         ui.Color // primary text and strong strokes
	InkStrong   ui.Color // titles, terminal-adjacent emphasis
	InkMuted    ui.Color // secondary text (AA on all surfaces)
	InkFaint    ui.Color // decor only: ghost glyphs, never text
	Hairline    ui.Color // 1px grouping rules
	Edge        ui.Color // control borders, >= 3:1
	Accent      ui.Color // signal cyan: active marks, meters
	AccentHover ui.Color // pressed/hover accent fills
	AccentSoft  ui.Color // tinted wells behind accent content
	AccentOnBar ui.Color // emphasis text on the status bar fill
	OnAccent    ui.Color // text on an Accent fill
	OK          ui.Color // added files, done steps
	Danger      ui.Color // deleted files, failed states
	// Warn is the attention tone: a session blocked on the user is neither
	// a success nor a failure, and reusing Accent made an approval look
	// like ordinary emphasis. It is also used as text, so it carries the
	// same >= 4.5 contrast as the ink tones.
	Warn     ui.Color
	Purple   ui.Color // model/provider signatures
	TermBG   ui.Color // terminal card
	TermText ui.Color // terminal foreground
	TermDim  ui.Color // terminal secondary text (AA on TermBG)
	OnInk    ui.Color // text on Ink and Accent fills
	// Scrim and SheetShadow are the modal layer, not the workbench: they
	// dim the window behind a sheet without turning a light theme muddy.
	Scrim       ui.Color
	SheetShadow ui.Color
}

func palette(t *ui.Theme) Palette {
	if t.Dark {
		return Palette{
			Window:      ui.Hex("#090909"),
			Surface:     ui.Hex("#121214"),
			SurfaceHi:   ui.Hex("#18181B"),
			Sunken:      ui.Hex("#0E0E10"),
			Ink:         ui.Hex("#E5E1E4"),
			InkStrong:   ui.Hex("#FFFFFF"),
			InkMuted:    ui.Hex("#A1A1AA"),
			InkFaint:    ui.Hex("#52525B"),
			Hairline:    ui.Hex("#272727"),
			Edge:        ui.Hex("#797979"),
			Accent:      ui.Hex("#00F5FF"),
			AccentHover: ui.Hex("#63F7FF"),
			AccentSoft:  ui.Hex("#0E2A2D"),
			AccentOnBar: ui.Hex("#A1A1AA"),
			OnAccent:    ui.Hex("#09090B"),
			OK:          ui.Hex("#34D399"),
			Danger:      ui.Hex("#F87171"),
			Warn:        ui.Hex("#FBBF24"),
			Purple:      ui.Hex("#C084FC"),
			TermBG:      ui.Hex("#090909"),
			TermText:    ui.Hex("#E5E1E4"),
			TermDim:     ui.Hex("#A1A1AA"),
			OnInk:       ui.Hex("#09090B"),
			Scrim:       ui.RGBA(0, 0, 0, 0.55),
			SheetShadow: ui.RGBA(0, 0, 0, 0.45),
		}
	}
	return Palette{
		Window:      ui.Hex("#FFFFFF"),
		Surface:     ui.Hex("#F5F5F5"),
		SurfaceHi:   ui.Hex("#E8E8E8"),
		Sunken:      ui.Hex("#EBEBEB"),
		Ink:         ui.Hex("#242424"),
		InkStrong:   ui.Hex("#111113"),
		InkMuted:    ui.Hex("#5F5F66"),
		InkFaint:    ui.Hex("#8A8A92"),
		Hairline:    ui.Hex("#E4E4E4"),
		Edge:        ui.Hex("#858585"),
		Accent:      ui.Hex("#0E7490"),
		AccentHover: ui.Hex("#155E75"),
		AccentSoft:  ui.Hex("#E0F7FA"),
		AccentOnBar: ui.Hex("#5F5F66"),
		OnAccent:    ui.Hex("#FFFFFF"),
		OK:          ui.Hex("#047857"),
		Danger:      ui.Hex("#B91C1C"),
		Warn:        ui.Hex("#8A5A00"),
		Purple:      ui.Hex("#7E22CE"),
		TermBG:      ui.Hex("#FFFFFF"),
		TermText:    ui.Hex("#242424"),
		TermDim:     ui.Hex("#5F5F66"),
		OnInk:       ui.Hex("#FFFFFF"),
		// A 55% black veil is right on near-black, but on paper it reads as
		// disabled chrome. Lift the veil and soften the cast shadow instead.
		Scrim:       ui.RGBA(0, 0, 0, 0.22),
		SheetShadow: ui.RGBA(0, 0, 0, 0.16),
	}
}

// Color accessors for hosts outside the package that need brand tones
// (the picker page headline, for example) without exposing Palette itself.
func AccentColor(c *ui.Context) ui.Color    { return palette(c.Theme()).Accent }
func InkStrongColor(c *ui.Context) ui.Color { return palette(c.Theme()).InkStrong }
func InkMutedColor(c *ui.Context) ui.Color  { return palette(c.Theme()).InkMuted }
func InkColor(c *ui.Context) ui.Color       { return palette(c.Theme()).Ink }
func SurfaceColor(c *ui.Context) ui.Color   { return palette(c.Theme()).Surface }
func EdgeColor(c *ui.Context) ui.Color      { return palette(c.Theme()).Edge }
func HairlineColor(c *ui.Context) ui.Color  { return palette(c.Theme()).Hairline }

// mono applies the terminal face to a run of text. Every readout, path,
// badge and key hint in the shell uses it; prose stays in Geist.
func mono(e ui.Element) ui.Element {
	return e.Font(MonoFont)
}

// labelText is the shell's structural caption: compact, monospaced, and
// spaced the way the design's uppercase labels are, without uppercasing the
// string itself (the words are part of the app's accessible text).
func labelText(c *ui.Context, text string, col ui.Color) ui.Element {
	return ui.Text(c, text).Font(MonoFont).FontSize(10).FontWeight(500).
		LetterSpacing(0.6).TextColor(col).SingleLine()
}

// strongText is the shell's inline emphasis: the word that leads a card, a
// panel or a message. It stays on the workhorse face so a heading never
// changes family in the middle of a panel.
func strongText(c *ui.Context, text string, col ui.Color) ui.Element {
	return ui.Text(c, text).Font(MonoFont).FontSize(12).FontWeight(600).TextColor(col)
}

// sheetTitle is a sheet's title. A title in a different face from the body
// it heads reads as a second, unrelated panel, so it keeps the body face.
func sheetTitle(c *ui.Context, text string, col ui.Color) ui.Element {
	return ui.Text(c, text).Font(MonoFont).FontSize(13).FontWeight(600).TextColor(col)
}

// windowTitle is the title in the app's toolbar. The toolbar is part of the
// workbench, not a display headline, so it keeps the workbench face.
func windowTitle(c *ui.Context, text string, col ui.Color) ui.Element {
	return ui.Text(c, text).Font(MonoFont).FontSize(13).FontWeight(600).
		LetterSpacing(-.1).TextColor(col).SingleLine()
}

// Section labels use sentence case; the right value is meaningful metadata.
func sectionLabel(c *ui.Context, p Palette, title, detail string) {
	ui.Row(c).FillWidth().Padding(12, panelPad, 7, panelPad).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Width(3).Height(10).Background(p.Accent).Shrink(0)
		ui.Box(c).Width(7).Shrink(0)
		labelText(c, title, p.InkMuted)
		if detail != "" {
			ui.Box(c).Grow(1)
			labelText(c, detail, p.InkMuted)
		}
	})
}

func hairlineH(c *ui.Context, p Palette) {
	ui.Box(c).Height(1).FillWidth().Background(p.Hairline).Shrink(0)
}

// signalDot is the one chromatic mark the design allows on a quiet row.
func signalDot(c *ui.Context, col ui.Color, size float32) {
	ui.Box(c).Width(size).Height(size).Radius(size / 2).Background(col).Shrink(0)
}

// badge is a compact telemetry chip: a hairline box carrying a monospaced
// word in a signal tone.
func badge(c *ui.Context, p Palette, text string, col ui.Color) ui.Element {
	return ui.Row(c).Shrink(0).AlignItems(ui.Center).Padding(2, 6).Radius(3).
		BorderWidth(1).BorderColor(p.Hairline).Background(p.Surface).Children(func() {
		mono(ui.Text(c, text)).FontSize(9).FontWeight(600).LetterSpacing(0.5).TextColor(col).SingleLine()
	})
}

// segmentedMeter draws the context bar as discrete cells, like the mock's
// saturation strip. used/total come from fx's usage report; there is no
// animation and no invented headroom.
func segmentedMeter(c *ui.Context, p Palette, used, total int, cells int) {
	if total <= 0 || cells <= 0 {
		return
	}
	ratio := float64(used) / float64(total)
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio*float64(cells) + 0.5)
	col := p.Accent
	if ratio >= 0.8 {
		col = p.Warn
	}
	ui.Row(c).FillWidth().Height(7).AlignItems(ui.Center).Gap(2).Children(func() {
		for i := 0; i < cells; i++ {
			cell := p.Hairline
			if i < filled {
				cell = col
			}
			ui.Box(c).Grow(1).Height(5).Radius(1).Background(cell)
		}
	})
}

// Compact inline metrics leave the vertical space to the terminal. The
// numbers are named because the agent strip decides what fits from the same
// arithmetic it lays out with (statWidth, fitAgent): a label at statLabelSize,
// a value at statValueSize, one gutter between them and statPad on each side.
const (
	statLabelSize = 10
	statValueSize = 11
	statGap       = 5
	statPad       = 8
)

// statWidth is the room a stat block asks for, by the same arithmetic.
func statWidth(c *ui.Context, label, value string) float32 {
	w := float32(2 * statPad)
	if label != "" {
		text, _ := c.MeasureText(0, ui.Span{Text: label, Size: statLabelSize})
		w += text
	}
	if value != "" {
		text, _ := c.MeasureText(0, ui.Span{Text: value, Size: statValueSize})
		w += text
	}
	if label != "" && value != "" {
		w += statGap
	}
	return w
}

func statBlock(c *ui.Context, p Palette, label, value string, grow bool) {
	statBlockValue(c, p, label, value, grow, p.InkStrong)
}
func statBlockValue(c *ui.Context, p Palette, label, value string, grow bool, vc ui.Color) {
	row := ui.Row(c).AlignItems(ui.Center).Padding(0, statPad).Gap(statGap).MinWidth(0)
	if grow {
		row.Grow(1)
	}
	row.Children(func() {
		if label != "" {
			ui.Text(c, label).FontSize(statLabelSize).TextColor(p.InkMuted).SingleLine()
		}
		if value != "" {
			ui.Text(c, value).FontSize(statValueSize).TextColor(vc).SingleLine()
		}
	})
}

// ApplyTheme makes all MyGo controls and the terminal share the same appearance.
// It runs once at the beginning of a view; a fresh system theme arrives next frame.
func ApplyTheme(c *ui.Context, appearance string) {
	dark := c.Theme().Dark
	switch appearance {
	case "dark", "Dark":
		dark = true
	case "light", "Light":
		dark = false
	}
	base := ui.LightTheme()
	if dark {
		base = ui.DarkTheme()
	}
	t := *base
	p := palette(base)
	t.Background, t.Surface = p.Window, p.Surface
	t.SurfaceHover, t.SurfacePressed = p.SurfaceHi, p.Sunken
	t.Border, t.Text, t.TextMuted = p.Edge, p.Ink, p.InkMuted
	// Native controls keep the monochrome action tone; the cyan signal is
	// drawn by the shell's own marks, not by every button at once.
	t.Accent, t.AccentHover, t.AccentPressed, t.AccentText = p.Ink, p.AccentHover, p.AccentHover, p.OnInk
	t.Selection, t.Focus = p.SurfaceHi, p.Edge
	t.Inverse, t.InverseText, t.Success, t.Danger = p.Ink, p.OnInk, p.OK, p.Danger
	t.Radius, t.FontSize = controlRadius, 13
	// Body, controls, sidebar navigation and prose share the design's
	// workhorse face; top-level headings and modal titles opt into Geist.
	t.Font = MonoFont
	c.SetTheme(&t)
}

// TerminalColors allows the host to match the embedded terminal to this shell.
func TerminalColors(dark bool) (background, foreground, selection ui.Color) {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	p := palette(t)
	return p.TermBG, p.TermText, p.SurfaceHi
}
