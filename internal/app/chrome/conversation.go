package chrome

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"fx-desktop/internal/conversation"
	"fx-desktop/internal/workspace"
)

// copyFeedbackDuration is how long the Copy button reads "Copied" before it
// reverts, mirroring the design mock. A var so tests can shorten it.
var copyFeedbackDuration = 1600 * time.Millisecond

// DiffLine kinds are the workspace's semantic classification; chrome only
// maps them to colors. They are re-exported so the rendering code keeps its
// vocabulary without owning the data model.
const (
	DiffContext = workspace.DiffContext
	DiffAdd     = workspace.DiffAdd
	DiffDel     = workspace.DiffDel
)

// The transcript, approval and configuration models are the conversation
// package's. chrome renders them and maps their semantic fields to colors; it
// does not own them, so the app never has to depend on this package to name a
// message.
type ChatMessage = conversation.Message
type ShellResult = conversation.ShellResult
type DiffInfo = conversation.DiffInfo
type PermissionOption = conversation.PermissionOption
type ChatPermission = conversation.Permission

// DiffLine is one classified diff line: Kind is "context", "add" or "del".
type DiffLine = workspace.DiffLine

type ConversationState struct {
	NoticesOpen bool
	Draft       string
	List        ui.ListState
	Markdown    markdown.Options
	Expanded    map[uint64]bool
	// DiffFolded records diffs the user collapsed. A diff shows by default,
	// so absence means "open".
	DiffFolded map[uint64]bool
	// Payloads holds each expanded tool call's laid-out payload, keyed by
	// message. A frame must not re-parse a session-sized JSON: the format
	// and the alignment are the same until the text changes.
	Payloads map[uint64]payloadCache
	// ConfigOpen is the fx config id whose picker is open ("" = none) and
	// ConfigQuery filters its choices.
	ConfigOpen     string
	ConfigQuery    string
	ConfigListOpen bool
	// AdvancedOpen edits the model-dependent effort, fast and Ultra settings
	// that fx accepts through session/set_config_option but does not list in
	// session/new. The drafts are copied from the host when the panel opens.
	AdvancedOpen   bool
	AdvancedEffort string
	AdvancedFast   bool
	AdvancedUltra  bool
	Input          ui.Handle
	// Attachments are the images waiting to ride the next prompt, and
	// AttachError explains why the last one was refused ("" = no refusal).
	Attachments []Attachment
	AttachError string
	// Mention is the @ picker over the composer: the mention the caret is in
	// (MentionStart–MentionEnd, so taking one replaces exactly it), what has
	// been typed of it, and which row is highlighted.
	MentionOpen              bool
	MentionQuery             string
	MentionStart, MentionEnd int
	MentionActive            int
	// MentionClosed is the mention Escape turned down, as it read then: the
	// picker is recomputed from the field every pass, so a dismissal has to
	// outlive the pass that made it. Leaving the mention forgets it.
	MentionClosed string
	// MentionCaretAt is where taking a mention leaves the caret, which the
	// next pass to build the field puts there: a caret can only be moved
	// while that pass runs, and the accept happens outside it.
	MentionCaretAt      int
	MentionCaretPending bool
	// copied tracks which replies show their transient "Copied" feedback.
	// The click runs on the UI thread; the revert runs on a timer
	// goroutine, so access is guarded.
	copyMu sync.Mutex
	copied map[uint64]bool
}
type ConversationHost struct {
	Notices []string
	// Auth is fx's sign-in state. A signed-out session says so in place,
	// with the one way out, rather than only failing when a prompt runs.
	Auth                                  AuthNotice
	SignIn                                func()
	SwitchWorkspace                       func()
	Project, Model, Problem               string
	Messages                              []ChatMessage
	Ready, Busy, Stopping, ConfigChanging bool
	// CanSteer says fx can take a message into the running turn. While
	// Busy, the composer stays open either way: with CanSteer the message
	// steers, without it the app queues it for the turn's end.
	CanSteer bool
	// Queued is how many messages are already waiting for the turn to end.
	Queued int
	// Preparing says the app is reading the files this message names; the
	// draft stays in the composer until that finishes.
	Preparing bool
	// Configs and Modes are the session's live configuration, straight from
	// fx. Modes is the richer view of the permission mode; Configs is
	// rendered verbatim, so "model" is not a special case (§4.4.1).
	Configs []ConfigOption
	Modes   []ModeOption
	Mode    string
	// CanAttach says fx's promptCapabilities allow images. When it is false
	// the composer offers no way to attach one: advertising an affordance
	// fx would reject is worse than not having it.
	CanAttach bool
	// AttachFiles takes image paths dropped on the composer. AttachClipboard
	// reads an image off the clipboard; both report failure through
	// state.AttachError. PickImage opens the platform's image chooser, so
	// the composer has a visible button for what a dropped file would do.
	AttachFiles      func(paths []string)
	AttachClipboard  func()
	PickImage        func()
	RemoveAttachment func(id uint64)
	// MentionFiles are the project's files the @ picker offers, as the app
	// read them; MentionLoading and MentionProblem say whether they are on
	// their way or why they are not. OpenMentions asks the app for them when
	// the picker opens, so a mention is only ever offered from files that are
	// really in the project.
	//
	// CanMention is fx's promptCapabilities.embeddedContext. Without it a
	// message that names a file stays the words it is: offering a picker for
	// content fx would not take is the one thing the composer must not do.
	MentionFiles   []MentionFile
	MentionLoading bool
	MentionProblem string
	CanMention     bool
	OpenMentions   func()
	// SetConfig changes one option by its fx config id. Everything routes
	// through here, mode included.
	SetConfig func(configID, value string)
	// Effort/Fast/Ultra are model-dependent settings accepted by fx through
	// set_config_option without appearing in session/new. AdvancedProblem is
	// the last rejection (for example, a model without reasoning effort).
	Effort, AdvancedProblem string
	Fast, Ultra             bool
	SetAdvanced             func(effort string, fast, ultra bool)
	Permission              *ChatPermission
	Send                    func(text string, attachments []Attachment) bool
	// DraftEdited counts one edit of the composer's draft — typing, paste,
	// undo/redo, a mention insertion — so the app can tell the draft a
	// submission was taken from from one the user has changed since (§B5).
	DraftEdited      func()
	Stop             func()
	ChoosePermission func(id, option string)
	// Changed repaints the window; the copy revert fires from a timer
	// goroutine, which may call it because Invalidate is goroutine-safe.
	Changed func()
}

