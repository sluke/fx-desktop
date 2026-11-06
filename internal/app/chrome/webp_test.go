package chrome

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"runtime"
	"testing"
)

// The two fixtures are the smallest lossless and lossy samples from
// golang.org/x/image's WebP test corpus (BSD-3-Clause), copied verbatim into
// testdata. x/image ships a WebP decoder but no encoder, so a real sample is
// the only way to test what the decoder actually allocates. Everything else
// here is built from those two byte strings by rewriting the RIFF container,
// which is how the four shapes WebP can take are covered without four
// third-party binaries.
const (
	losslessVP8LFile = "testdata/lossless-vp8l.webp" // 75×100, decodes to *image.NRGBA
	lossyVP8File     = "testdata/lossy-vp8.webp"     // 150×100, decodes to *image.YCbCr
)

type webpChunk struct {
	id   string
	data []byte
}

type webpSample struct {
	name  string
	data  []byte
	model color.Model
}

func readWebPFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func webpChunks(t *testing.T, data []byte) []webpChunk {
	t.Helper()
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatal("the fixture is not a WebP RIFF file")
	}
	var out []webpChunk
	for off := 12; off+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		start := off + 8
		if start+size > len(data) {
			t.Fatal("the fixture has a truncated chunk")
		}
		out = append(out, webpChunk{string(data[off : off+4]), data[start : start+size]})
		off = start + size + size%2
	}
	return out
}

func webpChunkWith(t *testing.T, data []byte, id string) []byte {
	t.Helper()
	for _, chunk := range webpChunks(t, data) {
		if chunk.id == id {
			return chunk.data
		}
	}
	t.Fatalf("the fixture has no %s chunk", id)
	return nil
}

