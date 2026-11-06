package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"fx-desktop/internal/app/chrome"
)

// imageExtensions are the file types the picker offers. They are what fx
// accepts (measured, §12.3.2); the bytes still decide the declared type, so a
// mislabelled file is caught rather than sent under a false name.
var imageExtensions = []string{"png", "jpg", "jpeg", "gif", "webp"}

// imageBatchBytes is what one drop may read in total. base64 costs a third on
// the wire, so three quarters of the prompt limit is the most raw image data a
// message can carry; refusing the rest here keeps a long file list from
// loading far more than any message could hold (N5).
const imageBatchBytes = chrome.MaxPromptBytes / 4 * 3

// pendingImage is a file a worker has read, waiting for the app loop to attach
// it.
type pendingImage struct {
	name string
	data []byte
}

// imageBatch is one attachment request: the files it will read, and the
// identity it must still have when the result is ready to publish.
//
// A batch is not cancelled by a newer one — two drops are two additions to the
// same composer — but a replaced conversation or a closed window retires it,
// and a result is only attached while its own batch and conversation are still
// current (N1).
type imageBatch struct {
	// conversation is the generation of the session that asked for the
	// images. A replacement advances it; this batch's result then belongs to
	// a draft the user can no longer see.
	conversation uint64
	paths        []string
	// pasted is an image the clipboard handed over rather than a file. The
	// system read happens on the main thread, because that is where the
	// clipboard lives; everything after it — validating, decoding, budgeting
	// — is worker work like a dropped file's (R5).
	pasted     []byte
	pastedName string
	// pending is the window's compressed-byte reservation when the caller has
	// already taken one — the clipboard, whose bytes exist before the worker
	// runs. A batch started from paths takes its own reservation in the
	// worker (S5).
	pending func()
	ctx     context.Context
	cancel  context.CancelFunc
}

// attachImageFiles turns dropped or chosen files into images on the next
// message. Everything that fails says why: a drop that quietly did nothing
// looks like the app is broken.
//
// The disk work — stat, the size check, the read and the decode — happens on a
// worker. Attaching an image is not a reason to freeze the composer on a slow
// disk, and the size is bounded before the read so a huge file is never loaded
// only to be refused (§2.1, N5).
func (c *Coordinator) attachImageFiles(s *ProjectWindow, paths []string) {
	if s == nil || s.chat == nil || len(paths) == 0 {
		return
	}
	c.attachImageFilesFor(s, paths, s.swap.generation())
}

// attachImageFilesFor is the same request with the conversation generation
// captured where the user expressed the intent — before a system dialog or a
// queued main-thread callback could wait for a different conversation. A
// picker opened for A does not attach through B (N1).
func (c *Coordinator) attachImageFilesFor(s *ProjectWindow, paths []string, conversation uint64) {
	if s == nil || s.chat == nil || len(paths) == 0 {
		return
	}
	if s.swap.generation() != conversation {
		return
	}
	c.enqueueImageBatch(s, &imageBatch{
		conversation: conversation,
		paths:        append([]string(nil), paths...),
	})
}

// enqueueImageBatch gives a batch its lifetime and starts the worker. A batch
// is retired by a replaced conversation or a closed window, so the lifetime is
// the window's, not the caller's.
func (c *Coordinator) enqueueImageBatch(s *ProjectWindow, batch *imageBatch) {
	ctx, cancel := context.WithCancel(s.lifetime())
	batch.ctx, batch.cancel = ctx, cancel
	s.mu.Lock()
	s.images = append(s.images, batch)
	s.mu.Unlock()
	go c.readImageBatch(s, batch)
}