// setCopied records the transient state and schedules the revert. It
// returns whether the state changed, so the caller can skip redundant
// repaints.
func (s *ConversationState) setCopied(id uint64, on bool, changed func()) {
	s.copyMu.Lock()
	if s.copied == nil {
		s.copied = make(map[uint64]bool)
	}
	if s.copied[id] == on {
		s.copyMu.Unlock()
		return
	}
	s.copied[id] = on
	s.copyMu.Unlock()
	if changed != nil {
		changed()
	}
}

func (s *ConversationState) isCopied(id uint64) bool {
	s.copyMu.Lock()
	defer s.copyMu.Unlock()
	return s.copied[id]
}

// Conversation uses a centered reading column, restrained user bubbles and an
// anchored composer. Native lists keep long conversations and user scrolling stable.
func Conversation(c *ui.Context, state *ConversationState, h ConversationHost) {
	p := palette(c.Theme())
	width, _ := c.Size()
	contentWidth := float32(740)
	state.List.FollowEnd = true
	state.List.Key = func(i int) any {
		if i < len(h.Messages) {
			return h.Messages[i].ID
		}
		return i
	}
	if state.Expanded == nil {
		state.Expanded = make(map[uint64]bool)
	}
	state.Markdown.Theme = &markdown.Theme{Font: MonoFont, CodeFont: MonoFont, FontSize: 14, LineHeight: 1.6, CodeBlockRadius: 6, CodeBlockBorder: p.Hairline, CodeBlockBackground: p.Surface, CodeBackground: p.SurfaceHi, Rule: p.Hairline}
	state.Markdown.Reveal = &markdown.Reveal{Unit: markdown.AtOnce, Fade: -time.Millisecond}
	if (state.ConfigListOpen || state.ConfigOpen != "" || state.AdvancedOpen) && c.Shortcut(0, ui.KeyEscape) {
		closeConfig(state)
		state.AdvancedOpen = false
		state.Input.Focus(c)
	}
	send := func(text string) bool {
		text = strings.TrimSpace(text)
		if (text == "" && len(state.Attachments) == 0) || !h.Ready || h.ConfigChanging || h.Send == nil {
			return false
		}
		// A running turn takes text alone: steering is text-only by contract,
		// and a queued message carries only what was written with it. Rather
		// than send the words and quietly drop the pictures, the composer
		// holds both (the button says so; Tooltip on the arrow).
		if h.Busy && len(state.Attachments) > 0 {
			return false
		}
		if h.Send(text, state.Attachments) {
			state.Draft = ""
			state.Attachments = nil
			state.AttachError = ""
			state.List.ScrollToEnd()
			state.Input.Focus(c)
			return true
		}
		return false
	}
	ui.Column(c).Fill().MinWidth(0).Children(func() {
		// The approval card is pinned above the transcript rather than
		// floated over the composer: a permission request is the one thing
		// that must never scroll out of view, and the transcript below it
		// keeps its own scroll position while the user decides (§4.2).
		if permission := h.Permission; permission != nil {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(16, 24, 0, 24).Shrink(0).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(contentWidth).Children(func() {
					permissionCard(c, h, p, permission)
				})
			})
		}
		if len(h.Messages) == 0 {
			ui.Column(c).Grow(1).FillWidth().MinHeight(0).Center().Padding(24).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(contentWidth).AlignItems(ui.Center).Gap(11).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						signalDot(c, p.Accent, 6)
						mono(ui.Text(c, "AGENT WORKSPACE")).FontSize(9).FontWeight(600).LetterSpacing(1.2).TextColor(p.InkMuted)
					})
					// The empty state is conversation content, not window
					// chrome: it stays on the workhorse face so this prompt
					// and the first message read in one voice. Geist is for
					// top-level headers, modal titles and navigation.
					ui.Text(c, "What are we working on?").Font(MonoFont).FontSize(27).FontWeight(500).LetterSpacing(-.6).TextColor(p.InkStrong).TextAlign(ui.Center)
					// The composer below is the affordance: a lede sentence
					// explaining "ask a question" would only restate it.
					if h.Project != "" {
						ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(2, 8).Radius(3).
							Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
							mono(ui.Text(c, h.Project)).FontSize(10).TextColor(p.InkMuted).SingleLine()
						})
					}
				})
			})
		} else {
			ui.List(c, &state.List, len(h.Messages), func(i int) {
				m := h.Messages[i]
				ui.Column(c).FillWidth().AlignItems(ui.Center).Children(func() {
					ui.Column(c).FillWidth().MaxWidth(contentWidth).Gap(10).Children(func() { conversationMessage(c, state, m, p, h.Changed) })
				})
			}).Grow(1).MinHeight(0).FillWidth().Padding(30, 24, 20, 24).Gap(28)
		}
		// Between submit and the first streamed token there is nothing in
		// the transcript to show for the turn. Say so where the reply will
		// land instead of leaving silence; it disappears once a message or
		// tool call starts moving.
		if thinkingPending(h) {
			// The thinking row stands in for the reply that is coming: it
			// sits in the reading column, on the same left edge as every
			// other line of the transcript, and carries the same author
			// mark the message it precedes will carry.
			ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(6, 24, 6, 24).Shrink(0).Children(func() {
				ui.Column(c).FillWidth().MaxWidth(contentWidth).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
						signalDot(c, p.Accent, 6)
						strongText(c, "fx", p.InkStrong)
						ui.Box(c).Width(3).Shrink(0)
						ui.Text(c, "Thinking…").FontSize(12).TextColor(p.InkMuted)
					})
				})
			})
		}
		ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(8, 24, 8, 24).Gap(8).Shrink(0).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(contentWidth).Gap(8).Children(func() {
				if len(h.Notices) > 0 {
					ui.Button(c, fmt.Sprintf("Session notices (%d)", len(h.Notices))).FontSize(11).TextColor(p.InkMuted).BorderWidth(0).Background(p.Window).Padding(4, 0).OnClick(func() { state.NoticesOpen = !state.NoticesOpen })
					if state.NoticesOpen {
						ui.Scroll(c).FillWidth().MaxHeight(100).Children(func() {
							ui.Column(c).FillWidth().Gap(8).Padding(10, 12).Background(p.Surface).Children(func() {
								for _, notice := range h.Notices {
									ui.Text(c, notice).FontSize(11).TextColor(p.InkMuted).Selectable()
								}
							})
						})
					}
				}
				if h.Auth.NeedsSignIn() {
					ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
						AuthNoticeCard(c, h.Auth, h.SignIn)
					})
				}
				if h.Problem != "" {
					ui.Column(c).FillWidth().Padding(10, 14).Gap(4).Radius(10).Background(p.Surface).Children(func() {
						ui.Text(c, "fx needs attention").FontSize(12).FontWeight(500).TextColor(p.Danger)
						ui.Text(c, h.Problem).FontSize(12).TextColor(p.InkMuted).Selectable()
					})
				}
				if state.AdvancedOpen {
					advancedSettings(c, state, h, p)
				} else if state.ConfigListOpen {
					configOverview(c, state, h, p)
				} else if state.ConfigOpen != "" {
					configDropdown(c, state, h, p)
				}
				// The composer is the drop target: a file dropped on the
				// message being written belongs to that message. It only
				// takes files when fx can take images — a drop that can
				// never be sent must not look like it landed.
				composer := ui.Column(c).FillWidth().Padding(10, 12, 8, 12).Gap(6).Radius(8).Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline)
				if h.CanAttach {
					if paths := composer.DroppedFiles(); len(paths) > 0 && h.AttachFiles != nil {
						h.AttachFiles(paths)
					}
					if composer.FileDragOver() {
						composer.BorderColor(p.Accent)
					}
				}
				composer.Children(func() {
					if h.CanAttach {
						attachmentsRow(c, state, h, p)
					}
					var field ui.Element
					ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(8).Children(func() {
						mono(ui.Text(c, "❯")).FontSize(13).TextColor(p.Accent).Padding(4, 0, 0, 2).Shrink(0)
						// The prompt and the options under it are one strip:
						// they share a size so the composer reads as one
						// block instead of a headline over a caption.
						field = ui.TextAreaBase(c, &state.Draft).Label("Message fx").Placeholder("Ask fx anything about this project…").Lines(1, 5).MinHeight(32).Grow(1).MinWidth(0).
							Bind(&state.Input).AutoFocus().Background(p.Surface).BorderWidth(0).FontSize(configLabelSize).Padding(4, 2).
							OnTextKey(func(k ui.TextKey) bool {
								if mentionKey(state, h, k) {
									return true
								}
								if k.Key == ui.KeyEnter && k.Mods == 0 {
									dismissMention(state)
									if send(k.Text()) {
										k.SetText("")
									}
									return true
								}
								return false
							}).
							// The control reports the edits it applies — typed,
							// pasted, undone — between frames. The draft's
							// identity follows those events, not the draws
							// (§B5, N2).
							OnChange(func() {
								if h.DraftEdited != nil {
									h.DraftEdited()
								}
							})
						// What a message can carry is shown by the controls
						// beside the field, not explained in a sentence
						// underneath it. Both buttons are absent when fx
						// cannot take that kind of content.
						if h.CanMention {
							ui.Button(c, "@").Label("Mention a file").Tooltip("Mention a file (@)").
								Font(MonoFont).FontSize(13).Padding(4, 7).Shrink(0).Radius(controlRadius).
								BorderWidth(0).Background(p.Surface).TextColor(p.InkMuted).
								OnClick(func() { startMention(state, h) })
						}
						if h.CanAttach && h.PickImage != nil {
							ui.Button(c, "").Label("Attach image").Tooltip("Attach image…").
								Padding(4, 7).Shrink(0).Radius(controlRadius).
								BorderWidth(0).Background(p.Surface).
								OnClick(h.PickImage).
								Children(func() { ui.Icon(c, IconImage).FontSize(14).TextColor(p.InkMuted) })
						}
					})
					// Taking a mention leaves the caret to be put back: the
					// pass that builds the field is the only one that can move
					// it, and the accept happens outside that pass.
					if state.MentionCaretPending {
						field.SetTextSelection(state.MentionCaretAt, state.MentionCaretAt)
						state.MentionCaretPending = false
					}
					// The mention is read from the field just built, while its
					// caret can be read at all.
					followMention(state, h, field)
					mentionPicker(c, state, h, p)
					if state.AttachError != "" {
						ui.Text(c, state.AttachError).FontSize(11).TextColor(p.Warn).Wrap()
					}
					// What fits on the row is measured from the widths of the
					// things that go on it — the workspace switch, each of fx's
					// options, the status and the controls — against the row's
					// own box from the frame before. Nothing on it is squeezed
					// to fit: an item that does not fit is left out, because a
					// segmented control squeezed by a few points drew "Cod"
					// for "Code" (§4.4).
					row := ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(composerRowGap)
					rowWidth := row.Bounds().W
					row.Children(func() {
						fit := fitComposerRow(c, h, state, rowWidth, width)
						configStrip(c, state, h, p, fit.Options)
						ui.Box(c).Grow(1)
						if fit.Status {
							// Shrink(0): the status is shown whole or left out
							// whole. Squeezed by a few pixels it ellipsised to
							// "Rea…", which reads as a bug, not a status.
							ui.Text(c, composerStatus(h, state)).FontSize(composerStatusSize).TextColor(p.InkMuted).SingleLine().Shrink(0)
						}
						// Stop belongs to the running turn; the send arrow
						// stays beside it because the composer never locks
						// — Enter steers or queues instead of doing nothing.
						if h.Busy {
							// The stop square is drawn, not typed: a text
							// glyph sits on the baseline and never lines up
							// with the arrow beside it. Same 16pt icon in a
							// 16pt-padded circle, so the two buttons are
							// the same size and centre the same way.
							ui.Button(c, "").Label("Stop response").Tooltip("Stop response (⌘.)").Padding(8).Radius(16).BorderWidth(0).Background(p.Ink).TextColor(p.OnInk).Disabled(h.Stopping).OnClick(h.Stop).
								Children(func() { ui.Icon(c, IconStop).FontSize(16).TextColor(p.OnInk) })
						}
						// The arrow is drawn, not typed: a text glyph
						// shifts baseline across platforms. The button
						// wraps its content — a 16pt icon plus 8pt on
						// each side makes the 32pt circle — so the
						// glyph is centered by construction. Icons do
						// not stretch or center reliably inside a
						// fixed-size button: they follow text metrics.
						ui.Button(c, "").Label("Send message").Tooltip(sendTooltip(h, state)).Padding(8).Radius(16).BorderWidth(0).Background(p.Ink).TextColor(p.OnInk).
							Disabled(!h.Ready || h.ConfigChanging || h.Preparing || h.Busy && len(state.Attachments) > 0 ||
								strings.TrimSpace(state.Draft) == "" && len(state.Attachments) == 0).OnClick(func() { dismissMention(state); send(state.Draft) }).
							Children(func() { ui.Icon(c, IconArrowUp).FontSize(16).TextColor(p.OnInk) })
					})
				})
				// No caption under the composer: the field, its two
				// affordances and the send arrow say what a message is.
			})
		})
	})
}

