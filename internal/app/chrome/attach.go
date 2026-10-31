package chrome

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"  // registered so Decode identifies GIFs
	_ "image/jpeg" // and JPEGs
	_ "image/png"  //  and PNGs
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // and WebPs
)

// Attachment is one image riding the next prompt.
//
// The bytes are exactly what fx will receive, so the chip and the wire cannot
// disagree; everything else is presentation. fx takes images as inline
// base64 with a declared mime type (measured against fx 0.0.14, §12.3.2), so
// an attachment is an image, the type it really is, and the name to call it.
type Attachment struct {
	// ID identifies the chip, so a click can remove this one image rather
	// than all of them.
	ID uint64
	// Name is what the chip calls it and what the transcript records: a
	// file's base name, or a description for pasted data.
	Name string
	// MimeType is derived from the image's own bytes, never from a file
	// name: a ".png" that is really a JPEG must be declared as a JPEG or fx
	// rejects the whole message.
	MimeType string
	Data     []byte
	// Preview is a small decoded copy for the chip. Nil when the bytes could
	// not be decoded at all, which is also why such an image is refused.
	Preview       *ui.Bitmap
	Width, Height int
}

// MaxImageBytes is the per-image limit the composer enforces.
//
// ACP's documentation calls it "5 MiB padded base64", but a probe against fx
// 0.0.14 accepted a valid 4.2 MB PNG (5.6 MB encoded) and finished the turn,
// so what binds is the decoded image, not its encoding. fx stays the
// authority: anything it refuses is shown in fx's own words (§12.3.2).
const MaxImageBytes = 5 << 20

// MaxPromptBytes bounds one session/prompt message, ACP's documented input
// limit. base64 inflates an attachment by a third, so several allowed images
// can still add up to a message fx will not read.
const MaxPromptBytes = 8 << 20

// previewPixels is the longest side of the decoded chip preview. The chip
// draws it at 26pt, so this covers a retina display while keeping a 12MP
// photo from becoming a 48MB bitmap.
const previewPixels = 96

// NewImageAttachment validates one image and prepares it for the composer.
// Nothing about the request is left to the caller: if this returns an
// attachment, fx has been measured to accept that shape and size.
func NewImageAttachment(name string, data []byte) (Attachment, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "image"
	}
	if len(data) == 0 {
		return Attachment{}, fmt.Errorf("%s is empty", name)
	}
	if len(data) > MaxImageBytes {
		return Attachment{}, fmt.Errorf("%s is %s; fx takes up to %s per image",
			name, humanSize(len(data)), humanSize(MaxImageBytes))
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Attachment{}, fmt.Errorf("%s is not an image fx can read (PNG, JPEG, GIF or WebP)", name)
	}
	mimeType := mimeForFormat(format)
	if mimeType == "" {
		// Decode only succeeds for the formats imported above, so this is
		// unreachable — but an unlabelled image must never be sent as a
		// guess, because fx rejects the whole message when the type is wrong.
		return Attachment{}, fmt.Errorf("%s is a %s image, which fx does not take", name, format)
	}
	bounds := img.Bounds()
	return Attachment{
		Name:     name,
		MimeType: mimeType,
		Data:     data,
		Preview:  thumbnail(img),
		Width:    bounds.Dx(),
		Height:   bounds.Dy(),
	}, nil
}

// mimeForFormat maps a decoder's name to the type fx accepts.
func mimeForFormat(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	}
	return ""
}

// thumbnail decodes a display-sized copy. A chip is 26pt wide; handing the
// renderer a full-size photo would cost a bitmap many thousand times larger
// than the pixels it shows.
func thumbnail(img image.Image) *ui.Bitmap {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	scale := math.Min(1, math.Min(float64(previewPixels)/float64(w), float64(previewPixels)/float64(h)))
	tw, th := int(float64(w)*scale+0.5), int(float64(h)*scale+0.5)
	if tw < 1 {
		tw = 1
	}
	if th < 1 {
		th = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, tw, th))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return ui.NewBitmap(dst)
}

// SizeLabel is the chip's answer to "how big is this", in the units a person
// reads rather than in bytes.
func (a Attachment) SizeLabel() string { return humanSize(len(a.Data)) }

// Dimensions returns "1024×768" when the image could be decoded, and "" when
// its size is not known — an unknown is left unsaid.
func (a Attachment) Dimensions() string {
	if a.Width <= 0 || a.Height <= 0 {
		return ""
	}
	return fmt.Sprintf("%d×%d", a.Width, a.Height)
}

// HumanSize is the human-readable form of a byte count, so the shell and the
// app describe the same limit in the same words.
func HumanSize(n int) string { return humanSize(n) }

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// attachmentsRow draws the images waiting to be sent, inside the composer so
// they read as part of the message being written rather than as a panel. The
// row scrolls: how many images a message may carry is fx's business, not a
// number this view gets to invent.
func attachmentsRow(c *ui.Context, state *ConversationState, h ConversationHost, p Palette) {
	if len(state.Attachments) == 0 {
		return
	}
	ui.ScrollHorizontal(c).FillWidth().Shrink(0).Gap(6).Children(func() {
		for _, a := range state.Attachments {
			attachmentChip(c, a, h.RemoveAttachment, p)
		}
	})
}

// attachmentChip is one image: its picture, what it is, and a way to take it
// back before it is sent.
func attachmentChip(c *ui.Context, a Attachment, onRemove func(id uint64), p Palette) {
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(7).Padding(4, 6).Radius(4).
		Background(p.SurfaceHi).BorderWidth(1).BorderColor(p.Hairline).
		Tooltip(a.Name + " · " + a.MimeType).
		Children(func() {
			thumb := ui.Box(c).Width(26).Height(26).Radius(5).Background(p.Window).Clip()
			if a.Preview != nil {
				thumb.Children(func() { ui.Image(c, a.Preview).Fit(ui.Cover).Fill() })
			}
			ui.Column(c).Gap(1).Children(func() {
				mono(ui.Text(c, shortName(a.Name))).FontSize(10).TextColor(p.InkStrong).SingleLine()
				detail := a.SizeLabel()
				if dims := a.Dimensions(); dims != "" {
					detail = dims + " · " + detail
				}
				mono(ui.Text(c, detail)).FontSize(9).TextColor(p.InkMuted).SingleLine()
			})
			if onRemove != nil {
				ui.Button(c, "✕").Label("Remove image "+a.Name).FontSize(10).Padding(4, 6).BorderWidth(0).
					Background(p.SurfaceHi).TextColor(p.InkMuted).
					OnClick(func() { onRemove(a.ID) })
			}
		})
}

// shortName keeps a file name from widening the chip; the full name is still
// what the wire, the tooltip and the transcript carry.
func shortName(name string) string {
	name = strings.TrimSpace(name)
	const limit = 18
	if len(name) <= limit {
		return name
	}
	// Trim the middle of the stem, keeping the extension readable.
	dot := strings.LastIndexByte(name, '.')
	if dot > 0 && len(name)-dot <= 6 {
		if keep := limit - (len(name) - dot); keep > 1 {
			return name[:keep] + "…" + name[dot:]
		}
	}
	return name[:limit] + "…"
}