// readImageBatch reads one drop, item by item, within the batch's byte budget
// and with the batch's cancellation checked between files. Decoding happens
// here as well: a high-resolution image is the most expensive part of the
// request, and the app loop must not pay it (N5).
//
// The batch is admitted by the window before it reads anything, and gives those
// bytes back when it ends: one drop's worth of compressed data is what a window
// holds at a time, however many drops arrive at once (S5).
func (c *Coordinator) readImageBatch(s *ProjectWindow, batch *imageBatch) {
	defer batch.cancel()
	if batch.pending != nil {
		defer batch.pending()
	} else {
		// The window's admission for compressed bytes, taken once for the
		// whole batch before anything is read and given back when this worker
		// ends (S5). Reserving everything at once is what makes the wait
		// deadlock-free: a batch that waits holds nothing while it waits.
		need := batchBytes(batch)
		release, err := s.imagePending.reserve(batch.ctx, need)
		if err != nil {
			if batch.ctx.Err() != nil {
				return
			}
			// Too large for this window to ever hold: say so, instead of
			// queueing a drop that could never be admitted.
			s.dispatch(func() { c.adoptImages(s, batch, nil, []string{err.Error()}) })
			return
		}
		defer release()
	}
	var loaded []chrome.Attachment
	var refused []string
	total := 0
	if len(batch.pasted) > 0 {
		// Clipboard bytes were already in memory when the paste was admitted,
		// so this batch holds them from the start rather than reading them.
		attachment, err := c.prepareImage(s, batch.ctx, batch.pastedName, batch.pasted)
		if batch.ctx.Err() != nil {
			return
		}
		if err != nil {
			refused = append(refused, err.Error())
		} else {
			loaded = append(loaded, attachment)
		}
	}
	for _, path := range batch.paths {
		if batch.ctx.Err() != nil {
			// Retired: the draft, conversation or window that asked for this
			// is gone. Publish nothing at all.
			return
		}
		allowance := imageBatchBytes - total
		if allowance <= 0 {
			refused = append(refused, fmt.Sprintf("the drop is over %s in total; add the rest in another message",
				chrome.HumanSize(imageBatchBytes)))
			break
		}
		image, err := readImageFile(batch.ctx, path, min(allowance, chrome.MaxImageBytes))
		if err != nil {
			refused = append(refused, err.Error())
			continue
		}
		// The window's budget, not the batch's: a second drop shares it, so
		// two batches cannot each assume they own the memory (R5).
		attachment, err := c.prepareImage(s, batch.ctx, image.name, image.data)
		if err != nil {
			if batch.ctx.Err() != nil {
				return
			}
			refused = append(refused, err.Error())
			continue
		}
		total += len(image.data)
		loaded = append(loaded, attachment)
	}
	if batch.ctx.Err() != nil {
		return
	}
	s.dispatch(func() { c.adoptImages(s, batch, loaded, refused) })
}

// prepareImage validates and decodes one image on a worker, inside the window's
// decode budget. The header is read first, so the pixels a compressed file
// claims are refused before they are allocated; the reservation is taken
// between the two and released however the decode ends — success, refusal or
// cancellation (R5).
func (c *Coordinator) prepareImage(s *ProjectWindow, ctx context.Context, name string, data []byte) (chrome.Attachment, error) {
	limit := s.imageBudget.imageLimit()
	info, err := chrome.InspectImage(name, data, limit)
	if err != nil {
		return chrome.Attachment{}, err
	}
	// The peak, not just the pixel buffer: the decoder's working set and the
	// preview bitmap are alive at the same time as the decoded image (T25).
	release, err := s.imageBudget.reserve(ctx, info.EstimatedDecodePeakBytes())
	if err != nil {
		if ctx.Err() != nil {
			return chrome.Attachment{}, ctx.Err()
		}
		return chrome.Attachment{}, fmt.Errorf("%s: %w", name, err)
	}
	defer release()
	// The reservation is granted under the budget's lock, but a cancellation
	// can land between the grant and the decode. Checking once more means a
	// retired draft does not pay for decoding an image nobody will see (S3).
	// A decode that has already started cannot be interrupted — the result is
	// discarded instead.
	if err := ctx.Err(); err != nil {
		return chrome.Attachment{}, err
	}
	return chrome.NewImageAttachmentWithin(name, data, limit)
}

// readImageFile bounds one image's read and reports what went wrong in words a
// person can act on.
//
// The size is checked from Lstat first, so a FIFO is refused without being
// opened (opening one for reading blocks until a writer arrives). The opened
// file is checked again as a regular file, and the bytes are read through
// LimitReader: a file that grows between the check and the read is still cut
// at the limit instead of being loaded whole (N5).
func readImageFile(ctx context.Context, path string, limit int) (pendingImage, error) {
	name := filepath.Base(path)
	info, err := os.Lstat(path)
	if err != nil {
		return pendingImage{}, fmt.Errorf("%s is not a file", name)
	}
	if !info.Mode().IsRegular() {
		return pendingImage{}, fmt.Errorf("%s is not a regular file", name)
	}
	if info.Size() > int64(limit) {
		return pendingImage{}, imageTooLarge(name, info.Size(), limit)
	}
	if err := ctx.Err(); err != nil {
		return pendingImage{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return pendingImage{}, fmt.Errorf("could not read %s", name)
	}
	defer file.Close()
	// The path could have been replaced between the check and the open; the
	// handle is what will actually be read.
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return pendingImage{}, fmt.Errorf("%s is not a regular file", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return pendingImage{}, fmt.Errorf("could not read %s", name)
	}
	if len(data) > limit {
		return pendingImage{}, imageTooLarge(name, int64(len(data)), limit)
	}
	if err := ctx.Err(); err != nil {
		return pendingImage{}, err
	}
	return pendingImage{name: name, data: data}, nil
}

// imageTooLarge names the limit that was hit: one image's own, or the budget
// the whole drop has left.
func imageTooLarge(name string, size int64, limit int) error {
	if limit < chrome.MaxImageBytes {
		return fmt.Errorf("%s does not fit in this message; the images already chosen take the rest of %s",
			name, chrome.HumanSize(imageBatchBytes))
	}
	return fmt.Errorf("%s is %s; fx takes up to %s per image",
		name, chrome.HumanSize(int(size)), chrome.HumanSize(chrome.MaxImageBytes))
}

// batchBytes is what one batch will hold while it is read: its clipboard bytes
// plus each file's size, capped at the batch's own read limit. A path that
// cannot be stat'ed adds nothing here — it is refused with its own message and
// never read, so it never holds bytes.
func batchBytes(batch *imageBatch) int {
	need := int64(len(batch.pasted))
	for _, path := range batch.paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		need += min(info.Size(), int64(chrome.MaxImageBytes))
	}
	if need > int64(imageBatchBytes) {
		need = int64(imageBatchBytes)
	}
	return int(need)
}

