package app

import (
	"fmt"
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

// attachImageFiles turns dropped or chosen files into images on the next
// message. Everything that fails says why: a drop that quietly did nothing
// looks like the app is broken.
func (c *Coordinator) attachImageFiles(s *Session, paths []string) {
	if s == nil || s.chat == nil || len(paths) == 0 {
		return
	}
	var refused []string
	for _, path := range paths {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			refused = append(refused, filepath.Base(path)+" is not a file")
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			refused = append(refused, "Could not read "+filepath.Base(path))
			continue
		}
		if err := c.attachImage(s, filepath.Base(path), data); err != nil {
			refused = append(refused, err.Error())
		}
	}
	// Report the first refusal: one sentence the user can act on beats a
	// paragraph about every file in the drop.
	if len(refused) > 0 {
		c.setAttachError(s, strings.Join(refused, " · "))
	}
}

// attachImage validates one image and puts it in the composer. It returns the
// refusal as well as showing it, so a drop of several files can collect what
// went wrong and report it once.
func (c *Coordinator) attachImage(s *Session, name string, data []byte) error {
	if s == nil || s.chat == nil {
		return nil
	}
	attachment, err := chrome.NewImageAttachment(name, data)
	if err != nil {
		c.setAttachError(s, err.Error())
		return err
	}
	// The whole message has to fit ACP's input limit, and base64 inflates an
	// image by a third: several allowed images can still add up to a prompt
	// fx will not read. Checking here means the answer arrives while the
	// user is still composing, not after they hit send.
	encoded := encodedSize(len(attachment.Data))
	for _, existing := range s.chatUI.Attachments {
		encoded += encodedSize(len(existing.Data))
	}
	if encoded+len(s.chatUI.Draft) > chrome.MaxPromptBytes {
		err := fmt.Errorf("that would make the message over %s, which is all fx reads; remove an image first",
			chrome.HumanSize(chrome.MaxPromptBytes))
		c.setAttachError(s, err.Error())
		return err
	}
	s.attachmentSeq++
	attachment.ID = s.attachmentSeq
	s.chatUI.Attachments = append(s.chatUI.Attachments, attachment)
	s.chatUI.AttachError = ""
	s.invalidate()
	return nil
}

// removeAttachment takes one image back out of the composer.
func (c *Coordinator) removeAttachment(s *Session, id uint64) {
	if s == nil {
		return
	}
	kept := s.chatUI.Attachments[:0]
	for _, a := range s.chatUI.Attachments {
		if a.ID != id {
			kept = append(kept, a)
		}
	}
	s.chatUI.Attachments = kept
	s.chatUI.AttachError = ""
	s.invalidate()
}

func (c *Coordinator) setAttachError(s *Session, message string) {
	if s == nil {
		return
	}
	s.chatUI.AttachError = message
	s.invalidate()
}

// pasteImage puts the clipboard's image into the composer. Clipboard access
// is main-thread work that may pump the loop, so it is queued out of the
// native callback that asked for it — the same reason pickFolder is.
func (c *Coordinator) pasteImage(s *Session) {
	if s == nil || s.chat == nil {
		return
	}
	mygo.App.WhenReady(func() { c.pasteImageOnMain(s) })
}

func (c *Coordinator) pasteImageOnMain(s *Session) {
	if s == nil || s.chat == nil {
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
	if err := c.attachImage(s, "Pasted image.png", png); err != nil {
		c.setAttachError(s, err.Error())
	}
}

// pickImage asks for image files and attaches what comes back.
func (c *Coordinator) pickImage(s *Session) {
	if s == nil || s.chat == nil {
		return
	}
	parent := s.winSnapshot()
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
		c.attachImageFiles(s, paths)
	})
}

// encodedSize is what base64 costs on the wire, rounded up — the number that
// matters for ACP's input limit.
func encodedSize(n int) int { return (n + 2) / 3 * 4 }