// buildWebP writes a RIFF/WEBP container from chunk headers and payloads. Only
// the headers are new: the image data is the real decoder's own input.
func buildWebP(chunks ...webpChunk) []byte {
	body := []byte("WEBP")
	for _, chunk := range chunks {
		var head [8]byte
		copy(head[:4], chunk.id)
		binary.LittleEndian.PutUint32(head[4:], uint32(len(chunk.data)))
		body = append(body, head[:]...)
		body = append(body, chunk.data...)
		if len(chunk.data)%2 == 1 {
			body = append(body, 0) // RIFF chunks are word-aligned
		}
	}
	out := make([]byte, 8, 8+len(body))
	copy(out, "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}

// vp8lDimensions reads the 14+14 bit canvas from a VP8L payload. The first
// byte is the 0x2f signature, then width-1, height-1, alpha-used and version.
func vp8lDimensions(t *testing.T, data []byte) (int, int) {
	t.Helper()
	if len(data) < 5 || data[0] != 0x2f {
		t.Fatal("the VP8L payload has no header")
	}
	bits := binary.LittleEndian.Uint32(data[1:5])
	return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1
}

// vp8Dimensions reads a VP8 frame header: a three-byte frame tag, the 9d 01 2a
// start code, then two 14-bit dimensions.
func vp8Dimensions(t *testing.T, data []byte) (int, int) {
	t.Helper()
	if len(data) < 10 || !bytes.Equal(data[3:6], []byte{0x9d, 0x01, 0x2a}) {
		t.Fatal("the VP8 payload has no frame header")
	}
	return int(binary.LittleEndian.Uint16(data[6:8]) & 0x3fff),
		int(binary.LittleEndian.Uint16(data[8:10]) & 0x3fff)
}

// vp8xCanvas is the ten-byte VP8X payload. With the alpha flag clear, x/image's
// DecodeConfig reports YCbCr for the whole container even when the image chunk
// after it is lossless — the case S8 is about.
func vp8xCanvas(w, h int, flags byte) []byte {
	out := make([]byte, 10)
	out[0] = flags
	w--
	h--
	for i := 0; i < 3; i++ {
		out[4+i] = byte(w >> (8 * i))
		out[7+i] = byte(h >> (8 * i))
	}
	return out
}

// uncompressedAlpha is an ALPH chunk in the simplest legal form: one flag byte
// (compression 0, filter 0) followed by w*h opaque alpha samples.
func uncompressedAlpha(w, h int) []byte {
	out := make([]byte, 1+w*h)
	for i := 1; i < len(out); i++ {
		out[i] = 0xff
	}
	return out
}

// webpSamples covers every model the shipped WebP decoder can return:
// lossless NRGBA, lossy YCbCr, lossy-with-alpha NYCbCrA, and a lossless image
// inside a VP8X container. The last one is a real WebP shape (the x/image
// corpus ships one with an ICCP chunk) whose header lies about the model.
func webpSamples(t *testing.T) []webpSample {
	t.Helper()
	lossless := readWebPFixture(t, losslessVP8LFile)
	lossy := readWebPFixture(t, lossyVP8File)
	vp8l := webpChunkWith(t, lossless, "VP8L")
	vp8 := webpChunkWith(t, lossy, "VP8 ")
	lw, lh := vp8lDimensions(t, vp8l)
	yw, yh := vp8Dimensions(t, vp8)
	return []webpSample{
		{"lossless VP8L", lossless, color.NRGBAModel},
		{"lossless VP8L in a VP8X container",
			buildWebP(webpChunk{"VP8X", vp8xCanvas(lw, lh, 0)}, webpChunk{"VP8L", vp8l}),
			color.NRGBAModel},
		{"lossy VP8", lossy, color.YCbCrModel},
		{"lossy VP8 with alpha in a VP8X container",
			buildWebP(
				webpChunk{"VP8X", vp8xCanvas(yw, yh, 0x10)},
				webpChunk{"ALPH", uncompressedAlpha(yw, yh)},
				webpChunk{"VP8 ", vp8},
			),
			color.NYCbCrAModel},
	}
}

// S8: what DecodeConfig says about a WebP is not always what Decode builds.
// The estimate the window reserves must cover the buffer that is really
// allocated, for every container shape, not only for a bare VP8L or VP8 chunk.
func TestWebPHeaderEstimateCoversTheDecodedBuffer(t *testing.T) {
	for _, tc := range webpSamples(t) {
		t.Run(tc.name, func(t *testing.T) {
			info, err := InspectImage("picture.webp", tc.data, DefaultImageLimit)
			if err != nil {
				t.Fatalf("the header was refused: %v", err)
			}
			if info.MimeType != "image/webp" {
				t.Fatalf("the declared type is %q", info.MimeType)
			}
			img, format, err := image.Decode(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("the fixture is not readable by the decoder this app ships: %v", err)
			}
			if format != "webp" || img.ColorModel() != tc.model {
				t.Fatalf("decoded as %s / %v, want webp / %v", format, img.ColorModel(), tc.model)
			}
			buffer := pixelBufferBytes(img)
			if buffer == 0 {
				t.Fatalf("the test does not measure the buffer of %T", img)
			}
			if got := info.EstimatedDecodeBytes(); got < buffer {
				t.Fatalf("a %d-byte decoded buffer was reserved as %d bytes (%d per pixel) from the header",
					buffer, got, info.bytesPerPixel)
			}
			if got, want := info.bytesPerPixel, decodeBytesPerPixel(tc.model); got < want {
				t.Fatalf("the header charged %d bytes per pixel, the model needs %d", got, want)
			}
		})
	}
}

// A VP8X container without the alpha flag is where the header says YCbCr (3
// bytes per pixel) and the image inside says NRGBA (4). InspectImage has to
// read the container, not stop at the header's colour model.
func TestWebPContainerCannotUnderstateTheLosslessModel(t *testing.T) {
	var container *webpSample
	samples := webpSamples(t)
	for i := range samples {
		if samples[i].name == "lossless VP8L in a VP8X container" {
			container = &samples[i]
		}
	}
	if container == nil {
		t.Fatal("the container sample is missing")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(container.data))
	if err != nil || format != "webp" {
		t.Fatalf("the container cannot be inspected: %s %v", format, err)
	}
	// Record what the decoder's header claims. If a later x/image fixes this,
	// the test still has to hold: InspectImage must never charge less than the
	// lossless model, whatever the header says.
	t.Logf("DecodeConfig reports %v (%d bytes per pixel)", cfg.ColorModel, decodeBytesPerPixel(cfg.ColorModel))
	info, err := InspectImage("picture.webp", container.data, DefaultImageLimit)
	if err != nil {
		t.Fatal(err)
	}
	if want := decodeBytesPerPixel(color.NRGBAModel); info.bytesPerPixel < want {
		t.Fatalf("a lossless image in a VP8X container was charged %d bytes per pixel, want at least %d",
			info.bytesPerPixel, want)
	}
	if info.Width != 75 || info.Height != 100 {
		t.Fatalf("the canvas read as %d×%d", info.Width, info.Height)
	}
}

// The format-level assertion that was missing in S7: each WebP shape reaches
// the composer with its own declared type and a real preview, not just a
// header estimate that happens to look plausible.
func TestWebPAttachmentsAreDecodedUnderTheirOwnMimeType(t *testing.T) {
	for _, tc := range webpSamples(t) {
		t.Run(tc.name, func(t *testing.T) {
			attachment, err := NewImageAttachment("picture.webp", tc.data)
			if err != nil {
				t.Fatalf("a readable WebP was refused: %v", err)
			}
			if attachment.MimeType != "image/webp" || attachment.Preview == nil {
				t.Fatalf("the attachment is %q with preview=%v", attachment.MimeType, attachment.Preview != nil)
			}
			if attachment.Width <= 0 || attachment.Height <= 0 {
				t.Fatalf("the attachment has no dimensions: %+v", attachment)
			}
		})
	}
}

// S10/S11: a WebP's per-pixel charge has two numbers. bytesPerPixel is the
// image the decoder returns; peakBytesPerPixel bounds the transient buffers
// that are live at the same time. These are the ceilings read from the shipped
// decoder's source, not a measurement of one lucky sample: a lossless VP8L can
// hold two NRGBA-sized slices (color-indexing), and a compressed ALPH chunk is
// decoded into a whole NRGBA image before its alpha plane is extracted.
//
// The expected values are literals on purpose. Reading them from the
// production constants would let someone lower the ceiling and take the test
// down with it.
func TestWebPDecoderPeakAllowsForTransientBuffers(t *testing.T) {
	cases := []struct {
		name        string
		data        []byte
		wantBytes   int
		wantPeakMin int
	}{
		{"lossless VP8L", readWebPFixture(t, losslessVP8LFile), 4, 8},
		{"lossless VP8L in a VP8X container",
			func() []byte {
				lossless := readWebPFixture(t, losslessVP8LFile)
				vp8l := webpChunkWith(t, lossless, "VP8L")
				w, h := vp8lDimensions(t, vp8l)
				return buildWebP(webpChunk{"VP8X", vp8xCanvas(w, h, 0)}, webpChunk{"VP8L", vp8l})
			}(), 4, 8},
		{"lossy VP8", readWebPFixture(t, lossyVP8File), 3, 3},
		{"VP8 with uncompressed ALPH", webpSamples(t)[3].data, 4, 4},
		{"VP8 with compressed ALPH",
			readWebPFixture(t, "testdata/lossy-compressed-alpha.webp"), 4, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := InspectImage("picture.webp", tc.data, DefaultImageLimit)
			if err != nil {
				t.Fatalf("the header was refused: %v", err)
			}
			if info.bytesPerPixel < tc.wantBytes {
				t.Fatalf("the returned image was charged %d bytes per pixel, want at least %d",
					info.bytesPerPixel, tc.wantBytes)
			}
			if info.peakBytesPerPixel < tc.wantPeakMin {
				t.Fatalf("the decoder's peak was charged %d bytes per pixel, want at least %d",
					info.peakBytesPerPixel, tc.wantPeakMin)
			}
			pixels := int64(info.Width) * int64(info.Height)
			if got, want := int64(info.EstimatedDecodePeakBytes()), pixels*int64(tc.wantPeakMin); got < want {
				t.Fatalf("the window reserves %d bytes for a %d-pixel image, less than the %d/px decoder peak",
					got, pixels, tc.wantPeakMin)
			}
			if info.EstimatedDecodePeakBytes() <= info.EstimatedDecodeBytes() {
				t.Fatal("the peak is no larger than the pixel buffer alone")
			}
		})
	}
}

// A compressed ALPH chunk is not the same shape as an uncompressed one: the
// latter is read straight into the alpha plane, while the former is a whole
// VP8L image decoded on the way. The container walk has to tell them apart,
// or the 246 KB shortfall of a 400×301 sample comes back.
func TestWebPCompressedAlphaCostsMoreThanUncompressed(t *testing.T) {
	compressed, err := InspectImage("compressed.webp",
		readWebPFixture(t, "testdata/lossy-compressed-alpha.webp"), DefaultImageLimit)
	if err != nil {
		t.Fatal(err)
	}
	uncompressed, err := InspectImage("uncompressed.webp", webpSamples(t)[3].data, DefaultImageLimit)
	if err != nil {
		t.Fatal(err)
	}
	if compressed.bytesPerPixel != uncompressed.bytesPerPixel {
		t.Fatalf("both decode to NYCbCrA, but were charged %d and %d bytes per pixel",
			compressed.bytesPerPixel, uncompressed.bytesPerPixel)
	}
	if compressed.peakBytesPerPixel <= uncompressed.peakBytesPerPixel {
		t.Fatalf("a compressed alpha image was charged the same peak as an uncompressed one: %d",
			compressed.peakBytesPerPixel)
	}
	if compressed.peakBytesPerPixel < 9 {
		t.Fatalf("a compressed alpha image was charged %d bytes per pixel, want at least 9",
			compressed.peakBytesPerPixel)
	}
}

// The chunk walk is bounded. A container whose image chunk sits beyond the
// walk (or whose lengths are malformed) cannot be verified, so it must be
// charged the widest peak rather than the header's optimistic claim.
func TestWebPUnwalkableContainerIsChargedAtTheWidestPeak(t *testing.T) {
	vp8l := webpChunkWith(t, readWebPFixture(t, losslessVP8LFile), "VP8L")
	var deep []webpChunk
	for i := 0; i < 33; i++ {
		deep = append(deep, webpChunk{"JUNK", []byte{byte(i)}})
	}
	deep = append(deep, webpChunk{"VP8L", vp8l})

	truncated := append([]byte("RIFF\x00\x00\x00\x00WEBP"), []byte("VP8L")...)
	truncated = append(truncated, 0xff, 0xff, 0xff, 0xff) // a chunk longer than the file
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"image chunk past the walk", buildWebP(deep...)},
		{"truncated chunk length", truncated},
		{"no RIFF header", []byte("WEBPVP8L")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := webpDecodePlanFor(tc.data, color.YCbCrModel)
			t.Logf("plan: %+v", plan)
			const widest = 9
			if plan.peakBytesPerPixel < widest {
				t.Fatalf("an unverifiable container was charged %d bytes per pixel, want at least %d",
					plan.peakBytesPerPixel, widest)
			}
			if plan.bytesPerPixel < 4 {
				t.Fatalf("an unverifiable container was charged %d bytes per pixel for its returned image",
					plan.bytesPerPixel)
			}
		})
	}
}

// S7 continues into WebP: the estimate is a floor on what the decoder really
// allocates, including its working set. Small fixtures are dominated by that
// fixed overhead, which the same allowance as the other formats absorbs.
func TestWebPDecodeEstimateMeetsTheMeasuredAllocation(t *testing.T) {
	for _, tc := range webpSamples(t) {
		t.Run(tc.name, func(t *testing.T) {
			info, err := InspectImage("picture.webp", tc.data, DefaultImageLimit)
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
