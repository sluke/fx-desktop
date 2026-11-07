package chrome

import "github.com/egoist/mygo/ui"

// design.go is the executable design system. Every visual decision that an
// app view is allowed to make is named here: type steps, icon sizes, spacing,
// radii and the one selection surface. Views must reference these names (or
// the semantic constructors in tokens.go) instead of writing raw numbers.
// design_system_test.go enforces that rule and audits the legacy code that
// predates it. See docs/design-system.md for the contract and the workflow.

// Type scale. Five steps cover every piece of chrome text: a chip, a
// structural label, a control label, an input value, a title. Nothing in the
// shell uses a size between or outside these steps.
const (
	TypeBadge   = 9
	TypeLabel   = 10
	TypeControl = 11
	TypeInput   = 12
	TypeTitle   = 13
)

// Icon glyph scale. These are glyph boxes, not text sizes — an icon is
// allowed to be larger than a title because it carries no line height. The
// three compact steps live inside dense rows; 15 and 17 are not steps and
// show up as legacy inconsistencies in the audit.
const (
	IconTiny    = 10
	IconCompact = 11
	IconSmall   = 12
	IconMedium  = 13
	IconLarge   = 14
	IconXL      = 16
	IconDisplay = 22
)

// Content and display scales are the documented exception to the chrome
// type scale: conversation prose, the large readout and the start screens'
// wordmark. They are named here so a new off-scale size is still a decision,
// not a typo.
const (
	ContentBody    = 14
	ContentReadout = 15
	DisplayTitle   = 26
	DisplayLead    = 27
)

// Spacing runs on a 2px grid. Space7 is the standard panel gutter (panelPad).
const (
	Space0  = 0
	Space1  = 2
	Space2  = 4
	Space3  = 6
	Space4  = 8
	Space5  = 10
	Space6  = 12
	Space7  = 14
	Space8  = 16
	Space9  = 18
	Space10 = 20
	Space12 = 24
	Space16 = 32
)

// Named compact paddings. These are the deliberate off-grid exceptions the
// compact controls need to hit their height budget (T44/T45). Writing the
// name records the decision; writing 9 again is a new deviation.
const (
	ButtonPadY = 6
	ButtonPadX = 9
	InlinePadY = 3
	InlinePadX = 6
	SheetPadX  = 14
)

// Batch four (T55) removed the Legacy* aliases entirely. Every view now
// references the standard Space* / Radius* steps below; there is no longer
// an off-grid value anywhere in the shell. TestDesignSystemNoLegacyAliases
// keeps it that way: reintroducing a Legacy* name is a hard failure.
// The retune map and the user decision behind it are recorded in
// docs/design-system.md §2.1.

// Radius scale. A chip, a control, a message bubble, a sheet, a pill.
// RadiusNone / RadiusCell name the two bare values the audit has always
// allowed so a view never has to write a literal 0 or 1 again.
const (
	RadiusNone    = 0
	RadiusCell    = 1
	RadiusChip    = 3
	RadiusControl = 5
	RadiusBubble  = 8
	RadiusSheet   = 10
	RadiusPill    = 16
)

// stateSurface is the only way a clickable row may show its state. Selection
// and hover share one lifted surface (SurfaceHi); Sunken belongs to wells and
// input fields, never to an active row (T50). Every list that highlights a
// row — rail, sidebar, history drawer, pickers — must come through here so
// the app has one selected-row style instead of one per screen.
func stateSurface(e ui.Element, p Palette, active, hovered bool) ui.Element {
	if active || hovered {
		return e.Background(p.SurfaceHi)
	}
	return e
}

// typeSize applies a named type step. New views should pass a Type* constant
// rather than a literal; the audit ratchet rejects new literal font sizes.
func typeSize(e ui.Element, step float32) ui.Element {
	return e.FontSize(step)
}
