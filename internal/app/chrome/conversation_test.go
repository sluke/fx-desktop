package chrome

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestConversationEnterSendAndShiftEnter(t *testing.T) {
	state := ConversationState{}
	var sent []string
	h := ConversationHost{Ready: true, Send: func(s string, _ []Attachment) bool { sent = append(sent, s); return true }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 800, 600)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("Hello")
	ts.Key(ui.Shift, ui.KeyEnter)
	ts.Type("世界")
	if state.Draft != "Hello\n世界" {
		t.Fatalf("Shift+Enter did not insert a line: %q", state.Draft)
	}
	ts.Key(0, ui.KeyEnter)
	if len(sent) != 1 || sent[0] != "Hello\n世界" || state.Draft != "" {
		t.Fatalf("send=%v, draft=%q", sent, state.Draft)
	}
	ts.Frame()
	ts.Key(0, ui.KeyEnter)
	if len(sent) != 1 {
		t.Fatal("empty or repeated frames submitted a turn")
	}
	h.Busy = true
	ts.Frame()
	ts.Type("Keep this draft")
	ts.Key(0, ui.KeyEnter)
	// Busy is not a closed composer: the message is accepted (the app
	// steers or queues it), so the draft clears like any other send.
	if len(sent) != 2 || sent[1] != "Keep this draft" || state.Draft != "" {
		t.Fatalf("busy input must reach the app: sent=%v, draft=%q", sent, state.Draft)
	}
	// The empty draft never sends twice.
	ts.Frame()
	ts.Key(0, ui.KeyEnter)
	if len(sent) != 2 {
		t.Fatal("submitted an empty draft")
	}
	h.Busy = false
	h.ConfigChanging = true
	ts.Frame()
	ts.Type("while reconnecting")
	ts.Key(0, ui.KeyEnter)
	if len(sent) != 2 || state.Draft != "while reconnecting" {
		t.Fatalf("a session with nowhere to put the text must hold it: sent=%v, draft=%q", sent, state.Draft)
	}
}
func TestConversationRendersMarkdownToolsAndApprovals(t *testing.T) {
	state := ConversationState{}
	selected := ""
	h := ConversationHost{Ready: true, Busy: true, Notices: []string{"Diagnostic details stay outside the conversation"}, Model: "provider/model", Messages: []ChatMessage{
		{ID: 1, Role: "user", Text: "Explain this project", Done: true},
		{ID: 2, Role: "assistant", Text: "## Findings\n\nA **native** conversation.\n\n```go\nfunc main() {}\n```", Done: true},
		{ID: 3, Role: "tool", Title: "Read main.go", Status: "completed", Text: "package main", Done: true},
	}, Permission: &ChatPermission{ID: "request-1", Title: "Update a file", Options: []PermissionOption{{ID: "allow_once", Name: "Allow once"}, {ID: "reject_once", Name: "Reject"}}}, ChoosePermission: func(id, option string) { selected = id + ":" + option }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 900)
	mustNotHave(t, ts, "Diagnostic details stay outside the conversation")
	if err := ts.Click("Session notices (1)"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	mustHave(t, ts, "Diagnostic details stay outside the conversation")
	if err := ts.Click("Session notices (1)"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	mustHave(t, ts, "Findings", "native", "Read main.go", "Approval required", "Allow once", "Reject", "Copy response")
	if err := ts.Click("Read main.go — completed"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	mustHave(t, ts, "package main")
	if err := ts.Click("Reject"); err != nil {
		t.Fatal(err)
	}
	if selected != "request-1:reject_once" {
		t.Fatal(selected)
	}
	h.Permission = nil
	h.Busy = false
	h.Messages = h.Messages[:1]
	ts.Frame()
	mustNotHave(t, ts, "Approval required", "Allow once", "Read main.go")
}

// The diff is the one timeline entry that does not start collapsed: the
// change is what the user came to review. Its header folds it away again.
func TestConversationRendersDiffExpandedAndFolds(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Messages: []ChatMessage{{
		ID: 5, Role: "tool", Title: "Edit chat.go", Status: "completed", Done: true,
		Text: "Applied 1 edit",
		Diff: &DiffInfo{Path: "internal/app/chat.go", Lines: []DiffLine{
			{Kind: DiffContext, Text: "func toolContent(raw json.RawMessage) {"},
			{Kind: DiffDel, Text: "return toolContent(raw)"},
			{Kind: DiffAdd, Text: "text, diff := toolContent(raw)"},
		}},
	}}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	mustHave(t, ts, "internal/app/chat.go", "+1", "−1",
		"func toolContent(raw json.RawMessage) {",
		"return toolContent(raw)",
		"text, diff := toolContent(raw)")
	// The raw output still waits behind the tool toggle.
	mustNotHave(t, ts, "Applied 1 edit")

	if err := ts.Click("internal/app/chat.go +1 −1"); err != nil {
		t.Fatalf("fold the diff: %v", err)
	}
	ts.Frame()
	mustNotHave(t, ts, "return toolContent(raw)", "text, diff := toolContent(raw)")
	mustHave(t, ts, "internal/app/chat.go", "+1", "−1")

	// And it opens again.
	if err := ts.Click("internal/app/chat.go +1 −1"); err != nil {
		t.Fatalf("unfold the diff: %v", err)
	}
	ts.Frame()
	mustHave(t, ts, "text, diff := toolContent(raw)")
}

// The diff header's right end holds three separate things: the two counts and
// the fold hint. Drawn without air between them they read as one token —
// "+1-1Hide" — so the header must keep them apart.
func TestDiffHeaderSeparatesWhatItCounts(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Messages: []ChatMessage{{
		ID: 5, Role: "tool", Title: "Edit chat.go", Status: "completed", Done: true,
		Diff: &DiffInfo{Path: "internal/app/chat.go", Lines: []DiffLine{
			{Kind: DiffDel, Text: "return toolContent(raw)"},
			{Kind: DiffAdd, Text: "text, diff := toolContent(raw)"},
		}},
	}}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	plus, ok := ts.Find("+1")
	if !ok {
		t.Fatal("the diff header's added count is missing")
	}
	minus, ok := ts.Find("−1")
	if !ok {
		t.Fatal("the diff header's removed count is missing — is it drawn with an ASCII hyphen?")
	}
	hint, ok := ts.Find("Hide")
	if !ok {
		t.Fatal("the diff header's fold hint is missing")
	}
	if gap := minus.X - (plus.X + plus.W); gap < 3 {
		t.Errorf("the counts are %v apart — \"%s\" reads as one figure", gap, "+1-1")
	}
	if gap := hint.X - (minus.X + minus.W); gap < 3 {
		t.Errorf("the fold hint sits %v after the last count — it reads as part of them", gap)
	}
}

// While a turn runs the composer stays open and says what Enter will do:
// steer the turn when fx can take it, queue the text when it cannot.
func TestConversationComposerKeepsWorkingWhileBusy(t *testing.T) {
	state := ConversationState{Draft: "one more thing"}
	sent := 0
	h := ConversationHost{Ready: true, Busy: true, CanSteer: true, Send: func(string, []Attachment) bool { sent++; return true }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 600)
	mustHave(t, ts, "Working… · ↵ steers this turn", "Stop response", "Send message")
	if err := ts.Click("Send message"); err != nil {
		t.Fatalf("send while busy: %v", err)
	}
	if sent != 1 {
		t.Fatalf("a busy send did not reach the app: %d", sent)
	}

	state.Draft = "one more thing"
	h.CanSteer = false
	ts.Frame()
	mustHave(t, ts, "Working… · ↵ queues the next message", "Stop response", "Send message")

	h.Queued = 1
	ts.Frame()
	mustHave(t, ts, "Working… · next message queued")

	// Every message the composer accepted is in the transcript, so a second
	// one must not read as if the first had vanished.
	h.Queued = 3
	ts.Frame()
	mustHave(t, ts, "Working… · 3 messages queued")
}

// A session that cannot accept text at all (reconnecting, changing an
// option) still holds the draft: refusing to send must not throw the words
// away.
func TestConversationHoldsDraftWhenSessionCannotSend(t *testing.T) {
	state := ConversationState{Draft: "half-typed thought"}
	calls := 0
	h := ConversationHost{Ready: true, ConfigChanging: true, Send: func(string, []Attachment) bool { calls++; return true }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 600)
	ts.Frame()
	mustHave(t, ts, "Changing setting…")
	if err := ts.Click("Send message"); err != nil {
		t.Fatalf("click send: %v", err)
	}
	if calls != 0 || state.Draft != "half-typed thought" {
		t.Fatalf("the draft must survive: calls=%d draft=%q", calls, state.Draft)
	}
}

// An approval is the one thing that must never scroll away: it is pinned
// above the transcript, not floated over the composer.
func TestConversationApprovalIsPinnedAboveTranscript(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Busy: true,
		Messages:   []ChatMessage{{ID: 1, Role: "user", Text: "Please update the file", Done: true}},
		Permission: &ChatPermission{ID: "r1", Title: "Update a file", Options: []PermissionOption{{ID: "allow_once", Name: "Allow once"}}},
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	approval, ok := ts.Find("Approval required")
	if !ok {
		t.Fatal("approval card missing")
	}
	msg, ok := ts.Find("Please update the file")
	if !ok {
		t.Fatal("transcript missing")
	}
	if approval.Y >= msg.Y {
		t.Fatalf("approval must sit above the transcript: approval.Y=%v transcript.Y=%v", approval.Y, msg.Y)
	}
}

// An empty diff line still occupies a row, or the diff loses its spacing and
// reads as if blank lines were deleted.
func TestConversationDiffKeepsBlankLines(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Messages: []ChatMessage{{
		ID: 9, Role: "tool", Title: "Edit a.go", Status: "completed", Done: true,
		Diff: &DiffInfo{Path: "a.go", Lines: []DiffLine{
			{Kind: DiffContext, Text: "one"},
			{Kind: DiffContext, Text: ""},
			{Kind: DiffContext, Text: "three"},
		}},
	}}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	one, okOne := ts.Find("one")
	three, okThree := ts.Find("three")
	if !okOne || !okThree {
		t.Fatalf("diff context lines missing: %v", ts.Texts())
	}
	if three.Y <= one.Y {
		t.Fatalf("lines must keep their order: one.Y=%v three.Y=%v", one.Y, three.Y)
	}
}

func TestConversationComposerStaysVisibleInNarrowWindow(t *testing.T) {
	state := ConversationState{}
	h, sh := testHost()
	h.Conversation = true
	h.Agent.Status = "Running"
	data := ConversationHost{Ready: true, Model: "provider/model", Messages: []ChatMessage{{ID: 1, Role: "assistant", Text: strings.Repeat("A longer reply with text.\n\n", 80), Done: true}}}
	h.Main = func(c *ui.Context) { Conversation(c, &state, data) }
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 720, 480)
	for _, label := range []string{"Message fx", "Send message"} {
		r, ok := ts.Find(label)
		if !ok || r.X < 248 || r.Y < 52 || r.X+r.W > 720.5 || r.Y+r.H > 456.5 {
			t.Errorf("%s clipped: %+v, found=%v", label, r, ok)
		}
	}
	mustNotHaveToken(t, ts, "agent live", "Elapsed", "Running")
	sh.SidebarOpen = false
	ts.Frame()
	mustHave(t, ts, "Message fx")
	ts.SetDark(true)
	ts.Frame()
	mustHave(t, ts, "Message fx")
}

// The mock shows a thinking indicator between submit and the first streamed
// token. It belongs only while nothing in the transcript is moving: a
// streaming reply or a working tool call reports its own progress, and an
// idle composer must not pretend work is happening.
func TestConversationShowsThinkingWhileBusy(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Busy: true, Messages: []ChatMessage{{ID: 1, Role: "user", Text: "Hi", Done: true}}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 800, 600)
	mustHave(t, ts, "Thinking…")

	h.Messages = append(h.Messages, ChatMessage{ID: 2, Role: "assistant", Text: "partial", Done: false})
	ts.Frame()
	mustNotHave(t, ts, "Thinking…")

	h.Messages = []ChatMessage{{ID: 1, Role: "user", Text: "Hi", Done: true}, {ID: 3, Role: "tool", Title: "Read main.go", Status: "pending", Done: false}}
	ts.Frame()
	mustNotHave(t, ts, "Thinking…")

	h.Busy = false
	ts.Frame()
	mustNotHave(t, ts, "Thinking…")
}

// Copy feedback reverts on its own: a button stuck on "Copied" stops
// telling the truth about the last action.
func TestConversationCopyFeedbackResets(t *testing.T) {
	previous := copyFeedbackDuration
	copyFeedbackDuration = 20 * time.Millisecond
	defer func() { copyFeedbackDuration = previous }()

	state := ConversationState{}
	h := ConversationHost{Ready: true, Messages: []ChatMessage{{ID: 7, Role: "assistant", Text: "A reply.", Done: true}}, Changed: func() {}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 800, 600)
	if err := ts.Click("Copy response"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	mustHave(t, ts, "Copied")
	mustNotHave(t, ts, "Copy response")

	time.Sleep(80 * time.Millisecond)
	ts.Frame()
	mustHave(t, ts, "Copy response")
	mustNotHave(t, ts, "Copied")
}

// The send arrow must sit in the middle of its 32pt circle. The icon draws
// inside the button's content box following text metrics, so a structural
// change can silently shove it aside; this asserts the rendered glyph, not
// the layout intent.
func TestConversationSendArrowIsCentered(t *testing.T) {
	state := ConversationState{Draft: "Hello"} // enabled: an empty draft renders at 35% opacity
	h := ConversationHost{Ready: true}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 800, 600)
	rect, ok := ts.Find("Send message")
	if !ok {
		t.Fatal("send button not found")
	}
	img := ts.Image()
	scaleX := float32(img.Bounds().Dx()) / 800
	scaleY := float32(img.Bounds().Dy()) / 600
	cx := (rect.X + rect.W/2) * scaleX
	cy := (rect.Y + rect.H/2) * scaleY
	// Scan the inscribed circle only: the composer surface (#F5F5F5) shows
	// through the rounded corners and would pollute a rectangle scan. The
	// arrow is a white stroke on the ink circle; antialiasing means most
	// stroke pixels are blends, so the cutoff sits at half coverage (140)
	// and catches the stroke symmetrically on both sides.
	radius := 14.0 * float64(scaleX)
	minX, minY, maxX, maxY := -1, -1, -1, -1
	for y := int(cy - float32(radius)); y <= int(cy+float32(radius)); y++ {
		for x := int(cx - float32(radius)); x <= int(cx+float32(radius)); x++ {
			dx, dy := float64(x)-float64(cx), float64(y)-float64(cy)
			if dx*dx+dy*dy > radius*radius {
				continue
			}
			r, g, b, _ := img.At(x, y).RGBA()
			if uint8(r>>8) > 140 && uint8(g>>8) > 140 && uint8(b>>8) > 140 {
				if minX < 0 || x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if minY < 0 || y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if minX < 0 {
		t.Fatalf("arrow glyph not found inside the send button (rect %+v, image %dx%d)", rect, img.Bounds().Dx(), img.Bounds().Dy())
	}
	centerX := float32(minX+maxX+1) / 2
	centerY := float32(minY+maxY+1) / 2
	if dx, dy := centerX-cx, centerY-cy; dx*dx+dy*dy > 2.25 {
		t.Fatalf("arrow center (%.1f, %.1f) is off the circle center (%.1f, %.1f); glyph bbox x[%d..%d] y[%d..%d]",
			centerX, centerY, cx, cy, minX, maxX, minY, maxY)
	}
}

// fixtureConfigs stands in for what fx reports at session/new: a provider, a
// long model list behind a filter, and the mode, which is rendered as the
// permission control rather than as another button.
func fixtureConfigs() []ConfigOption {
	return []ConfigOption{
		{ID: "provider", Name: "Provider", Category: "model", Type: "select", CurrentValue: "gateway",
			Options: []ConfigChoice{{Value: "gateway", Name: "gateway"}, {Value: "codex", Name: "codex"}}},
		{ID: "model", Name: "Model", Category: "model", Type: "select", CurrentValue: "zenmux/z-ai/glm-5.3-flash",
			Options: []ConfigChoice{
				{Value: "zenmux/z-ai/glm-5.3-flash", Name: "zenmux/z-ai/glm-5.3-flash"},
				{Value: "codex/gpt-5.5", Name: "codex/gpt-5.5"},
			}},
		{ID: "mode", Name: "Session Mode", Category: "mode", Type: "select", CurrentValue: "ask",
			Options: []ConfigChoice{{Value: "ask", Name: "Ask"}, {Value: "code", Name: "Code"}}},
	}
}

func fixtureModes() []ModeOption {
	return []ModeOption{
		{ID: "ask", Name: "Ask", Description: "Request permission before making changes"},
		{ID: "code", Name: "Code", Description: "Run routine work and review other actions"},
	}
}

// The strip renders whatever fx published: the mode as the permission
// control, and every other option as its own button. Nothing here is
// hard-coded to "model", so a new fx option appears without a code change.
func TestConversationRendersConfigFromData(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask"}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	mustHave(t, ts, "Provider · gateway", "Model · glm-5.3-flash")
	mustNotHaveToken(t, ts, "Ask", "Code", "All session settings")
	// The mode is the permission control, not a fourth config button.
	mustNotHave(t, ts, "Session Mode · ask")

	// Without the data there is nothing to show, and nothing is invented.
	h.Configs = nil
	h.Modes = nil
	ts.Frame()
	mustNotHave(t, ts, "Model · glm-5.3-flash", "Provider · gateway")

	// A session whose mode fx did not name still renders its id rather
	// than a blank segment.
	h.Modes = []ModeOption{{ID: "ask"}, {ID: "code"}}
	ts.Frame()
	mustNotHaveToken(t, ts, "ask", "code")
}

// Opening an option lists its choices; picking one dispatches the fx config
// id and value, then closes. One path serves every option.
func TestConversationConfigPickerDispatches(t *testing.T) {
	state := ConversationState{}
	var got [][2]string
	h := ConversationHost{Ready: true, Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask",
		SetConfig: func(configID, value string) { got = append(got, [2]string{configID, value}) }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	if err := ts.Click("Model · glm-5.3-flash"); err != nil {
		t.Fatal(err)
	}
	ts.Frame()
	if state.ConfigOpen != "model" {
		t.Fatalf("clicking an option must open its picker, got %q", state.ConfigOpen)
	}
	mustHave(t, ts, "codex/gpt-5.5", "current")
	if err := ts.Click("codex/gpt-5.5"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != [2]string{"model", "codex/gpt-5.5"} {
		t.Fatalf("dispatch: %v", got)
	}
	if state.ConfigOpen != "" {
		t.Fatalf("picking a value must close the picker, got %q", state.ConfigOpen)
	}
	if len(got) != 1 {
		t.Fatal("a click after closing dispatched twice")
	}
}

// The mode is dispatched through the same config path as everything else,
// and the current segment is not re-sent: that would spend a round trip to
// change nothing.
func TestConversationModeDoesNotResendCurrent(t *testing.T) {
	state := ConversationState{ConfigListOpen: true}
	var got [][2]string
	h := ConversationHost{Ready: true, Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask",
		SetConfig: func(configID, value string) { got = append(got, [2]string{configID, value}) }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	if err := ts.Click("Configure Session Mode"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Click("Code"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != [2]string{"mode", "code"} {
		t.Fatalf("mode dispatch: %v", got)
	}
	got = nil
	state.ConfigListOpen = true
	ts.Frame()
	if err := ts.Click("Configure Session Mode"); err != nil {
		t.Fatal(err)
	}
	if err := ts.Click("Ask"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("re-selecting the current mode must send nothing, got %v", got)
	}
}

// A picker left open across a session change points at an option the new
// session does not have. It must close rather than draw a panel about
// nothing.
func TestConversationClosesStaleConfigPicker(t *testing.T) {
	state := ConversationState{ConfigOpen: "model"}
	h := ConversationHost{Ready: true, Configs: []ConfigOption{{ID: "provider", Name: "Provider", CurrentValue: "gateway"}}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 700)
	ts.Frame()
	if state.ConfigOpen != "" {
		t.Fatalf("a picker for a vanished option must close, got %q", state.ConfigOpen)
	}
}

// A signed-out session says so where the user would otherwise just watch
// every prompt fail, and the guidance is fx's own sentence (§12.1).
func TestConversationShowsSignInGuidanceWhenFxCannotAuthenticate(t *testing.T) {
	const help = "fx needs access to Vercel AI Gateway. Run fx login to sign in."
	state := ConversationState{}
	signedIn := 0
	h := ConversationHost{Ready: true, Auth: AuthNotice{Auth: "missing", Help: help}, SignIn: func() { signedIn++ }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 700)
	mustHave(t, ts, help, "Sign in with fx…")
	if err := ts.Click("Sign in with fx…"); err != nil {
		t.Fatal(err)
	}
	if signedIn != 1 {
		t.Fatalf("the composer's guidance must open the sign-in flow, calls=%d", signedIn)
	}

	// Signed in: the composer goes back to being only a composer.
	h.Auth = AuthNotice{Auth: "configured provider", Providers: []string{"magpie"}}
	ts.Frame()
	mustNotHave(t, ts, "Sign in with fx…", help)
}

// The composer states what the session is doing, or says nothing at all: a
// status squeezed by a couple of pixels ellipsised to "Rea…", which reads as a
// bug rather than as a status. Which of the two it is comes from a
// measurement, because both terms of it are the window's to decide: the strip
// is as wide as fx's config options come out (§4.3), and the pane is as wide as
// the rail and the drawer leave it.
func TestConversationStatusIsShownWholeOrNotAtAll(t *testing.T) {
	// Widest first: a status that stops fitting as the window narrows must not
	// come back at a narrower one.
	widths := []int{1400, 1200, 1000, 900, 820, 780, 760, 740, 700, 660, 620, 580, 540, 500, 460, 420, 380}
	cases := []struct {
		name string
		host ConversationHost
		// shownAt is a width where the status plainly has the room to be
		// shown. The measurement must not over-hide: a strip on a FillWidth
		// basis reports the row's width rather than its own, and drops the
		// status long before the row is out of room.
		shownAt int
	}{
		{"idle", ConversationHost{Ready: true, Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask"}, 620},
		{"busy", ConversationHost{Ready: true, Busy: true, CanSteer: true, Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask"}, 660},
		// Without config options the status has the row to itself: it used to
		// be dropped below a fixed 760pt pane even so.
		{"no configs", ConversationHost{Ready: true}, 700},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := ConversationState{Draft: "Hello"}
			h := tc.host
			status := composerStatus(h, &state)
			if status == "" {
				t.Fatal("the fixture's session has no status to show")
			}
			draw := func(width int) (float32, bool) {
				t.Helper()
				ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, width, 800)
				ts.Frame()
				ts.Frame() // the first frame has no metrics to measure with
				r, ok := ts.Find(status)
				return r.W, ok
			}
			want, ok := draw(widths[0])
			if !ok {
				t.Fatalf("%q is not shown even at %d wide", status, widths[0])
			}
			if _, ok := draw(tc.shownAt); !ok {
				t.Errorf("%q is missing at %d wide, where it has the room to be shown", status, tc.shownAt)
			}
			shown := true
			for _, width := range widths[1:] {
				got, ok := draw(width)
				if !ok {
					shown = false
					continue
				}
				if !shown {
					t.Errorf("%q came back at %d wide after a narrower composer dropped it", status, width)
				}
				if got != want {
					t.Errorf("%q is %v wide at %d, want %v: a status that does not fit is left out, not squeezed into %q", status, got, width, want, "Rea…")
				}
			}
		})
	}
}

// The strip is as wide as fx's options come out, so it is measured rather than
// assumed: whatever the window leaves, the config strip stops before the button
// that sends. A strip that overflowed used to be drawn across the arrow, which
// is what the sentinel's screenshot showed.
func TestConversationConfigStripLeavesTheArrowItsRoom(t *testing.T) {
	for _, width := range []int{1600, 1200, 900, 780, 700, 620, 540, 460, 420, 380} {
		for _, busy := range []bool{false, true} {
			state := ConversationState{Draft: "Hello"}
			h := ConversationHost{Ready: true, Busy: busy, CanSteer: true,
				Configs: fixtureConfigs(), Modes: fixtureModes(), Mode: "ask"}
			ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, width, 800)
			ts.Frame()
			ts.Frame()
			strip, ok := ts.Find("Composer options")
			if !ok {
				t.Fatalf("no config strip at %d wide to measure", width)
			}
			arrow, ok := ts.Find("Send message")
			if !ok {
				t.Fatalf("no send button at %d wide", width)
			}
			if strip.X+strip.W > arrow.X {
				t.Errorf("at %d wide (busy=%v) the config strip reaches %v, past the send button at %v",
					width, busy, strip.X+strip.W, arrow.X)
			}
		}
	}
}

// B5/N2: the draft's identity follows the control's edit events. Typing,
// paste, undo and taking a mention each count, so a submission in flight can
// tell that the field it is about to clear is no longer the one it took.
func TestConversationReportsEveryKindOfDraftEdit(t *testing.T) {
	state := ConversationState{}
	edits := 0
	h := ConversationHost{
		Ready:        true,
		DraftEdited:  func() { edits++ },
		MentionFiles: []MentionFile{{Name: "main.go", Path: "main.go"}},
		CanMention:   true,
		Send:         func(string, []Attachment) bool { return true },
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 800, 600)
	if err := ts.Click("Message fx"); err != nil {
		t.Fatal(err)
	}
	ts.Type("abc")
	if edits == 0 {
		t.Fatal("typing did not count as an edit")
	}
	typed := edits

	ts.SetClipboard("pasted")
	ts.Key(ui.Cmd, ui.KeyV)
	if edits <= typed {
		t.Fatalf("paste did not count as an edit (%d edits)", edits)
	}
	pasted := edits

	ts.Key(ui.Cmd, ui.KeyZ)
	if edits <= pasted {
		t.Fatalf("undo did not count as an edit (%d edits)", edits)
	}
	undone := edits

	// Open the @ picker by typing a mention, then take the row with Enter.
	ts.Frame()
	ts.Type(" @ma")
	ts.Frame()
	if !state.MentionOpen {
		t.Fatalf("the mention picker did not open: %q open=%v q=%q %d-%d closed=%q", state.Draft, state.MentionOpen, state.MentionQuery, state.MentionStart, state.MentionEnd, state.MentionClosed)
	}
	ts.Key(0, ui.KeyEnter)
	if edits <= undone {
		t.Fatalf("taking a mention did not count as an edit (%d edits)", edits)
	}
	if !strings.Contains(state.Draft, "@main.go") {
		t.Fatalf("the mention was not inserted: %q", state.Draft)
	}
}
