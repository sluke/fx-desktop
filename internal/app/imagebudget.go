package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"fx-desktop/internal/app/chrome"
)

// imagePendingBytes is how many compressed image bytes one window may hold at
// once for pictures that are not in the composer yet.
//
// It equals one message's worth, so a second drop waits for the first to be
// read and decoded rather than doubling the window's peak. The decode budget
// below starts where this one ends: bytes admitted here become a decoded
// picture under imageDecodeBudget (S5).
const imagePendingBytes = imageBatchBytes

// imageDecodeBytes is how much decoded image memory one window may hold at
// once, and imageDecodeSlots how many images it may decode at a time.
//
// The per-batch byte budget bounds what is sent; this bounds what is held, and
// the two are different numbers: one message may carry several images, and two
// drops may be reading at the same time. A 24-megapixel image is ~96 MB once
// decoded, so the budget holds one of those plus a little, and two ordinary
// screenshots comfortably (R5).
const (
	imageDecodeBytes = 128 << 20
	imageDecodeSlots = 2
)

// errImageBudgetTooLarge refuses an image whose decoded size alone is beyond
// what the window will ever allow. Waiting for that much room would never end.
var errImageBudgetTooLarge = errors.New("this image needs more memory than the window allows for one picture")

// imageDecodeBudget is one window's share of decode memory and decode workers.
//
// A reservation is taken after the image's header is read and before it is
// decoded, released on every exit path — success, refusal and cancellation —
// and shared by every batch of the window, so a second drop cannot spend the
// budget the first one already promised. A waiter blocks until room is free
// rather than being refused, and its wait ends with its own cancellation, so
// closing the window or replacing the conversation releases it too (R5).
//
// What it does not bound, and does not claim to: the decoder's own working set
// (a measured 1.6–2% of the pixel buffer for PNG, a fixed ~33 KB for GIF), the
// thumbnail built from the decode, and the resident memory those become. The
// compressed bytes on their way here are admitted by imagePendingBudget, and the
// attachments that reached the composer are bounded by the message limit in
// adoptAttachment; measuring the window's resident memory is a separate job
// (§4.2, T23).
type imageDecodeBudget struct {
	mu sync.Mutex
	// limit is what one image may expand to; the package default when zero,
	// and a small limit in tests.
	limit chrome.ImageLimit
	// maxBytes and slots are this window's bounds; the defaults above when
	// zero.
	maxBytes int
	slots    int
	used     int
	inFlight int
	// wait is closed and replaced on every release, which wakes every waiter
	// without polling.
	wait chan struct{}
}

// imageLimit is the per-image header bound this window applies.
func (b *imageDecodeBudget) imageLimit() chrome.ImageLimit {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit.MaxSide == 0 && b.limit.MaxPixels == 0 {
		return chrome.DefaultImageLimit
	}
	return b.limit
}

func (b *imageDecodeBudget) bounds() (maxBytes, slots int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	maxBytes, slots = b.maxBytes, b.slots
	if maxBytes <= 0 {
		maxBytes = imageDecodeBytes
	}
	if slots <= 0 {
		slots = imageDecodeSlots
	}
	return maxBytes, slots
}

// reserve claims room for one decode and returns the release. An error means
// the request was cancelled or the image can never fit; the two are different
// answers, so they are different errors.
//
// Cancellation is checked before every counter changes — on the fast path when
// there is room, and again after each wake — so a retired draft never takes a
// slot or a byte it will not use (S3). A decode that has already started cannot
// be interrupted; this only refuses to start one.
func (b *imageDecodeBudget) reserve(ctx context.Context, bytes int) (func(), error) {
	if bytes < 0 {
		bytes = 0
	}
	maxBytes, slots := b.bounds()
	if bytes > maxBytes {
		return nil, fmt.Errorf("%w: it needs %s decoded, and this window keeps %s for one picture",
			errImageBudgetTooLarge, chrome.HumanSize(bytes), chrome.HumanSize(maxBytes))
	}
	b.mu.Lock()
	if b.wait == nil {
		b.wait = make(chan struct{})
	}
	for {
		if err := ctx.Err(); err != nil {
			// Under the lock and before the counters move: there is room,
			// but nobody is waiting for this image any more.
			b.mu.Unlock()
			return nil, err
		}
		if b.inFlight < slots && b.used+bytes <= maxBytes {
			break
		}
		wait := b.wait
		b.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		b.mu.Lock()
	}
	b.used += bytes
	b.inFlight++
	b.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			b.mu.Lock()
			b.used -= bytes
			b.inFlight--
			if b.wait != nil {
				close(b.wait)
			}
			b.wait = make(chan struct{})
			b.mu.Unlock()
		})
	}
	return release, nil
}

