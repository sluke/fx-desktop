package app

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

// pngBytes builds a real PNG so the attachment path runs its real decode,
// rather than a stand-in that would be refused for the wrong reason.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// paddedPNG builds a valid PNG of exactly total bytes: a one-pixel image plus
// a tEXt chunk of padding. It weighs what the size limits are about while
// still decoding, so a test of those limits exercises the real path.
func paddedPNG(t *testing.T, total int) []byte {
	t.Helper()
	chunk := func(kind string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		out = append(out, kind...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write([]byte{0, 0, 0, 0, 0}); err != nil { // one RGBA pixel
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	ihdr := binary.BigEndian.AppendUint32(nil, 1)
	ihdr = binary.BigEndian.AppendUint32(ihdr, 1)
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // 8-bit RGBA
	out := append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", ihdr)...)
	out = append(out, chunk("IDAT", compressed.Bytes())...)
	// The padding cannot use the tEXt chunk's 12 bytes of overhead, its
	// "pad\0" keyword, or IEND's 12.
	pad := total - len(out) - 12 - 4 - 12
	if pad < 0 {
		t.Fatalf("a padded PNG cannot be %d bytes; its smallest form is larger", total)
	}
	out = append(out, chunk("tEXt", append([]byte("pad\x00"), bytes.Repeat([]byte("x"), pad)...))...)
	out = append(out, chunk("IEND", nil)...)
	if len(out) != total {
		t.Fatalf("padded PNG is %d bytes, want %d", len(out), total)
	}
	return out
}

// attachSession is the least session an attachment needs: a native chat, a
// composer, and no window (invalidate is a no-op without one).
func attachSession() *Session {
	return &Session{coord: NewCoordinator(), chat: &chatSession{}}
}

// What fx would refuse never reaches the wire, and the refusal lands where the
// image was added rather than after the message is sent.
func TestAttachImageRefusesBeforeTheWire(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := attachSession()
	s.coord = c

	if err := c.attachImage(s, "notes.txt", []byte("plain words")); err == nil {
		t.Fatal("a text file is not an image")
	}
	if len(s.chatUI.Attachments) != 0 {
		t.Fatalf("a refused image must not stay attached: %v", s.chatUI.Attachments)
	}
	if !strings.Contains(s.chatUI.AttachError, "not an image") {
		t.Fatalf("the composer has no reason to show: %q", s.chatUI.AttachError)
	}

	if err := c.attachImage(s, "huge.png", make([]byte, chrome.MaxImageBytes+1)); err == nil {
		t.Fatal("an oversized image is refused before it is decoded")
	}

	if err := c.attachImage(s, "shot.png", pngBytes(t, 4, 4)); err != nil {
		t.Fatal(err)
	}
	if len(s.chatUI.Attachments) != 1 || s.chatUI.Attachments[0].ID == 0 {
		t.Fatalf("attachment = %+v", s.chatUI.Attachments)
	}
	if s.chatUI.Attachments[0].MimeType != "image/png" {
		t.Fatalf("mime = %q", s.chatUI.Attachments[0].MimeType)
	}
	// A good image clears the refusal it replaced.
	if s.chatUI.AttachError != "" {
		t.Fatalf("stale refusal: %q", s.chatUI.AttachError)
	}
}

// base64 inflates an attachment by a third, so images each inside the per
// image limit can still add up to a message fx will not read. The answer has
// to arrive while the user is still composing.
func TestAttachRefusesAMessageOverThePromptLimit(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := attachSession()
	s.coord = c
	// One image at the very top of fx's per image limit is legal on its own.
	if err := c.attachImage(s, "first.png", paddedPNG(t, chrome.MaxImageBytes)); err != nil {
		t.Fatal(err)
	}
	// A second one is not: two of them encode to more than one prompt.
	err := c.attachImage(s, "second.png", paddedPNG(t, chrome.MaxImageBytes))
	if err == nil {
		t.Fatal("two five-megabyte images are more than one prompt")
	}
	if !strings.Contains(err.Error(), chrome.HumanSize(chrome.MaxPromptBytes)) {
		t.Fatalf("refusal does not name the limit: %q", err)
	}
	if len(s.chatUI.Attachments) != 1 {
		t.Fatalf("the second image stayed attached: %d", len(s.chatUI.Attachments))
	}
}

func TestAttachImageFilesTakesImagesAndNamesTheRest(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := attachSession()
	s.coord = c
	dir := t.TempDir()
	good := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(good, pngBytes(t, 6, 6), 0o600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(bad, []byte("words"), 0o600); err != nil {
		t.Fatal(err)
	}

	c.attachImageFiles(s, []string{good, bad, filepath.Join(dir, "missing.png")})
	if len(s.chatUI.Attachments) != 1 || s.chatUI.Attachments[0].Name != "shot.png" {
		t.Fatalf("attachments = %+v", s.chatUI.Attachments)
	}
	if !strings.Contains(s.chatUI.AttachError, "notes.txt") || !strings.Contains(s.chatUI.AttachError, "missing.png") {
		t.Fatalf("the drop did not report what it skipped: %q", s.chatUI.AttachError)
	}
}

// A drop on a window that could not send an image is not a silent no-op: the
// shell asks nothing of a session that is not a native one.
func TestAttachIsIgnoredWithoutANativeSession(t *testing.T) {
	c := NewCoordinator()
	s := &Session{coord: c}
	c.attachImageFiles(s, []string{"anything.png"})
	if len(s.chatUI.Attachments) != 0 || s.chatUI.AttachError != "" {
		t.Fatalf("a terminal session took an image: %d attachments, %q",
			len(s.chatUI.Attachments), s.chatUI.AttachError)
	}
}

func TestRemoveAttachmentTakesBackOneImage(t *testing.T) {
	testutil.IsolateHome(t)
	c := NewCoordinator()
	s := attachSession()
	s.coord = c
	for _, name := range []string{"one.png", "two.png"} {
		if err := c.attachImage(s, name, pngBytes(t, 3, 3)); err != nil {
			t.Fatal(err)
		}
	}
	first := s.chatUI.Attachments[0].ID

	c.removeAttachment(s, first)
	if len(s.chatUI.Attachments) != 1 || s.chatUI.Attachments[0].Name != "two.png" {
		t.Fatalf("attachments = %+v", s.chatUI.Attachments)
	}
}