// The composer row's gutter, and the size of the two round controls that end
// it: the arrow button wraps a 16pt icon with 8pt on each side, and the stop
// button asks for the same box. Named because the row decides from them
// whether the status fits.
const (
	composerRowGap     = 8
	composerControl    = 32
	composerStatusSize = 11
)

// composerStatusMinPane is the pane width the composer treated as room for a
// status while it had nothing to measure with. It decides the first frame only.
const composerStatusMinPane = 760

// composerFit is what the composer's row shows beside its controls: whether
// the status is drawn, and how many of fx's options are.
type composerFit struct {
	Status  bool
	Options int
}

// fitComposerRow decides the composer's row from the room it has. What the row
// never gives up is the workspace switch, followed by the status: a status that came and went
// as the window was dragged narrower would flicker, so whether one shows is
// decided against the row's floor rather than against whatever fx's options
// take this frame (TestConversationStatusIsShownWholeOrNotAtAll pins this).
// fx's options then fill what is left, whole and in fx's order, so the strip
// is a prefix of what fx published and an option that does not fit whole is
// left out rather than shown squeezed. Nothing here is ever shrunk to fit: a
// segmented control squeezed by a few points drew "Cod" for "Code".
//
// room is the row's own box from the frame before; 0 means nothing has been
// measured yet, and the pane stands in for that one frame.
func fitComposerRow(c *ui.Context, h ConversationHost, state *ConversationState, room, pane float32) composerFit {
	labels := configStripLabels(h.Configs)
	status := composerStatus(h, state)
	fit := composerFit{Options: len(labels)}
	if room <= 0 {
		fit.Status = status != "" && pane >= composerStatusMinPane
		return fit
	}
	entry := float32(0)
	if h.SwitchWorkspace != nil {
		entry = workspaceButtonWidth
	}
	statusWidth := float32(0)
	if status != "" {
		statusWidth, _ = c.MeasureText(0, ui.Span{Text: status, Size: composerStatusSize})
	}
	// base is the room the row keeps beside the strip: one gutter between each
	// of its children, plus the controls that end it. The status leaving takes
	// its gutter with it.
	base := func(withStatus bool) float32 {
		gaps := 2
		if withStatus {
			gaps = 3
		}
		b := float32(gaps)*composerRowGap + composerControl
		if h.Busy {
			b += composerControl + composerRowGap
		}
		return b
	}
	fit.Status = statusWidth > 0 && entry+statusWidth+base(true) <= room
	// What the row has spent so far: the status is part of it, so the options
	// below only ever take room the status did not need (§4.8.2).
	used := entry
	if fit.Status {
		used += statusWidth
	}
	avail := room - base(fit.Status)
	for i, label := range labels {
		w := configButtonWidth(c, label)
		// The gutter between items belongs to the item that follows one.
		if used > 0 {
			w += configStripGap
		}
		if used+w > avail {
			fit.Options = i
			break
		}
		used += w
	}
	return fit
}

