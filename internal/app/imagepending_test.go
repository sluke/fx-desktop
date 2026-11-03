package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// S5: two drops are two additions to the composer, but they are one window's
// memory. The second waits for the first to finish reading instead of taking a
// second batch's worth, which is what the per-batch limit alone allowed.
func TestPendingBudgetMakesTheSecondDropWait(t *testing.T) {
	b := imagePendingBudget{max: 1 << 20}
	hold, err := b.reserve(context.Background(), 600<<10)
	if err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() {
		release, err := b.reserve(context.Background(), 600<<10)
		if err == nil {
			release()
		}
		second <- err
	}()
	select {
	case err := <-second:
		t.Fatalf("a second drop took bytes the first was still holding: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	hold()
	select {
	case err := <-second:
		if err != nil {
			t.Fatalf("the waiting drop was refused instead of admitted: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting drop never got the bytes the first one gave back")
	}
	if got := b.state(); got != 0 {
		t.Fatalf("the budget did not return to zero: %d bytes held", got)
	}
}

// A drop that could never fit is refused with both numbers: waiting for room
// the window will never have would leave the user watching nothing happen.
func TestPendingBudgetRefusesADropItCannotHold(t *testing.T) {
	b := imagePendingBudget{max: 1 << 20}
	release, err := b.reserve(context.Background(), 2<<20)
	if !errors.Is(err, errPendingImagesTooLarge) {
		t.Fatalf("reserve reported %v, want a refusal the user can act on", err)
	}
	if release != nil {
		t.Fatal("a refused drop returned a release")
	}
	if got := b.state(); got != 0 {
		t.Fatalf("a refused drop was counted: %d bytes", got)
	}
	message := err.Error()
	if !strings.Contains(message, "2") || !strings.Contains(message, "1") {
		t.Fatalf("the refusal does not name the size and the limit: %q", message)
	}
}

// A batch retired while it waits must leave nothing behind, or a window would
// lose its budget to drafts nobody can see.
func TestPendingBudgetCancelledWaiterTakesNothing(t *testing.T) {
	b := imagePendingBudget{max: 1 << 20}
	hold, err := b.reserve(context.Background(), 900<<10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiter := make(chan error, 1)
	go func() {
		release, err := b.reserve(ctx, 200<<10)
		if err == nil {
			release()
		}
		waiter <- err
	}()
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled waiter reported %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled waiter kept waiting for bytes")
	}
	hold()
	if got := b.state(); got != 0 {
		t.Fatalf("a cancelled waiter left bytes behind: %d", got)
	}
}

// The clipboard path cannot wait: its bytes are already allocated, so the only
// honest answers are "admitted now" or "not this time".
func TestPendingBudgetTryReserveNeverWaits(t *testing.T) {
	b := imagePendingBudget{max: 100 << 10}
	hold, ok := b.tryReserve(90 << 10)
	if !ok {
		t.Fatal("a drop that fits was refused")
	}
	if _, ok := b.tryReserve(20 << 10); ok {
		t.Fatal("tryReserve admitted bytes that did not fit")
	}
	if _, ok := b.tryReserve(200 << 10); ok {
		t.Fatal("tryReserve admitted a size the window can never hold")
	}
	if got := b.state(); got != 90<<10 {
		t.Fatalf("a refused tryReserve moved the counter: %d", got)
	}
	hold()
	hold() // idempotent: a double release must not free someone else's bytes
	if got := b.state(); got != 0 {
		t.Fatalf("a double release left %d bytes held", got)
	}
	if release, ok := b.tryReserve(90 << 10); !ok {
		t.Fatal("the bytes were not given back")
	} else {
		release()
	}
}

// The window's admission is what decides: a drop larger than the window's
// pending ceiling is refused before a single byte is read, and the composer
// says so once rather than staying silent.
func TestWindowRefusesADropBeyondItsPendingBudget(t *testing.T) {
	s, loop := decodeWindow(func(*imageDecodeBudget) {})
	s.imagePending.max = 64 << 10
	path := filepath.Join(t.TempDir(), "big.png")
	if err := os.WriteFile(path, paddedPNG(t, 200<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	s.coord.attachImageFilesFor(s, []string{path}, s.swap.generation())
	loop.await(t)
	loop.drain()
	if n := len(s.chatUI.Attachments); n != 0 {
		t.Fatalf("an over-budget drop was attached (%d image(s))", n)
	}
	if s.chatUI.AttachError == "" {
		t.Fatal("the refusal was not shown")
	}
	if got := s.imagePending.state(); got != 0 {
		t.Fatalf("a refused drop kept %d bytes reserved", got)
	}
}

// Two drops share the window's ceiling, and the batch that waits is admitted
// once the one ahead gives its bytes back — the same contract the decode budget
// has, one layer earlier.
func TestTwoDropsShareTheWindowPendingBudget(t *testing.T) {
	s, loop := decodeWindow(func(*imageDecodeBudget) {})
	s.imagePending.max = 64 << 10
	// One byte short of the ceiling: even the smallest image has to wait.
	hold, err := s.imagePending.reserve(context.Background(), 64<<10-1)
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
		t.Fatal("an attachment was published while the window's bytes were held")
	}
	hold()
	loop.await(t)
	loop.drain()
	if n, err := len(s.chatUI.Attachments), s.chatUI.AttachError; n != 1 {
		t.Fatalf("the waiting drop was not attached (%d image(s)): %q", n, err)
	}
	if got := s.imagePending.state(); got != 0 {
		t.Fatalf("the batch kept %d bytes after it was adopted", got)
	}
}

// A paste whose bytes do not fit right now is refused with a sentence, not
// queued: the clipboard bytes are already allocated, and keeping them alive
// behind a slow drop is the memory this ceiling exists to bound.
func TestPastedImageIsRefusedWhileTheWindowIsFull(t *testing.T) {
	s, loop := decodeWindow(func(*imageDecodeBudget) {})
	s.imagePending.max = 4 << 10
	hold, err := s.imagePending.reserve(context.Background(), 4<<10-1)
	if err != nil {
		t.Fatal(err)
	}
	if s.coord.admitPastedImage(s, s.swap.generation(), pngBytes(t, 20, 20)) {
		t.Fatal("a paste was queued while the window's bytes were held")
	}
	if s.chatUI.AttachError == "" {
		t.Fatal("the refusal was not shown")
	}
	hold()
	if !s.coord.admitPastedImage(s, s.swap.generation(), pngBytes(t, 20, 20)) {
		t.Fatalf("a paste that fits was refused: %q", s.chatUI.AttachError)
	}
	loop.await(t)
	loop.drain()
	if n, err := len(s.chatUI.Attachments), s.chatUI.AttachError; n != 1 {
		t.Fatalf("the pasted image was not attached (%d image(s)): %q", n, err)
	}
	if got := s.imagePending.state(); got != 0 {
		t.Fatalf("the paste kept %d bytes after it was adopted", got)
	}
}
