package chrome

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // registered so Decode identifies GIFs
	_ "image/jpeg" // and JPEGs
	_ "image/png"  //  and PNGs
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // and WebPs

	"fx-desktop/internal/workspace"
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

// ImageLimit bounds what one compressed image may expand to. Compressed bytes
// say nothing about the canvas inside them — a few kilobytes of PNG can declare
// millions of pixels — so the limits are read from the header before any pixel
// buffer is allocated (R5).
type ImageLimit struct {
	// MaxSide is the longest side, in pixels, a single dimension may have.
	MaxSide int
	// MaxPixels is the total number of pixels the image may declare.
	MaxPixels int
}

// DefaultImageLimit is the product's bound: 12 000 pixels on a side and 24
// megapixels in total. Both are far above a screenshot or a phone photo a
// person sends to a model, and far below what a decompression bomb claims.
var DefaultImageLimit = ImageLimit{MaxSide: 12000, MaxPixels: 24_000_000}

// ImageInfo is what an encoded image says about itself without being decoded.
type ImageInfo struct {
	MimeType      string
	Width, Height int
	// bytesPerPixel is the buffer Go's decoder allocates per pixel for this
	// image's own colour model, taken from the header. It is not a constant:
	// a 16-bit PNG decodes to eight bytes per pixel, not four (S2).
	bytesPerPixel int
	// peakBytesPerPixel is only set for WebP, whose decoders can hold a
	// second image-sized buffer while they work (S10/S11). It is an upper
	// bound on all the per-pixel buffers that are live at the same moment,
	// including bytesPerPixel. Zero for the other formats, whose decoders'
	// working set is bounded by the fixed allowance in
	// EstimatedDecodePeakBytes instead.
	peakBytesPerPixel int
}

// EstimatedDecodeBytes is the pixel buffer the preview is scaled from, at the
// image's own depth. It is the floor of what the window reserves; the number
// actually handed to the budget is EstimatedDecodePeakBytes below.
//
// It is a reservation, not a measurement: a decoder's own working set and the
// thumbnail add to it, and the budget it feeds is logical bytes, not resident
// memory. What it must never do is understate the buffer itself — that is the
// one part that is knowable from the header.
func (i ImageInfo) EstimatedDecodeBytes() int {
	const maxInt = int(^uint(0) >> 1)
	bytes := int64(i.Width) * int64(i.Height) * int64(i.bytesPerPixel)
	if bytes <= 0 || bytes > int64(maxInt) {
		// Overflow or no size: reserve everything rather than too little.
		return maxInt
	}
	return int(bytes)
}

// EstimatedPreviewBytes bounds the display bitmap and its scaler's temporary
// buffers, both alive while the decoded image is still in scope. Measured at
// ~33 KB for a 96×77 preview with ApproxBiLinear (the destination RGBA is
// 29 KB); two full preview-sized bitmaps leave room for the renderer to keep
// a copy. It is a constant: the preview is capped at previewPixels on a side
// whatever the source image claims, and the scaler no longer allocates a
// source-sized intermediate (T25).
func EstimatedPreviewBytes() int { return 2 * previewPixels * previewPixels * 4 }

// EstimatedDecodePeakBytes is what the window must reserve for one image.
// While NewImageAttachmentWithin runs, the decoded pixel buffer, the decoder's
// own working set and the preview bitmap are all live at the same moment. The
// working set is the measured ~1.6–2% for PNG with a 64 KB floor for small
// images (GIF has a fixed ~33 KB), so peak/16 plus that floor is a bound, not
// a guess (T25).
//
// WebP is the exception that needs more than that fixed allowance: its
// decoders can allocate a whole second image-sized buffer — lossless
// color-indexing, or the NRGBA alpha image a compressed ALPH chunk is decoded
// into. peakBytesPerPixel is the bound on those simultaneously live
// per-pixel buffers, read from the container (S10/S11). Reserving only
// EstimatedDecodeBytes leaves that extra buffer outside the window's budget.
func (i ImageInfo) EstimatedDecodePeakBytes() int {
	const maxInt = int(^uint(0) >> 1)
	base := i.EstimatedDecodeBytes()
	peak := base
	if i.peakBytesPerPixel > i.bytesPerPixel {
		pixels := int64(i.Width) * int64(i.Height)
		charged := pixels * int64(i.peakBytesPerPixel)
		if charged > int64(maxInt) {
			return maxInt
		}
		if int(charged) > peak {
			peak = int(charged)
		}
	}
	overhead := peak / 16
	if overhead < 64<<10 {
		overhead = 64 << 10
	}
	extra := overhead + EstimatedPreviewBytes()
	if peak > maxInt-extra {
		return maxInt
	}
	return peak + extra
}