// composerStatus says what the session is doing, beside the control that acts
// on it: the one thing the composer cannot otherwise show. A disabled arrow
// with no explanation reads as a broken app (§4.4).
func composerStatus(h ConversationHost, state *ConversationState) string {
	switch {
	case h.Stopping:
		return "Stopping…"
	case h.Preparing:
		return "Preparing… · reading mentioned files"
	case h.Permission != nil:
		return "Waiting for approval"
	case h.Busy && h.Queued == 1:
		return "Working… · next message queued"
	case h.Busy && h.Queued > 1:
		return fmt.Sprintf("Working… · %d messages queued", h.Queued)
	case h.Busy && len(state.Attachments) > 0:
		return "Working… · images wait for the next turn"
	case h.Busy && h.CanSteer:
		return "Working… · ↵ steers this turn"
	case h.Busy:
		return "Working… · ↵ queues the next message"
	case h.ConfigChanging:
		return "Changing setting…"
	case h.Problem != "" && !h.Ready:
		return "Connection unavailable"
	case h.Ready:
		return "Ready"
	}
	return "Connecting to fx…"
}

// startMention writes the @ the picker follows. The picker is derived from
// the text under the caret, so a button opens it by producing the same
// character a user would type, rather than setting a second, competing flag.
func startMention(state *ConversationState, h ConversationHost) {
	state.MentionClosed = ""
	if state.Draft != "" && !strings.HasSuffix(state.Draft, " ") {
		state.Draft += " "
	}
	state.Draft += "@"
	if h.DraftEdited != nil {
		h.DraftEdited()
	}
	state.MentionCaretAt = len([]rune(state.Draft))
	state.MentionCaretPending = true
	state.Input.Focus()
}

