package workspace

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func BenchmarkCompareTextsMillionShortLines(b *testing.B) {
	const lines = 1_000_000
	var text strings.Builder
	text.Grow(lines * 2)
	for i := 0; i < lines; i++ {
		text.WriteString("x\n")
	}
	in := text.String()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := CompareTexts("", in)
		if len(out.Lines) > MaxDiffLines {
			b.Fatalf("len=%d", len(out.Lines))
		}
	}
}

func TestCompareTextsKeepsBlankLineDeletion(t *testing.T) {
	got := CompareTexts("a\n\nb\n", "a\nb\n")
	var dels int
	for _, line := range got.Lines {
		if line.Kind == DiffDel && line.Text == "" {
			dels++
		}
	}
	if dels != 1 {
		t.Fatalf("blank-line deletion missing: %+v", got.Lines)
	}
}

func TestReviewR2CompareLineCap(t *testing.T) {
	const lines = 1_000_000
	var b strings.Builder
	b.Grow(lines * 2)
	for i := 0; i < lines; i++ {
		b.WriteString("x\n")
	}
	out := CompareTexts("", b.String())
	if len(out.Lines) > MaxDiffLines {
		t.Fatalf("got %d diff lines, want at most %d", len(out.Lines), MaxDiffLines)
	}
}

func TestReviewR2CompareUTF8(t *testing.T) {
	// Each line is three UTF-8 bytes; trim must not split a code point.
	unit := "中"
	var b strings.Builder
	for i := 0; i < maxCompareTextBytes/2/len(unit)+10; i++ {
		b.WriteString(unit + "\n")
	}
	out := CompareTexts("", b.String()).Lines
	for _, line := range out {
		if !utf8.ValidString(strings.TrimPrefix(line.Text, "+")) && line.Text != "" {
			// degrade path uses raw line text without +
			if !utf8.ValidString(line.Text) {
				t.Fatalf("invalid UTF-8 in line %q", line.Text)
			}
		}
	}
}