// adoptImages attaches what the worker read. It runs on the app loop, where
// the composer's state is owned, and only while the request that started the
// read still owns the draft (N1).
func (c *Coordinator) adoptImages(s *ProjectWindow, batch *imageBatch, loaded []chrome.Attachment, refused []string) {
	if s == nil || s.chat == nil {
		return
	}
	s.mu.Lock()
	tracked := false
	for i, active := range s.images {
		if active == batch {
			s.images = append(s.images[:i], s.images[i+1:]...)
			tracked = true
			break
		}
	}
	s.mu.Unlock()
	if !tracked || s.swap.generation() != batch.conversation {
		// The conversation was replaced, the window closed, or the batch was
		// retired while it read: the result belongs to a draft that is gone.
		return
	}
	for _, attachment := range loaded {
		if err := c.adoptAttachment(s, attachment); err != nil {
			refused = append(refused, err.Error())
		}
	}
	// One sentence the user can act on beats a paragraph about every file in
	// the drop.
	if len(refused) > 0 {
		c.setAttachError(s, strings.Join(refused, " · "))
	}
}

// retireImageBatches cancels every read this window started and forgets them.
// Called when the conversation is replaced or the window closes; the worker's
// own check makes the same fact hold after a queued callback (§4.4).
func (s *ProjectWindow) retireImageBatches() {
	s.mu.Lock()
	batches := s.images
	s.images = nil
	s.mu.Unlock()
	for _, batch := range batches {
		if batch.cancel != nil {
			batch.cancel()
		}
	}
}

// attachImage validates one image and puts it in the composer. It returns the
// refusal as well as showing it, so a drop of several files can collect what
// went wrong and report it once. It runs the same decode path as a batch, so a
// caller that hands it bytes cannot skip the window's budget.
func (c *Coordinator) attachImage(s *ProjectWindow, name string, data []byte) error {
	if s == nil || s.chat == nil {
		return nil
	}
	attachment, err := c.prepareImage(s, context.Background(), name, data)
	if err != nil {
		c.setAttachError(s, err.Error())
		return err
	}
	if err := c.adoptAttachment(s, attachment); err != nil {
		c.setAttachError(s, err.Error())
		return err
	}
	return nil
}

// adoptAttachment puts an already validated image in the composer. The whole
// message has to fit ACP's input limit, and base64 inflates an image by a
// third: several allowed images can still add up to a prompt fx will not read.
// Checking here means the answer arrives while the user is still composing,
// not after they hit send. It uses the same size function as the submit path,
// so "adding an image" and "sending the message" cannot disagree about the
// limit; the latter also re-checks text and @ blocks added afterwards (N6).
func (c *Coordinator) adoptAttachment(s *ProjectWindow, attachment chrome.Attachment) error {
	withNew := append(append([]chrome.Attachment(nil), s.chatUI.Attachments...), attachment)
	if promptSizeBytes(s.chatUI.Draft, toPromptImages(withNew), nil) > chrome.MaxPromptBytes {
		return fmt.Errorf("that would make the message over %s, which is all fx reads; remove an image first",
			chrome.HumanSize(chrome.MaxPromptBytes))
	}
	s.attachmentSeq++
	attachment.ID = s.attachmentSeq
	s.chatUI.Attachments = append(s.chatUI.Attachments, attachment)
	s.chatUI.AttachError = ""
	s.invalidate()
	return nil
}