// sendTooltip explains why the arrow may be disabled: a session mid-turn
// cannot take the attached images, and that is worth saying before the click.
func sendTooltip(h ConversationHost, state *ConversationState) string {
	if h.Busy && len(state.Attachments) > 0 {
		return "Images are sent with the next message; wait for this turn to end"
	}
	return "Send message (Enter)"
}

// maxDiffRenderLines bounds how many diff lines one tool row draws. A
// 10k-line diff is data the app can hold but not a view the user can read;
// past the cap the row says how much it left out.
const maxDiffRenderLines = 400

// maxToolOutputLines bounds the raw payload an expanded tool call shows. The
// payload can be a whole session's JSON; the transcript keeps the part a
// person can scan and says how much it left out.
const maxToolOutputLines = 120

// toolStateLabel is the state a tool call ended in, for assistive
// technology: the row's own dot and duration carry it visually.
func toolStateLabel(status string) string {
	switch status {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "pending":
		return "queued"
	default:
		return "working"
	}
}

// toolDuration is how long a tool call took. The app times the call as it
// arrives, so the transcript can say what the call cost.
func toolDuration(m ChatMessage) (time.Duration, bool) {
	if m.Started.IsZero() || m.Ended.IsZero() || m.Ended.Before(m.Started) {
		return 0, false
	}
	return m.Ended.Sub(m.Started), true
}

