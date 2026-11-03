package chrome

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"runtime"
	"strings"
	"testing"
)

// headerOnlyPNG builds a PNG that declares w×h and carries no usable pixel
// data. A decoder can describe it from the header alone; it cannot decode it.
// It is how these tests tell "refused before decoding" from "decoded and then
// refused".
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
	ihdr = append(ihdr, 8, 6, 0, 0, 0) // 8-bit RGBA
	out := append([]byte("\x89PNG\r\n\x1a\n"), chunk("IHDR", ihdr)...)
	out = append(out, chunk("IDAT", []byte{0x78, 0x9c, 0x00})...) // not a valid stream
	return append(out, chunk("IEND", nil)...)
}

// R5: the canvas an image declares is refused before the pixels are allocated.
// The image below cannot be decoded at all, so a decode-first implementation
// would report a decode error — and would already have allocated its buffer.
func TestImageLimitsAreCheckedFromTheHeaderBeforeDecode(t *testing.T) {
	data := headerOnlyPNG(t, 400, 300)
	small := ImageLimit{MaxSide: 64, MaxPixels: 64 * 64}

	info, err := InspectImage("big.png", data, small)
	if err == nil {
		t.Fatalf("a %dx%d header was accepted under a 64-pixel limit: %+v", 400, 300, info)
	}
	if !strings.Contains(err.Error(), "64") {
		t.Fatalf("the refusal does not name the limit it hit: %v", err)
	}

	// The same bytes are readable when the limit admits them, which proves the
	// header test above was not just a decode failure in disguise.
	roomy := ImageLimit{MaxSide: 400, MaxPixels: 400 * 300}
	info, err = InspectImage("big.png", data, roomy)
	if err != nil {
		t.Fatalf("a readable header was refused: %v", err)
	}
	if info.Width != 400 || info.Height != 300 || info.MimeType != "image/png" {
		t.Fatalf("header read as %+v", info)
	}
	if _, err := NewImageAttachmentWithin("big.png", data, roomy); err == nil {
		t.Fatal("an image with no pixel data was accepted")
	}

	// The default bound refuses a side no chat attachment needs, and does it
	// without decoding: 14000×2 is 28 000 pixels, so nothing is risked even if
	// this test regresses.
	if _, err := NewImageAttachment("wide.png", headerOnlyPNG(t, 14000, 2)); err == nil {
		t.Fatal("the default side limit accepted a 14000-pixel image")
	}
}

// The pixel count is a second, independent bound: a long thin canvas passes the
// side limit and still claims more pixels than the window will hold.
func TestImageTotalPixelsIsChecked(t *testing.T) {
	data := headerOnlyPNG(t, 100, 100)
	if _, err := InspectImage("flat.png", data, ImageLimit{MaxSide: 200, MaxPixels: 5000}); err == nil {
		t.Fatal("10 000 pixels passed a 5 000-pixel limit")
	}
	if _, err := InspectImage("flat.png", data, ImageLimit{MaxSide: 200, MaxPixels: 10000}); err != nil {
		t.Fatalf("an image exactly at the limit was refused: %v", err)
	}
}

// EstimatedDecodeBytes is what the budget reserves, so it must be the RGBA size
// the decoder actually allocates.
func TestEstimatedDecodeBytesIsTheRGBASize(t *testing.T) {
	data := pngImage(t, 40, 30)
	info, err := InspectImage("shot.png", data, DefaultImageLimit)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.EstimatedDecodeBytes(), 40*30*4; got != want {
		t.Fatalf("estimate = %d, want %d", got, want)
	}
	// The comment on a maximum is only true if it is above a real message.
	if _, err := NewImageAttachment("shot.png", data); err != nil {
		t.Fatalf("a 40×30 image was refused by the default limits: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG")) {
		t.Fatal("the fixture is not a PNG")
	}
}

