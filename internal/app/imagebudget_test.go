package app

import (
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

// pending reports whether the app loop has work waiting.
func (l *manualLoop) pending() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queue) > 0
}

// headerOnlyPNG builds a PNG that declares w×h and carries no usable pixel
// data: a decoder can describe it from the header and cannot decode it. It is
// how these tests tell "refused before decoding" from "decoded and failed".
func headerOnlyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	chunk := func(kind string, data []byte) []byte {
		out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
		out = append(out, kind...)
		out = append(out, data...)
		return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	}
	ihdr := binary.BigEndian.AppendUint32(nil, uint32(w))
	ihdr = binary.BigEndian.AppendUint32(ihdr, uint32(h))
	ihdr = append(ihdr, 8, 6, 0, 0, 0)
	out := append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", ihdr)...)
	out = append(out, chunk("IDAT", []byte{0x78, 0x9c, 0x00})...)
	return append(out, chunk("IEND", nil)...)
}

// decodeWindow is the least window a budget test needs: an app loop the test
// drains, a conversation generation, and a budget the test can afford.
func decodeWindow(configure func(*imageDecodeBudget)) (*ProjectWindow, *manualLoop) {
	s := attachSession()
	loop := &manualLoop{}
	s.loop = loop.dispatch
	configure(&s.imageBudget)
	return s, loop
}