// formatDuration reads as a measurement, the way the design's audit log
// does: milliseconds for the quick calls, seconds for the slow ones.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return itoa(int(d.Milliseconds())) + "ms"
	case d < time.Minute:
		return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + "s"
	default:
		return d.Round(time.Second).String()
	}
}

// toolPayload caps a tool call's output and reports what it left out.
func toolPayload(text string, limit int) (string, int) {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= limit {
		return text, 0
	}
	return strings.Join(lines[:limit], "\n"), len(lines) - limit
}

// toolOutput is the payload behind an expanded tool call: one framed
// component under the bar that opens it, so the call reads as one block
// instead of a log dump spilled across the page. A JSON payload is data with
// structure and takes the data grid; anything else stays verbatim.
func toolOutput(c *ui.Context, state *ConversationState, m ChatMessage, p Palette) {
	if m.Shell != nil {
		shellOutput(c, m, p)
		return
	}
	text := m.Text
	if text == "" {
		text = "No additional output."
	}
	ui.Column(c).FillWidth().Radius(4).Background(p.Sunken).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		if lines, ok := payloadLines(state, m); ok {
			lines, omitted := capJSONLines(lines, maxToolOutputLines)
			ui.Scroll(c).FillWidth().MaxHeight(260).Children(func() {
				jsonText(c, lines, p).FillWidth().Padding(10, 10)
			})
			if omitted > 0 {
				payloadFooter(c, omitted, p)
			}
			return
		}
		textPayload(c, text, p)
	})
}

// textPayload is a tool payload that stays verbatim: a framed block capped at
// the transcript's line budget, with a hairline foot saying what it left out.
func textPayload(c *ui.Context, text string, p Palette) {
	payload, omitted := toolPayload(text, maxToolOutputLines)
	ui.Scroll(c).FillWidth().MaxHeight(260).Children(func() {
		ui.Text(c, payload).Font(MonoFont).FontSize(jsonSize).TextColor(p.InkMuted).Selectable().FillWidth().Padding(10, 10)
	})
	if omitted > 0 {
		payloadFooter(c, omitted, p)
	}
}

// payloadFooter reports a payload's cut, below a hairline so the note does not
// run into the last line it kept.
func payloadFooter(c *ui.Context, omitted int, p Palette) {
	hairlineH(c, p)
	mono(ui.Text(c, "… "+itoa(omitted)+" more lines")).FontSize(10).TextColor(p.InkFaint).Padding(6, 10, 8, 10)
}

// shellOutput is the expanded payload of a shell call. fx's ACP result is a
// 200-byte preview of its own envelope — session, capture mode, handles — cut
// before the command's output. The envelope is bookkeeping, not a payload, so
// the block shows the call itself: the command that ran, and the output when
// the envelope was complete enough to carry one. Only a result that lost
// something gets a note, under a hairline, so a routine call stays quiet.
func shellOutput(c *ui.Context, m ChatMessage, p Palette) {
	ui.Column(c).FillWidth().Radius(4).Background(p.Sunken).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		if m.Command != "" {
			ui.Scroll(c).FillWidth().MaxHeight(200).Children(func() {
				ui.RichText(c,
					ui.Span{Text: "$ ", Color: p.InkMuted},
					ui.Span{Text: m.Command, Color: p.Ink},
				).Font(MonoFont).FontSize(jsonSize).Selectable().FillWidth().Padding(10, 10)
			})
		}
		if m.Text != "" {
			if m.Command != "" {
				hairlineH(c, p)
			}
			textPayload(c, m.Text, p)
		}
		if m.Command == "" && m.Text == "" {
			ui.Text(c, "No additional output.").Font(MonoFont).FontSize(jsonSize).TextColor(p.InkMuted).
				FillWidth().Padding(10, 10)
		}
		if notes := shellNotes(m.Shell, p); len(notes) > 0 {
			hairlineH(c, p)
			ui.Row(c).AlignItems(ui.Center).Gap(6).Padding(6, 10, 8, 10).Children(func() {
				for i, note := range notes {
					if i > 0 {
						mono(ui.Text(c, "·")).FontSize(10).TextColor(p.InkMuted)
					}
					mono(ui.Text(c, note.text)).FontSize(10).TextColor(note.col)
				}
			})
		}
	})
}

type shellNote struct {
	text string
	col  ui.Color
}

// shellNotes is the shell block's footer vocabulary: only facts that change
// what the result means. fx's capture mode, persistence and handles are
// plumbing and stay out of the transcript.
func shellNotes(r *ShellResult, p Palette) []shellNote {
	if r == nil {
		return nil
	}
	var notes []shellNote
	if r.Truncated {
		notes = append(notes, shellNote{"output truncated", p.Warn})
	}
	if r.Incomplete {
		notes = append(notes, shellNote{"output incomplete", p.Warn})
	}
	if r.Indeterminate {
		notes = append(notes, shellNote{"termination unknown", p.Danger})
	}
	if r.Signal != "" {
		notes = append(notes, shellNote{"signal " + r.Signal, p.Danger})
	}
	return notes
}