// inUse reports the budget's current state. It exists for tests: the
// reservation has to come back on every path, and the only way to know is to
// look.
func (b *imageDecodeBudget) state() (used, inFlight int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used, b.inFlight
}

// errPendingImagesTooLarge refuses a drop whose own bytes are beyond what the
// window holds while pictures are being added. Waiting for that much room would
// never end, so it is a refusal rather than a wait.
var errPendingImagesTooLarge = errors.New("this window holds a limited amount of image data while pictures are being added")

// imagePendingBudget is one window's admission for compressed image bytes that
// are being read, decoded, or still waiting to be adopted.
//
// The decode budget bounds what one picture costs once it is open. This bounds
// what is in flight before that: the files a batch is reading, the clipboard
// bytes a paste is holding, and the reads that have decoded but are not in the
// composer yet. Without it each drop took a full imageBatchBytes on its own, so
// three quick drops held three batches' worth and only the last could still fit
// in the message (S5).
//
// A batch reserves its whole need in one call, before it reads anything, and
// gives it back when its worker ends. Reserving the whole batch at once is what
// keeps waiting deadlock-free: a waiter holds nothing while it waits, so two
// batches cannot each wait for room the other is holding. Bytes that reached
// the composer are no longer counted here — the message limit in adoptAttachment
// bounds those.
type imagePendingBudget struct {
	mu sync.Mutex
	// max is this window's bound; the default above when zero, and a small
	// number in tests.
	max  int
	used int
	// wait is closed and replaced on every release, which wakes every waiter
	// without polling.
	wait chan struct{}
}

// bounds is the window's byte limit, with the package default for a zero value.
func (b *imagePendingBudget) bounds() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.max <= 0 {
		return imagePendingBytes
	}
	return b.max
}

// reserve admits bytes for one batch, waiting for other batches to finish. The
// wait ends with the caller's cancellation, so a retired draft or a closed
// window does not stay queued.
func (b *imagePendingBudget) reserve(ctx context.Context, bytes int) (func(), error) {
	if bytes < 0 {
		bytes = 0
	}
	max := b.bounds()
	if bytes > max {
		return nil, fmt.Errorf("%w: they need %s, and it holds %s", errPendingImagesTooLarge,
			chrome.HumanSize(bytes), chrome.HumanSize(max))
	}
	b.mu.Lock()
	if b.wait == nil {
		b.wait = make(chan struct{})
	}
	for {
		if err := ctx.Err(); err != nil {
			b.mu.Unlock()
			return nil, err
		}
		if b.used+bytes <= max {
			break
		}
		wait := b.wait
		b.mu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		b.mu.Lock()
	}
	b.used += bytes
	b.mu.Unlock()
	return b.releaser(bytes), nil
}

// tryReserve admits bytes only if they fit right now. It is the path for the
// clipboard, where the bytes already exist and queueing them would hold them
// for as long as the drop ahead is still reading.
func (b *imagePendingBudget) tryReserve(bytes int) (func(), bool) {
	if bytes < 0 {
		bytes = 0
	}
	max := b.bounds()
	if bytes > max {
		return nil, false
	}
	b.mu.Lock()
	if b.used+bytes > max {
		b.mu.Unlock()
		return nil, false
	}
	b.used += bytes
	b.mu.Unlock()
	return b.releaser(bytes), true
}

// releaser returns the idempotent hand-back for one reservation.
func (b *imagePendingBudget) releaser(bytes int) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.used -= bytes
			if b.wait != nil {
				close(b.wait)
			}
			b.wait = make(chan struct{})
			b.mu.Unlock()
		})
	}
}

// state is what the tests ask: how many bytes are held right now.
func (b *imagePendingBudget) state() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}