// decodeBytesPerPixel is the buffer these decoders allocate for one pixel of
// this model. The values are the slice sizes, not a guess: image.NRGBA64 and
// image.RGBA64 store eight bytes, image.YCbCr keeps three planes of one byte,
// image.NYCbCrA adds an alpha plane (WebP with alpha decodes to it), and
// image.Paletted one index.
//
// The table is per model rather than per format, because one format can decode
// to several: PNG to RGBA, NRGBA, RGBA64 or Gray, JPEG to YCbCr or CMYK, WebP to
// YCbCr, NYCbCrA or NRGBA. A model that is not listed costs the widest common
// form — an estimate that is too high wastes a reservation, one that is too low
// defeats the budget.
func decodeBytesPerPixel(m color.Model) int {
	switch m {
	case color.NRGBA64Model, color.RGBA64Model:
		return 8
	case color.NRGBAModel, color.RGBAModel, color.CMYKModel:
		return 4
	case color.NYCbCrAModel:
		// Three planes plus alpha, the shape WebP's alpha channel decodes to.
		return 4
	case color.YCbCrModel:
		return 3
	case color.Gray16Model:
		return 2
	case color.GrayModel, color.AlphaModel:
		return 1
	}
	if _, ok := m.(color.Palette); ok {
		// GIF decodes to a paletted image: one index per pixel.
		return 1
	}
	// An unknown model from a decoder added later: assume the widest common
	// form rather than under-reserving.
	return 4
}

// Per-pixel peaks of the WebP decoders shipped in x/image@v0.46.0, read from
// the decoder source rather than guessed from a measurement:
//
//   - VP8L (lossless): decodePix allocates the final NRGBA pix slice with a
//     capacity of 4 bytes/pixel, and inverseColorIndexing allocates another
//     full 4 bytes/pixel dst before returning it (vp8l/transform.go:247).
//     Both are live at once, so 8 is the ceiling.
//   - VP8 + a compressed ALPH chunk: readAlpha decodes the alpha plane into
//     an entire NRGBA image (4/px), extracts a separate []byte alpha plane
//     (1/px), and only then decodes the YCbCr frame (3/px). The alpha image
//     may itself use color-indexing (a second 4/px), so the alpha stage peaks
//     at 9/px; the final NYCbCrA is 4/px (webp/decode.go:199-246).
//
// Uncompressed ALPH has no intermediate NRGBA, so it costs only the final
// NYCbCrA's four bytes per pixel.
const (
	webpLosslessPeakBytesPerPixel        = 8
	webpCompressedAlphaPeakBytesPerPixel = 9
	webpWidestPeakBytesPerPixel          = 9
)

// webpDecodePlan is charged per pixel for a WebP: bytesPerPixel is the image
// the decoder returns, peakBytesPerPixel is every per-pixel buffer that can be
// live at the same moment on the way there.
type webpDecodePlan struct {
	bytesPerPixel     int
	peakBytesPerPixel int
}