// diffBlock renders one changed file as classified lines. It shows by
// default — a change is what the user came to see — and the header folds it
// away (§4.2).
func diffBlock(c *ui.Context, state *ConversationState, m ChatMessage, p Palette) {
	d := m.Diff
	added, removed := 0, 0
	for _, line := range d.Lines {
		switch line.Kind {
		case DiffAdd:
			added++
		case DiffDel:
			removed++
		}
	}
	folded := state.DiffFolded[m.ID]
	ui.Column(c).FillWidth().Radius(6).Background(p.Surface).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.ButtonBase(c).Label(fmt.Sprintf("%s +%d −%d", d.Path, added, removed)).FillWidth().Padding(6, panelPad).
			OnClick(func() {
				if state.DiffFolded == nil {
					state.DiffFolded = map[uint64]bool{}
				}
				state.DiffFolded[m.ID] = !folded
			}).Children(func() {
			ui.Icon(c, IconChevron).FontSize(11).TextColor(p.InkMuted)
			ui.Text(c, d.Path).Font(MonoFont).FontSize(11).TextColor(p.InkStrong).SingleLine()
			ui.Box(c).Grow(1)
			// The counts are readouts and the hint is an action: squashed
			// together they read as one token ("+1-1Hide"), so they keep the
			// row's own gutter between them. The minus is the app's own
			// (U+2212, as the rail and the review drawer draw it).
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "+"+itoa(added)).FontSize(10).TextColor(p.OK).Shrink(0)
				ui.Text(c, "−"+itoa(removed)).FontSize(10).TextColor(p.Danger).Shrink(0)
				ui.Text(c, diffFoldHint(folded)).FontSize(10).TextColor(p.InkMuted).Shrink(0)
			})
		})
		if folded {
			return
		}
		ui.Scroll(c).FillWidth().MaxHeight(320).Children(func() {
			ui.Column(c).FillWidth().Padding(0, 0, 6, 0).Children(func() {
				diffLines(c, d.Lines, p, maxDiffRenderLines)
			})
		})
	})
}

// diffLines draws classified diff lines, at most max of them (max <= 0 means
// no cap), and says how many it left out. The timeline and the review drawer
// both show diffs, so the rules live in one place: one mark column, a
// monospace body, and an empty line that still gets a body so the diff keeps
// its spacing instead of collapsing.
func diffLines(c *ui.Context, lines []DiffLine, p Palette, max int) {
	shown := 0
	for _, line := range lines {
		if max > 0 && shown >= max {
			break
		}
		shown++
		mark, col := " ", p.InkMuted
		switch line.Kind {
		case DiffAdd:
			mark, col = "+", p.OK
		case DiffDel:
			mark, col = "-", p.Danger
		}
		row := ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(6).Padding(1, panelPad)
		switch line.Kind {
		case DiffAdd:
			row.Background(ui.RGBA(p.OK.R, p.OK.G, p.OK.B, 0.09))
		case DiffDel:
			row.Background(ui.RGBA(p.Danger.R, p.Danger.G, p.Danger.B, 0.10))
		}
		row.Children(func() {
			ui.Text(c, mark).Font(MonoFont).FontSize(11).TextColor(col).Shrink(0)
			if line.Text == "" {
				ui.Text(c, " ").Font(MonoFont).FontSize(11).TextColor(col).FillWidth()
				return
			}
			ui.Text(c, line.Text).Font(MonoFont).FontSize(11).TextColor(col).FillWidth()
		})
	}
	if omitted := len(lines) - shown; omitted > 0 {
		ui.Text(c, fmt.Sprintf("… %d more lines", omitted)).FontSize(10).TextColor(p.InkMuted).Padding(4, panelPad)
	}
}

func diffFoldHint(folded bool) string {
	if folded {
		return "Show"
	}
	return "Hide"
}

// permissionCard is the sticky approval block: what fx wants to do, the
// affected surface, and the scope buttons (once / session / deny).
func permissionCard(c *ui.Context, h ConversationHost, p Palette, permission *ChatPermission) {
	ui.Column(c).FillWidth().Padding(12, 14).Gap(9).Radius(6).Background(p.Surface).BorderWidth(1).BorderColor(p.Warn).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(7).Children(func() {
			signalDot(c, p.Warn, 6)
			mono(ui.Text(c, "Approval required")).FontSize(10).FontWeight(600).LetterSpacing(0.4).TextColor(p.Warn)
		})
		ui.Text(c, permission.Title).FontSize(13).TextColor(p.Ink)
		if permission.Detail != "" {
			ui.Scroll(c).FillWidth().MaxHeight(100).Children(func() {
				// The detail is fx's raw input for the call: usually JSON.
				// It reads as the same data grid the transcript uses, so an
				// approval and the tool row it belongs to speak one language.
				if lines, ok := formatJSON(permission.Detail); ok {
					lines, _ = capJSONLines(lines, 40)
					jsonText(c, lines, p).FillWidth()
					return
				}
				ui.Text(c, permission.Detail).FontSize(11).TextColor(p.InkMuted).Selectable()
			})
		}
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			for _, option := range permission.Options {
				option := option
				ui.Button(c, option.Name).FontSize(11).Padding(7, 10).Disabled(h.Stopping).OnClick(func() {
					if h.ChoosePermission != nil {
						h.ChoosePermission(permission.ID, option.ID)
					}
				})
			}
		})
	})
}

