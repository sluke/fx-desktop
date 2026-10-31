package chrome

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// pngImage builds a real PNG. Nothing in these tests fakes an image: the
// point of the attachment path is that it refuses what fx would refuse, and a
// stand-in byte string would be refused for the wrong reason.
func pngImage(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 0x80, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gifImage(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// The declared type comes from the bytes, never from the file name: fx rejects
// the whole message when a block's mimeType does not match what it decodes, so
// a .jpg that is really a PNG has to be announced as a PNG.
func TestImageTypeComesFromTheBytesNotTheName(t *testing.T) {
	a, err := NewImageAttachment("screenshot.jpg", pngImage(t, 8, 4))
	if err != nil {
		t.Fatal(err)
	}
	if a.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", a.MimeType)
	}
	if a.Width != 8 || a.Height != 4 || a.Dimensions() != "8×4" {
		t.Fatalf("dimensions = %dx%d (%q)", a.Width, a.Height, a.Dimensions())
	}
	if a.Preview == nil {
		t.Fatal("a decoded image has a preview")
	}
	gifAttachment, err := NewImageAttachment("dots.gif", gifImage(t))
	if err != nil {
		t.Fatal(err)
	}
	if gifAttachment.MimeType != "image/gif" {
		t.Fatalf("gif mime = %q", gifAttachment.MimeType)
	}
}

// Every refusal has to name the file and say what is wrong with it: an image
// that silently does not appear reads as a broken app, not as a rejected one.
func TestImageRefusalsSayWhatIsWrong(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		data     []byte
		contains string
	}{
		{"text", "notes.txt", []byte("just some words"), "not an image"},
		{"empty", "empty.png", nil, "empty"},
		{"oversized", "huge.png", make([]byte, MaxImageBytes+1), "fx takes up to 5.0 MB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewImageAttachment(tc.file, tc.data)
			if err == nil {
				t.Fatal("this image must be refused")
			}
			if !strings.Contains(err.Error(), tc.file) {
				t.Errorf("%q does not name the file", err)
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Errorf("%q does not explain (%q)", err, tc.contains)
			}
		})
	}
}

// The chip shows a 26pt picture, so the preview is capped: a phone photo must
// not become a bitmap of its full resolution just to be drawn that small.
func TestPreviewIsSmallerThanTheImage(t *testing.T) {
	a, err := NewImageAttachment("big.png", pngImage(t, 400, 200))
	if err != nil {
		t.Fatal(err)
	}
	if a.Width != 400 || a.Height != 200 {
		t.Fatalf("a large image keeps its real dimensions: %dx%d", a.Width, a.Height)
	}
	if w, h := a.Preview.Size(); w > previewPixels || h > previewPixels {
		t.Fatalf("preview is %dx%d, want at most %d on a side", w, h, previewPixels)
	}
}