// webpDecodePlanFor finds the image chunk inside a WebP container.
// x/image/webp's DecodeConfig returns as soon as it sees a VP8X header, and
// reports YCbCr (or NYCbCrA when the alpha flag is set) from that header
// alone. A VP8X container can instead hold a lossless VP8L image, which
// decodes to *image.NRGBA: four bytes per pixel where the header advertised
// three (S8). The same walk also finds the ALPH chunk that decides whether
// decoding holds a second full-size image in memory (S11).
//
// Only chunk headers are read, never pixels, and the walk stops after
// maxChunks so a hostile container cannot turn a header check into a scan of
// the whole file. A container that cannot be walked is charged the widest
// peak: over-reserving is safe, under-reserving is what breaks the budget.
func webpDecodePlanFor(data []byte, reported color.Model) webpDecodePlan {
	const maxChunks = 32
	reportedBytes := decodeBytesPerPixel(reported)
	losslessBytes := decodeBytesPerPixel(color.NRGBAModel)
	widest := webpDecodePlan{
		bytesPerPixel:     max(reportedBytes, losslessBytes),
		peakBytesPerPixel: webpWidestPeakBytesPerPixel,
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return widest
	}
	alphaPresent, alphaCompressed := false, false
	for off, i := 12, 0; i < maxChunks && off+8 <= len(data); i++ {
		id := string(data[off : off+4])
		size := int64(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		off += 8
		if size > int64(len(data))-int64(off) {
			break
		}
		payload := data[off : off+int(size)]
		switch id {
		case "ALPH":
			alphaPresent = true
			// The first byte's low two bits are the compression method; 1
			// means the alpha plane is a whole VP8L image (decode.go:202).
			if len(payload) > 0 && payload[0]&0x03 == 1 {
				alphaCompressed = true
			}
		case "VP8L":
			return webpDecodePlan{losslessBytes, webpLosslessPeakBytesPerPixel}
		case "VP8 ":
			bytes := reportedBytes
			if alphaPresent && bytes < losslessBytes {
				// YCbCr plus a separate alpha plane is NYCbCrA, four bytes.
				bytes = losslessBytes
			}
			peak := bytes
			if alphaCompressed && peak < webpCompressedAlphaPeakBytesPerPixel {
				peak = webpCompressedAlphaPeakBytesPerPixel
			}
			return webpDecodePlan{bytes, peak}
		}
		off += int(size) + int(size&1) // RIFF chunks are word-aligned
	}
	return widest
}

// InspectImage reads the header of an encoded image and refuses it if its
// declared canvas is beyond limit. It allocates no pixel buffer, so it is the
// check that has to come first: decoding is where the memory goes.
func InspectImage(name string, data []byte, limit ImageLimit) (ImageInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "image"
	}
	if len(data) == 0 {
		return ImageInfo{}, fmt.Errorf("%s is empty", name)
	}
	if len(data) > MaxImageBytes {
		return ImageInfo{}, fmt.Errorf("%s is %s; fx takes up to %s per image",
			name, humanSize(len(data)), humanSize(MaxImageBytes))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return ImageInfo{}, fmt.Errorf("%s is not an image fx can read (PNG, JPEG, GIF or WebP)", name)
	}
	mimeType := mimeForFormat(format)
	if mimeType == "" {
		// DecodeConfig only succeeds for the formats imported above, so this
		// is unreachable — but an unlabelled image must never be sent as a
		// guess, because fx rejects the whole message when the type is wrong.
		return ImageInfo{}, fmt.Errorf("%s is a %s image, which fx does not take", name, format)
	}
	w, h := cfg.Width, cfg.Height
	if w <= 0 || h <= 0 {
		return ImageInfo{}, fmt.Errorf("%s has no picture in it", name)
	}
	if limit.MaxSide > 0 && (w > limit.MaxSide || h > limit.MaxSide) {
		return ImageInfo{}, fmt.Errorf("%s is %d×%d; this app opens images up to %d pixels on a side",
			name, w, h, limit.MaxSide)
	}
	// Widen before multiplying: a decoder's own header may declare a canvas
	// whose product overflows a 32-bit int even though both sides fit in one.
	if limit.MaxPixels > 0 && int64(w)*int64(h) > int64(limit.MaxPixels) {
		return ImageInfo{}, fmt.Errorf("%s is %d×%d, more than the %d megapixels this app opens at once",
			name, w, h, limit.MaxPixels/1_000_000)
	}
	bytesPerPixel := decodeBytesPerPixel(cfg.ColorModel)
	peakBytesPerPixel := 0
	if format == "webp" {
		plan := webpDecodePlanFor(data, cfg.ColorModel)
		bytesPerPixel, peakBytesPerPixel = plan.bytesPerPixel, plan.peakBytesPerPixel
	}
	return ImageInfo{
		MimeType:          mimeType,
		Width:             w,
		Height:            h,
		bytesPerPixel:     bytesPerPixel,
		peakBytesPerPixel: peakBytesPerPixel,
	}, nil
}