// thinkingPending reports whether the "Thinking…" row belongs in the flow:
// the turn is running, and the latest transcript entry is not already an
// in-flight reply or tool call that shows its own progress.
func thinkingPending(h ConversationHost) bool {
	if !h.Busy {
		return false
	}
	if n := len(h.Messages); n > 0 {
		last := h.Messages[n-1]
		if (last.Role == "assistant" || last.Role == "tool") && !last.Done {
			return false
		}
	}
	return true
}

func conversationMessage(c *ui.Context, state *ConversationState, m ChatMessage, p Palette, changed func()) {
	switch m.Role {
	case "user":
		ui.Column(c).FillWidth().AlignItems(ui.End).Gap(6).Children(func() {
			// What was sent with the words, so a message written to go with a
			// screenshot still reads as that message weeks later.
			for _, name := range m.Images {
				ui.Row(c).MaxWidth(600).Padding(7, 12).Radius(14).Background(p.SurfaceHi).
					AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Icon(c, IconImage).FontSize(12).TextColor(p.InkMuted)
					ui.Text(c, name).FontSize(12).TextColor(p.InkMuted).SingleLine()
				})
			}
			ui.Text(c, m.Text).Selectable().FontSize(14).TextColor(p.Ink).MaxWidth(600).Padding(10, 14).Radius(8).Background(p.SurfaceHi)
		})
	case "tool":
		expanded := state.Expanded[m.ID]
		label := m.Title
		if label == "" {
			label = "Tool activity"
		}
		// The right-hand slot is the state and the scale, not a word that
		// contradicts the title: a row that reads "Running … Completed"
		// tells the reader nothing. A dot says how the call ended, and the
		// duration says what it cost — the design's audit log.
		stateCol := p.InkMuted
		stateText := ""
		switch m.Status {
		case "completed":
			stateCol = p.OK
		case "failed":
			stateCol = p.Danger
		case "pending":
			stateCol = p.InkFaint
			stateText = "queued"
		default:
			// A call still in flight is the one state the reader has to
			// notice: it gets the attention tone, not the muted one.
			stateCol = p.Warn
			stateText = "working"
		}
		if m.Status == "completed" || m.Status == "failed" {
			if d, ok := toolDuration(m); ok {
				if m.Status == "failed" {
					// A failure has to say so in words: a red dot alone
					// leaves the reader guessing what the red meant.
					stateText = "failed · " + formatDuration(d)
				} else {
					stateText = formatDuration(d)
				}
			}
		}
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.ButtonBase(c).Label(fmt.Sprintf("%s — %s", label, toolStateLabel(m.Status))).FillWidth().Padding(7, 10).Gap(8).Radius(4).
				Background(p.Sunken).BorderWidth(1).BorderColor(p.Hairline).OnClick(func() { state.Expanded[m.ID] = !expanded }).Children(func() {
				// The chevron is the disclosure: it has to say which way the
				// payload is folded.
				chevron := ui.Icon(c, IconChevron).FontSize(11).TextColor(p.InkFaint)
				if expanded {
					chevron.Rotate(90)
				}
				mono(ui.Text(c, "❯")).FontSize(10).TextColor(p.Accent)
				ui.Text(c, label).FontSize(12).TextColor(p.Ink).SingleLine()
				ui.Box(c).Grow(1)
				signalDot(c, stateCol, 5)
				if stateText != "" {
					mono(ui.Text(c, stateText)).FontSize(9).TextColor(stateCol)
				}
				if m.Diff != nil {
					add, del := 0, 0
					for _, line := range m.Diff.Lines {
						switch line.Kind {
						case DiffAdd:
							add++
						case DiffDel:
							del++
						}
					}
					mono(ui.Text(c, "+"+itoa(add)+" −"+itoa(del))).FontSize(9).TextColor(p.InkFaint)
				}
			})
			// The diff is the exception to "tool output starts collapsed":
			// the change is what the user came to review, so it renders by
			// default and only the raw output waits behind the toggle.
			if m.Diff != nil {
				diffBlock(c, state, m, p)
			}
			if expanded {
				toolOutput(c, state, m, p)
			}
		})
	case "notice":
		ui.Text(c, m.Text).FontSize(12).TextColor(p.InkMuted).Selectable()
	default:
		ui.Row(c).AlignItems(ui.Center).Gap(7).Children(func() {
			signalDot(c, p.Accent, 6)
			strongText(c, "fx", p.InkStrong)
		})
		if m.Done {
			markdown.View(c, m.Text, &state.Markdown).FillWidth()
		} else {
			markdown.Stream(c, m.Text, &state.Markdown).FillWidth()
		}
		if m.Done && m.Text != "" {
			services := c.Services()
			copied := state.isCopied(m.ID)
			label := "Copy response"
			if copied {
				label = "Copied"
			}
			ui.Row(c).FillWidth().Children(func() {
				ui.Button(c, label).Label(label).FontSize(10).BorderWidth(0).Background(p.Window).TextColor(p.InkMuted).Padding(4, 0).OnClick(func() {
					services.WriteClipboard(m.Text)
					state.setCopied(m.ID, true, changed)
					time.AfterFunc(copyFeedbackDuration, func() { state.setCopied(m.ID, false, changed) })
				})
			})
		}
	}
}