// removeAttachment takes one image back out of the composer. It builds a new
// slice rather than compacting in place: an in-flight submission may still hold
// the old array as its snapshot, and reusing its capacity would make that
// snapshot describe chips it never carried (§B2).
func (c *Coordinator) removeAttachment(s *ProjectWindow, id uint64) {
	if s == nil {
		return
	}
	kept := make([]chrome.Attachment, 0, len(s.chatUI.Attachments))
	for _, a := range s.chatUI.Attachments {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	s.chatUI.Attachments = kept
	s.chatUI.AttachError = ""
	s.invalidate()
}

func (c *Coordinator) setAttachError(s *ProjectWindow, message string) {
	if s == nil {
		return
	}
	s.chatUI.AttachError = message
	s.invalidate()
}

// pasteImage puts the clipboard's image into the composer. Clipboard access
// is main-thread work that may pump the loop, so it is queued out of the
// native callback that asked for it — the same reason pickFolder is.
func (c *Coordinator) pasteImage(s *ProjectWindow) {
	if s == nil || s.chat == nil {
		return
	}
	// The paste was asked for in this conversation; the clipboard read runs
	// on the main thread and must not attach into whichever conversation is
	// current by then (N1).
	conversation := s.swap.generation()
	mygo.App.WhenReady(func() { c.pasteImageOnMain(s, conversation) })
}

func (c *Coordinator) pasteImageOnMain(s *ProjectWindow, conversation uint64) {
	if s == nil || s.chat == nil || s.swap.generation() != conversation {
		return
	}
	// An image arrives as PNG whatever the system's own format was, so one
	// request covers a copied screenshot whatever made it.
	if _, ok := transfer.PreferredFormat(mygo.Clipboard.Formats(), transfer.PNG); !ok {
		c.setAttachError(s, "The clipboard has no image on it.")
		return
	}
	data, err := mygo.Clipboard.Read(transfer.PNG)
	if err != nil {
		c.setAttachError(s, "Could not read an image off the clipboard.")
		return
	}
	png, err := data.Read(transfer.PNG)
	if err != nil {
		c.setAttachError(s, "Could not read an image off the clipboard.")
		return
	}
	// The clipboard read is main-thread work; decoding is not. Handing the
	// bytes to the batch worker keeps a large paste from freezing the composer
	// and puts it under the same window budget as a dropped file (R5).
	c.admitPastedImage(s, conversation, png)
}

// admitPastedImage queues clipboard bytes for decoding, if the window can hold
// them right now. The bytes already exist by the time this runs, so they are
// admitted now or not at all: queueing them would keep them alive for as long
// as the drop ahead is still reading, which is exactly the memory that budget
// exists to bound (S5). It reports whether the paste was queued.
func (c *Coordinator) admitPastedImage(s *ProjectWindow, conversation uint64, png []byte) bool {
	if s == nil || s.chat == nil {
		return false
	}
	if len(png) > chrome.MaxImageBytes {
		c.setAttachError(s, fmt.Sprintf("Pasted image.png is %s; fx takes up to %s per image",
			chrome.HumanSize(len(png)), chrome.HumanSize(chrome.MaxImageBytes)))
		return false
	}
	release, ok := s.imagePending.tryReserve(len(png))
	if !ok {
		c.setAttachError(s, "Another image is still being added; paste this one again in a moment.")
		return false
	}
	c.enqueueImageBatch(s, &imageBatch{conversation: conversation, pasted: png, pastedName: "Pasted image.png", pending: release})
	return true
}

// pickImage asks for image files and attaches what comes back.
func (c *Coordinator) pickImage(s *ProjectWindow) {
	if s == nil || s.chat == nil {
		return
	}
	parent := s.winSnapshot()
	// The dialog is a delay the user can feel: capture the conversation it
	// was opened for, so files chosen here cannot land in a session that
	// replaced it meanwhile (N1).
	conversation := s.swap.generation()
	// A synchronous dialog cannot finish the draw it was opened from, so the
	// whole interaction is queued (see pickFolder).
	mygo.App.WhenReady(func() {
		if parent != nil && parent.IsDestroyed() {
			return
		}
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   parent,
			Title:    "Attach image",
			Multiple: true,
			Filters:  []mygo.FileFilter{{Name: "Images", Extensions: imageExtensions}},
		})
		if err != nil || len(paths) == 0 {
			return
		}
		c.attachImageFilesFor(s, paths, conversation)
	})
}

// encodedSize is what base64 costs on the wire, rounded up — the number that
// matters for ACP's input limit.
func encodedSize(n int) int { return (n + 2) / 3 * 4 }