func TestHumanSizeReadsLikeAPersonWroteIt(t *testing.T) {
	cases := map[int]string{0: "0 B", 999: "999 B", 2048: "2 KB", 5 << 20: "5.0 MB"}
	for n, want := range cases {
		if got := HumanSize(n); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

// The composer carries attachments the way it carries text: shown before they
// go, removable one at a time, and cleared once they are sent.
func TestComposerSendsAttachments(t *testing.T) {
	image := func(id uint64, name string) Attachment {
		a, err := NewImageAttachment(name, pngImage(t, 8, 8))
		if err != nil {
			t.Fatal(err)
		}
		a.ID = id
		return a
	}
	state := ConversationState{Attachments: []Attachment{image(1, "one.png"), image(2, "two.png")}}
	var sent []Attachment
	var removed []uint64
	h := ConversationHost{
		Ready:            true,
		CanAttach:        true,
		RemoveAttachment: func(id uint64) { removed = append(removed, id) },
		Send:             func(_ string, a []Attachment) bool { sent = a; return true },
	}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 600)
	mustHave(t, ts, "one.png", "two.png", "8×8", "drop or paste an image")

	if err := ts.Click("Remove image one.png"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(removed) != 1 || removed[0] != 1 {
		t.Fatalf("removed = %v, want [1]", removed)
	}

	// A message made of pictures alone is still a message.
	if err := ts.Click("Send message"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(sent) != 2 || sent[0].Name != "one.png" || len(state.Attachments) != 0 {
		t.Fatalf("sent = %v, attachments = %v", sent, state.Attachments)
	}
	ts.Frame()
	mustNotHave(t, ts, "one.png", "two.png")
}

// A running turn takes text alone — steering is text-only and a queued message
// carries only what was written with it. Rather than send the words and drop
// the pictures, the composer holds both and says why.
func TestComposerHoldsImagesWhileATurnRuns(t *testing.T) {
	a, err := NewImageAttachment("shot.png", pngImage(t, 8, 8))
	if err != nil {
		t.Fatal(err)
	}
	a.ID = 1
	state := ConversationState{Draft: "look at this", Attachments: []Attachment{a}}
	calls := 0
	h := ConversationHost{Ready: true, Busy: true, CanSteer: true, CanAttach: true,
		Send: func(string, []Attachment) bool { calls++; return true }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 1000, 600)
	mustHave(t, ts, "Working… · images wait for the next turn",
		"Images are attached with the next message · Enter still steers or queues text")

	// The arrow is drawn disabled, so clicking it is not an error — it is
	// simply not a way to send the pictures mid-turn.
	if err := ts.Click("Send message"); err != nil {
		t.Fatal(err)
	}
	ts.Key(0, ui.KeyEnter)
	if calls != 0 {
		t.Fatalf("a busy turn took the images: %d sends", calls)
	}
	if state.Draft != "look at this" || len(state.Attachments) != 1 {
		t.Fatalf("the message was not held: draft=%q attachments=%d", state.Draft, len(state.Attachments))
	}
}

func TestComposerShowsWhyAnImageWasRefused(t *testing.T) {
	state := ConversationState{AttachError: "huge.png is 6.0 MB; fx takes up to 5.0 MB per image"}
	h := ConversationHost{Ready: true, CanAttach: true, Send: func(string, []Attachment) bool { return true }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 500)
	mustHave(t, ts, "huge.png is 6.0 MB; fx takes up to 5.0 MB per image")
}

// A session whose fx cannot take images never mentions them: advertising an
// affordance the agent would refuse is worse than not having it (§A.8).
func TestComposerAdvertisesImagesOnlyWhenFxCanTakeThem(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Send: func(string, []Attachment) bool { return true }}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 500)
	mustNotHave(t, ts, "drop or paste an image")

	h.CanAttach = true
	ts.Frame()
	mustHave(t, ts, "drop or paste an image")
}

// A picture already sent is history, so the transcript keeps recording it
// whether or not fx could take another one today.
func TestTranscriptRecordsWhatWasSentWithAMessage(t *testing.T) {
	state := ConversationState{}
	h := ConversationHost{Ready: true, Messages: []ChatMessage{
		{ID: 1, Role: "user", Text: "does this look right?", Images: []string{"shot.png"}, Done: true},
	}}
	ts := ui.NewTester(func(c *ui.Context) { Conversation(c, &state, h) }, 900, 500)
	mustHave(t, ts, "shot.png", "does this look right?")
}

// The palette offers an image row only where a session could actually send
// one: a row that opens nothing is a lie (§A.8).
func TestPaletteOffersImageRowsOnlyWhereTheyWork(t *testing.T) {
	for _, id := range []string{"attach-image", "paste-image"} {
		var cmd Command
		for _, candidate := range Commands {
			if candidate.ID == id {
				cmd = candidate
			}
		}
		if cmd.ID == "" {
			t.Fatalf("the palette has no %q row", id)
		}
		if commandAvailable(Host{}, cmd) {
			t.Errorf("%s is offered with nothing behind it", id)
		}
		if !commandAvailable(Host{Actions: Actions{AttachImage: noop, PasteImage: noop}}, cmd) {
			t.Errorf("%s is hidden where it would work", id)
		}
	}

	h, sh := testHost()
	sh.PaletteOpen = true
	h.Actions.AttachImage = noop
	h.Actions.PasteImage = noop
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "Attach image…", "Paste image", "⇧⌘V")
}