// R5: the budget blocks a second decode until the first gives its memory back.
// It is a queue, not a refusal: two ordinary images in one drop must both load.
func TestDecodeBudgetWaitsForRoomAndReleases(t *testing.T) {
	b := imageDecodeBudget{slots: 1, maxBytes: 8 << 20}
	releaseFirst, err := b.reserve(context.Background(), 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() {
		release, err := b.reserve(context.Background(), 4<<20)
		if err == nil {
			release()
		}
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("a second decode took the one slot while the first held it: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseFirst()
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the waiter was refused instead of admitted: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter never got the room the first decode released")
	}
	if used, inFlight := b.state(); used != 0 || inFlight != 0 {
		t.Fatalf("the budget did not return to zero: used=%d inFlight=%d", used, inFlight)
	}
}

// A batch retired while it waits for room must not leave a reservation behind,
// or the window would slowly lose its budget to drafts nobody can see.
func TestDecodeBudgetCancellationReleasesTheWaiter(t *testing.T) {
	b := imageDecodeBudget{slots: 1, maxBytes: 8 << 20}
	hold, err := b.reserve(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		release, err := b.reserve(ctx, 1<<20)
		if err == nil {
			release()
		}
		cancelled <- err
	}()
	cancel()
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled waiter reported %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled waiter kept waiting for room")
	}
	hold()
	if used, inFlight := b.state(); used != 0 || inFlight != 0 {
		t.Fatalf("a cancelled waiter left a reservation: used=%d inFlight=%d", used, inFlight)
	}
}

// One image whose decoded size alone exceeds the window is refused with a
// reason, not queued forever behind a limit it can never fit.
func TestDecodeBudgetRefusesAnImageTheWindowCannotHold(t *testing.T) {
	b := imageDecodeBudget{maxBytes: 1 << 20, slots: 2}
	if _, err := b.reserve(context.Background(), 2<<20); !errors.Is(err, errImageBudgetTooLarge) {
		t.Fatalf("reserve reported %v, want a refusal the user can act on", err)
	}
	if used, inFlight := b.state(); used != 0 || inFlight != 0 {
		t.Fatalf("a refused reservation was counted: used=%d inFlight=%d", used, inFlight)
	}
}

// R5: a drop shares the window's budget with whatever else is decoding, so two
// drops cannot each decide they own the memory. The second one waits.
func TestImageBatchSharesTheWindowBudget(t *testing.T) {
	s, loop := decodeWindow(func(b *imageDecodeBudget) { b.slots = 1; b.maxBytes = 8 << 20 })
	hold, err := s.imageBudget.reserve(context.Background(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, pngBytes(t, 40, 30), 0o600); err != nil {
		t.Fatal(err)
	}
	s.coord.attachImageFilesFor(s, []string{path}, s.swap.generation())

	time.Sleep(100 * time.Millisecond)
	if loop.pending() {
		t.Fatal("an attachment was published while the window budget was taken")
	}
	hold()
	loop.await(t)
	loop.drain()
	if len(s.chatUI.Attachments) != 1 {
		t.Fatalf("the waited-for image was not attached: %+v %q", s.chatUI.Attachments, s.chatUI.AttachError)
	}
	if used, inFlight := s.imageBudget.state(); used != 0 || inFlight != 0 {
		t.Fatalf("the budget outlived the batch: used=%d inFlight=%d", used, inFlight)
	}
}

// The reservation is taken before decoding and released on every exit path,
// including a decode that fails: a refusal must not shrink the window's budget
// for the rest of the session.
func TestDecodeBudgetReleasedOnFailureAndSuccess(t *testing.T) {
	s, _ := decodeWindow(func(b *imageDecodeBudget) { b.maxBytes = 64 << 20; b.slots = 1 })
	// Header says 400×300, pixels are missing: the header passes a limit that
	// admits it, the decode then fails after the reservation.
	if _, err := s.coord.prepareImage(s, context.Background(), "broken.png", headerOnlyPNG(t, 400, 300)); err == nil {
		t.Fatal("an image with no pixel data was prepared")
	}
	if used, inFlight := s.imageBudget.state(); used != 0 || inFlight != 0 {
		t.Fatalf("a failed decode kept its reservation: used=%d inFlight=%d", used, inFlight)
	}

	good := pngBytes(t, 40, 30)
	if _, err := s.coord.prepareImage(s, context.Background(), "shot.png", good); err != nil {
		t.Fatal(err)
	}
	if used, inFlight := s.imageBudget.state(); used != 0 || inFlight != 0 {
		t.Fatalf("a successful decode kept its reservation: used=%d inFlight=%d", used, inFlight)
	}
}

// The per-image header limit the window applies is the one that governs the
// decode, so a test (or a future product) can lower it in one place.
func TestWindowImageLimitGovernsTheDecode(t *testing.T) {
	s, _ := decodeWindow(func(b *imageDecodeBudget) { b.limit = chrome.ImageLimit{MaxSide: 10, MaxPixels: 100} })
	if _, err := s.coord.prepareImage(s, context.Background(), "shot.png", pngBytes(t, 40, 30)); err == nil {
		t.Fatal("an image larger than the window's own limit was prepared")
	}
	if used, inFlight := s.imageBudget.state(); used != 0 || inFlight != 0 {
		t.Fatalf("a refused image took budget: used=%d inFlight=%d", used, inFlight)
	}
}

// S3: a request that was already cancelled must not take a slot just because
// there happens to be room. The old fast path counted it and only refused once
// the budget was full, so a retired draft could still spend the window's
// memory and CPU on a decode nobody would look at.
func TestCancelledRequestTakesNoBudget(t *testing.T) {
	b := imageDecodeBudget{slots: 2, maxBytes: 8 << 20}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := b.reserve(ctx, 4)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled request was granted room: err=%v", err)
	}
	if release != nil {
		t.Fatal("a cancelled request returned a release")
	}
	if used, inFlight := b.state(); used != 0 || inFlight != 0 {
		t.Fatalf("a cancelled request moved the counters: used=%d inFlight=%d", used, inFlight)
	}
	// The whole budget is still there for a live request.
	live, err := b.reserve(context.Background(), 4<<20)
	if err != nil {
		t.Fatalf("a cancelled request shrank the budget: %v", err)
	}
	live()
}

// A cancellation that lands between the grant and the decode stops at the
// boundary: the reservation comes back and the bytes are never decoded. The
// fixture cannot be decoded at all, so "the decode ran" would be a decode
// error rather than a cancellation.
func TestCancelledPrepareImageDoesNotDecode(t *testing.T) {
	s, _ := decodeWindow(func(b *imageDecodeBudget) { b.maxBytes = 64 << 20; b.slots = 1 })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.coord.prepareImage(s, ctx, "broken.png", headerOnlyPNG(t, 400, 300))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("prepareImage reported %v, want the cancellation", err)
	}
	if used, inFlight := s.imageBudget.state(); used != 0 || inFlight != 0 {
		t.Fatalf("a cancelled image kept its reservation: used=%d inFlight=%d", used, inFlight)
	}
}

// The refusal names both numbers, so a person can tell an impossible image from
// a broken one: how much this image costs and what the window allows.
func TestImageTooLargeRefusalNamesBothNumbers(t *testing.T) {
	b := imageDecodeBudget{maxBytes: 1 << 20, slots: 1}
	_, err := b.reserve(context.Background(), 3<<20)
	if !errors.Is(err, errImageBudgetTooLarge) {
		t.Fatalf("reserve reported %v", err)
	}
	message := err.Error()
	if !strings.Contains(message, "3") || !strings.Contains(message, "1") {
		t.Fatalf("the refusal does not name the cost and the limit: %q", message)
	}
}