// NewImageAttachment validates one image and prepares it for the composer.
// Nothing about the request is left to the caller: if this returns an
// attachment, fx has been measured to accept that shape and size.
func NewImageAttachment(name string, data []byte) (Attachment, error) {
	return NewImageAttachmentWithin(name, data, DefaultImageLimit)
}

// NewImageAttachmentWithin is NewImageAttachment under limits the caller
// chooses, so a test can prove the refusal with a budget it can afford.
func NewImageAttachmentWithin(name string, data []byte, limit ImageLimit) (Attachment, error) {
	info, err := InspectImage(name, data, limit)
	if err != nil {
		return Attachment{}, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Attachment{}, fmt.Errorf("%s is not an image fx can read (PNG, JPEG, GIF or WebP)", name)
	}
	bounds := img.Bounds()
	return Attachment{
		Name:     name,
		MimeType: info.MimeType,
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
//
// The interpolator matters as much as the destination size: the higher-order
// kernels (BiLinear, CatmullRom) build an intermediate buffer the size of the
// whole source image before scaling, measured at 2.6 MB for a 1000×800 photo
// and 1.0 MB even for a 300×300 GIF. ApproxBiLinear scales from the source
// directly into the 96-pixel destination, about 33 KB regardless of the
// source, which is also what keeps the window's peak estimate honest (T25).
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
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
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
func HumanSize(n int) string { return workspace.HumanSize(n) }

func humanSize(n int) string { return workspace.HumanSize(n) }

// attachmentsRow draws the images waiting to be sent, inside the composer so
// they read as part of the message being written rather than as a panel. The
// row scrolls: how many images a message may carry is fx's business, not a
// number this view gets to invent.
func attachmentsRow(c *ui.Context, state *ConversationState, h ConversationHost, p Palette) {
	if len(state.Attachments) == 0 {
		return
	}
	ui.ScrollHorizontal(c).FillWidth().Shrink(0).Gap(Space3).Children(func() {
		for _, a := range state.Attachments {
			attachmentChip(c, a, h.RemoveAttachment, p)
		}
	})
}

// attachmentChip is one image: its picture, what it is, and a way to take it
// back before it is sent.
func attachmentChip(c *ui.Context, a Attachment, onRemove func(id uint64), p Palette) {
	ui.Row(c).Shrink(0).AlignItems(ui.Center).Gap(Space3).Padding(Space2, Space3).Radius(RadiusChip).
		Background(p.SurfaceHi).BorderWidth(1).BorderColor(p.Hairline).
		Tooltip(a.Name + " · " + a.MimeType).
		Children(func() {
			thumb := ui.Box(c).Width(26).Height(26).Radius(RadiusControl).Background(p.Window).Clip()
			if a.Preview != nil {
				thumb.Children(func() { ui.Image(c, a.Preview).Fit(ui.Cover).Fill() })
			}
			ui.Column(c).Gap(Space0).Children(func() {
				mono(ui.Text(c, shortName(a.Name))).FontSize(TypeLabel).TextColor(p.InkStrong).SingleLine()
				detail := a.SizeLabel()
				if dims := a.Dimensions(); dims != "" {
					detail = dims + " · " + detail
				}
				mono(ui.Text(c, detail)).FontSize(TypeBadge).TextColor(p.InkMuted).SingleLine()
			})
			if onRemove != nil {
				ui.Button(c, "✕").Label("Remove image "+a.Name).FontSize(TypeLabel).Padding(Space2, Space3).BorderWidth(0).
					Background(p.SurfaceHi).TextColor(p.InkMuted).
					OnClick(func() { onRemove(a.ID) })
			}
		})
}

// shortName keeps a file name from widening the chip; the full name is still
// what the wire, the tooltip and the transcript carry. The limit counts
// characters, not bytes: slicing a Chinese name or an emoji by bytes leaves
// half a rune in the chip (T33).
func shortName(name string) string {
	name = strings.TrimSpace(name)
	const limit = 18
	runes := []rune(name)
	if len(runes) <= limit {
		return name
	}
	// Trim the middle of the stem, keeping the extension readable.
	dot := -1
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] == '.' {
			dot = i
			break
		}
	}
	if dot > 0 && len(runes)-dot <= 6 {
		if keep := limit - (len(runes) - dot); keep > 1 {
			return string(runes[:keep]) + "…" + string(runes[dot:])
		}
	}
	return string(runes[:limit]) + "…"
}