// png16Image builds a real 16-bit PNG: the format whose decoded buffer is
// twice what a "four bytes per pixel" constant predicts (S2).
func png16Image(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA64(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.SetNRGBA64(x, y, color.NRGBA64{R: uint16(x * 512), G: uint16(y * 512), B: 0x8080, A: 0xFFFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegImage(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 4), B: 0x40, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// pixelBufferBytes is the pixel slice a decoded image actually holds. It is
// what the reservation has to cover; a type this helper does not know returns
// zero, which would hide a shortfall — so every format these tests decode is
// listed.
func pixelBufferBytes(img image.Image) int {
	switch m := img.(type) {
	case *image.RGBA:
		return len(m.Pix)
	case *image.NRGBA:
		return len(m.Pix)
	case *image.RGBA64:
		return len(m.Pix)
	case *image.NRGBA64:
		return len(m.Pix)
	case *image.Gray:
		return len(m.Pix)
	case *image.Gray16:
		return len(m.Pix)
	case *image.Paletted:
		return len(m.Pix)
	case *image.CMYK:
		return len(m.Pix)
	case *image.YCbCr:
		return len(m.Y) + len(m.Cb) + len(m.Cr)
	}
	return 0
}

// S2: the estimate is what the window's budget reserves, so it must cover the
// buffer the decoder really allocates — for every format and depth, not only
// 8-bit RGBA. A 16-bit PNG used to be estimated at half its real cost.
func TestEstimatedDecodeBytesCoversTheDecodedBuffer(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"8-bit PNG", pngImage(t, 32, 32)},
		{"16-bit PNG", png16Image(t, 32, 32)},
		{"JPEG", jpegImage(t, 32, 32)},
		{"GIF", gifImage(t)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := InspectImage("shot", tc.data, DefaultImageLimit)
			if err != nil {
				t.Fatal(err)
			}
			img, _, err := image.Decode(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			allocated := pixelBufferBytes(img)
			if allocated == 0 {
				t.Fatalf("%s decoded to an unmeasured type %T", tc.name, img)
			}
			if got := info.EstimatedDecodeBytes(); got < allocated {
				t.Fatalf("estimate %d is below the %d-byte buffer the decoder allocated", got, allocated)
			}
			pixels := img.Bounds().Dx() * img.Bounds().Dy()
			if got, want := info.EstimatedDecodeBytes(), pixels*info.bytesPerPixel; got != want {
				t.Fatalf("estimate = %d, want %d pixels × %d bytes", got, pixels, info.bytesPerPixel)
			}
		})
	}
}

// The 16-bit case is the one that regressed: it must be eight bytes per pixel,
// not four, and the difference has to be visible in the number the budget sees.
func TestSixteenBitPNGCostsTwiceAnEightBitOne(t *testing.T) {
	eight, err := InspectImage("eight.png", pngImage(t, 16, 16), DefaultImageLimit)
	if err != nil {
		t.Fatal(err)
	}
	sixteen, err := InspectImage("sixteen.png", png16Image(t, 16, 16), DefaultImageLimit)
	if err != nil {
		t.Fatal(err)
	}
	if eight.EstimatedDecodeBytes() != 16*16*4 {
		t.Fatalf("8-bit estimate = %d", eight.EstimatedDecodeBytes())
	}
	if got, want := sixteen.EstimatedDecodeBytes(), 16*16*8; got != want {
		t.Fatalf("16-bit estimate = %d, want %d", got, want)
	}
}

// S7: the table is per colour model, not per format, because one format can
// decode to several (PNG to RGBA or RGBA64, JPEG to YCbCr, WebP to YCbCr,
// NYCbCrA or NRGBA). Every model the imported decoders can return is listed,
// and an unlisted one is deliberately treated as the widest common form: a
// decoder added later must not be able to make the budget under-reserve.
func TestBytesPerPixelCoversEveryDecodedModel(t *testing.T) {
	cases := []struct {
		name    string
		model   color.Model
		want    int
		comment string
	}{
		{"NRGBA", color.NRGBAModel, 4, "PNG 8-bit, WebP lossless"},
		{"RGBA", color.RGBAModel, 4, "GIF's first frame, PNG"},
		{"NRGBA64", color.NRGBA64Model, 8, "PNG 16-bit"},
		{"RGBA64", color.RGBA64Model, 8, "PNG 16-bit"},
		{"CMYK", color.CMYKModel, 4, "JPEG CMYK"},
		{"YCbCr", color.YCbCrModel, 3, "JPEG, WebP lossy"},
		{"YCbCrA", color.NYCbCrAModel, 4, "WebP lossy with alpha"},
		{"Gray", color.GrayModel, 1, "PNG grayscale"},
		{"Gray16", color.Gray16Model, 2, "PNG 16-bit grayscale"},
		{"Alpha", color.AlphaModel, 1, "an alpha-only buffer"},
		{"Paletted", color.Palette{color.Black, color.White}, 1, "GIF, PNG 1-bit"},
		// A model no decoder in this build returns. It must not be treated as
		// "no buffer": the estimate is a floor, so an unknown model costs the
		// widest common form.
		{"unknown", color.ModelFunc(func(color.Color) color.Color { return color.NRGBA{} }), 4, "a decoder added later"},
	}
	for _, tc := range cases {
		if got := decodeBytesPerPixel(tc.model); got != tc.want {
			t.Errorf("%s (%s): %d bytes per pixel, want %d", tc.name, tc.comment, got, tc.want)
		}
	}
}

// gifSized builds a GIF large enough that its pixel buffer, not its fixed
// decoder overhead, is what the allocation measurement sees.
func gifSized(t *testing.T, w, h int) []byte {
	t.Helper()
	palette := make(color.Palette, 0, 256)
	for i := 0; i < 256; i++ {
		palette = append(palette, color.RGBA{R: uint8(i), G: uint8(255 - i), B: uint8(i / 2), A: 0xFF})
	}
	img := image.NewPaletted(image.Rect(0, 0, w, h), palette)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetColorIndex(x, y, uint8((x+y)%256))
		}
	}
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// S7: the estimate is what the window's budget reserves, so it has to be true
// of a decoder's real work, not only of the arithmetic on the header. This
// decodes each format and compares what was actually allocated with what the
// header promised. A decoder's fixed working set (row buffers, a palette) is
// allowed on top of the estimate; what may not happen is the pixel buffer being
// under-counted, which is the mistake that let a 16-bit PNG reserve half its
// cost (S2).
func TestDecodeEstimateMeetsTheMeasuredAllocation(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"8-bit PNG", pngImage(t, 1000, 800)},
		{"16-bit PNG", png16Image(t, 800, 600)},
		{"JPEG", jpegImage(t, 1000, 800)},
		{"GIF", gifSized(t, 300, 300)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := InspectImage("shot", tc.data, DefaultImageLimit)
			if err != nil {
				t.Fatal(err)
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			img, _, err := image.Decode(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatal(err)
			}
			runtime.ReadMemStats(&after)
			allocated := int(after.TotalAlloc - before.TotalAlloc)
			estimated := info.EstimatedDecodeBytes()
			t.Logf("estimate %d, allocated %d, buffer %d", estimated, allocated, pixelBufferBytes(img))
			if limit := estimated + estimated/10 + 64<<10; allocated > limit {
				t.Fatalf("decoding allocated %d bytes, and the estimate the budget reserves is %d", allocated, estimated)
			}
			runtime.KeepAlive(img)
		})
	}
}
