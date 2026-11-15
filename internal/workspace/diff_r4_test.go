package workspace

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A final newline terminates the last line; it does not create an extra one.
// A newline before it still leaves a blank line, and deleting that blank line
// is a real deletion that the comparison must report.
func TestCompareTextsTrailingBlankDeletion(t *testing.T) {
	tests := []struct {
		name      string
		old, next string
		wantDels  int
		wantAdds  int
	}{
		{name: "single newline to empty", old: "\n", next: "", wantDels: 1},
		{name: "blank after content", old: "a\n\n", next: "a\n", wantDels: 1},
		{name: "blank after content no final newline", old: "a\n\n", next: "a", wantDels: 1},
		{name: "two trailing blanks", old: "a\n\n\n", next: "a\n", wantDels: 2},
		{name: "content without newline", old: "a", next: "", wantDels: 1},
		{name: "add blank line", old: "a\n", next: "a\n\n", wantAdds: 1},
		{name: "add blank to empty", old: "", next: "\n", wantAdds: 1},
		{name: "middle blank kept", old: "a\n\nb\n", next: "a\nb\n", wantDels: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareTexts(tt.old, tt.next)
			if got.Truncated || got.ComparisonSkipped {
				t.Fatalf("small input was degraded: %+v", got)
			}
			var dels, adds int
			for _, line := range got.Lines {
				switch line.Kind {
				case DiffDel:
					dels++
				case DiffAdd:
					adds++
				}
			}
			if dels != tt.wantDels || adds != tt.wantAdds {
				t.Fatalf("lines = %+v, want %d deletions and %d additions", got.Lines, tt.wantDels, tt.wantAdds)
			}
		})
	}
}

// The blank line at the end is a line of its own, so the diff must not
// silently drop it: "\n" is one empty line, "a\n\n" is "a" plus a blank.
func TestCompareTextsCountsTrailingBlankLines(t *testing.T) {
	for _, tt := range []struct {
		text string
		want []string
	}{
		{text: "a", want: []string{"a"}},
		{text: "a\n", want: []string{"a"}},
		{text: "a\n\n", want: []string{"a", ""}},
		{text: "a\n\n\n", want: []string{"a", "", ""}},
		{text: "\n", want: []string{""}},
		{text: "\n\n", want: []string{"", ""}},
		{text: "a\nb", want: []string{"a", "b"}},
	} {
		lines, cut := scanLinesBudget(tt.text, maxCompareTextBytes, MaxDiffLines)
		if cut {
			t.Fatalf("%q reported a cut", tt.text)
		}
		if len(lines) != len(tt.want) {
			t.Fatalf("%q scanned %q, want %q", tt.text, lines, tt.want)
		}
		for i := range tt.want {
			if lines[i] != tt.want[i] {
				t.Fatalf("%q scanned %q, want %q", tt.text, lines, tt.want)
			}
		}
	}
}

// A budget cut lands wherever the byte limit is; the line it returns must
// still be whole runes, including when the cut falls inside a 4-byte emoji.
func TestCompareTextsBudgetKeepsValidUTF8(t *testing.T) {
	old := strings.Repeat("中", maxCompareTextBytes) // single line, 3x the side budget
	got := CompareTexts(old, "")
	if !got.Truncated || !got.ComparisonSkipped {
		t.Fatalf("oversized input was not reported: %+v", got)
	}
	if len(got.Lines) == 0 {
		t.Fatal("no preview line for an oversized deletion")
	}
	for _, line := range got.Lines {
		if !utf8.ValidString(line.Text) {
			t.Fatalf("cut through a code point: %d bytes", len(line.Text))
		}
		if len(line.Text) > maxCompareTextBytes/2 {
			t.Fatalf("preview line used %d bytes, over the side budget", len(line.Text))
		}
	}

	// Placing the emoji so the byte cap falls inside it is the case the old
	// trim missed: half of a 4-byte rune at the boundary.
	var b strings.Builder
	b.WriteString(strings.Repeat("x", maxCompareTextBytes/2-2))
	for b.Len() < maxCompareTextBytes {
		b.WriteString("🙂")
	}
	got = CompareTexts("", b.String())
	for _, line := range got.Lines {
		if !utf8.ValidString(line.Text) {
			t.Fatalf("emoji cut through a code point: %d bytes", len(line.Text))
		}
	}
}

// ClassifyUnifiedDiff keeps trailing blank lines; only the final separator is
// dropped, so a diff that ends in a blank context line still shows it.
func TestClassifyUnifiedDiffKeepsTrailingBlankLines(t *testing.T) {
	for _, tt := range []struct {
		text string
		want []string
	}{
		{text: "", want: nil},
		{text: "\n", want: []string{""}},
		{text: "a\n", want: []string{"a"}},
		{text: "a\n\n", want: []string{"a", ""}},
		{text: "a\n\n\n", want: []string{"a", "", ""}},
		{text: "\n\n", want: []string{"", ""}},
		{text: "-old\r\n+new\r\n", want: []string{"-old", "+new"}},
	} {
		lines, omitted := ClassifyUnifiedDiff(tt.text)
		if omitted != 0 {
			t.Fatalf("%q omitted %d lines", tt.text, omitted)
		}
		if len(lines) != len(tt.want) {
			t.Fatalf("%q classified %d lines (%+v), want %d", tt.text, len(lines), lines, len(tt.want))
		}
		for i := range tt.want {
			if lines[i].Text != tt.want[i] {
				t.Fatalf("%q line %d = %q, want %q", tt.text, i, lines[i].Text, tt.want[i])
			}
		}
	}
}
