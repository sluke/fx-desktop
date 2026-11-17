package workspace

import (
	"fmt"
	"strings"
	"testing"
)

func r9File(lines int) []string {
	out := make([]string, lines)
	for i := range out {
		out[i] = fmt.Sprintf("\tline %04d := compute(%d)", i, i*7)
	}
	return out
}

func r9Count(lines []DiffLine) (ctx, add, del int) {
	for _, l := range lines {
		switch l.Kind {
		case DiffAdd:
			add++
		case DiffDel:
			del++
		default:
			ctx++
		}
	}
	return
}

// MaxDiffCells used to be applied to the product of the two whole files, so
// the comparison was abandoned as soon as both sides passed 632 lines: a
// one-line edit in a 700-line file was shown as 700 added lines. The cap now
// applies to what is left after the shared head and tail are set aside.
func TestRegressionR9_OneLineEditInOrdinaryFileIsCompared(t *testing.T) {
	for _, n := range []int{600, 632, 633, 700, 1500, 3999} {
		t.Run(fmt.Sprintf("lines=%d", n), func(t *testing.T) {
			oldLines := r9File(n)
			newLines := append([]string(nil), oldLines...)
			newLines[n/2] = "\tchanged := true"
			got := CompareTexts(strings.Join(oldLines, "\n")+"\n", strings.Join(newLines, "\n")+"\n")
			ctx, add, del := r9Count(got.Lines)
			if got.ComparisonSkipped || got.Truncated {
				t.Fatalf("a one-line edit in a %d-line file was not compared (%d lines shown as added)", n, add)
			}
			if add != 1 || del != 1 || ctx != n-1 {
				t.Fatalf("got context=%d add=%d del=%d, want %d/1/1", ctx, add, del, n-1)
			}
			if got.Lines[n/2].Kind != DiffDel || got.Lines[n/2+1].Kind != DiffAdd {
				t.Fatalf("the edit is not where it was made: %+v %+v", got.Lines[n/2], got.Lines[n/2+1])
			}
		})
	}
}

func TestRegressionR9_CompareTextsEdges(t *testing.T) {
	// Two files with nothing in common still exceed the table and are a
	// preview, said as such.
	a, b := r9File(700), make([]string, 700)
	for i := range b {
		b[i] = fmt.Sprintf("other %d", i)
	}
	got := CompareTexts(strings.Join(a, "\n"), strings.Join(b, "\n"))
	if !got.ComparisonSkipped || !got.Truncated {
		t.Fatalf("two unrelated 700-line files were compared: skipped=%v truncated=%v", got.ComparisonSkipped, got.Truncated)
	}
	for _, l := range got.Lines {
		if l.Kind != DiffAdd {
			t.Fatalf("an uncompared preview must not claim context or deletions: %+v", l)
		}
	}

	same := strings.Join(r9File(900), "\n")
	got = CompareTexts(same, same)
	if ctx, add, del := r9Count(got.Lines); got.ComparisonSkipped || add != 0 || del != 0 || ctx != 900 {
		t.Fatalf("identical files: skipped=%v context=%d add=%d del=%d", got.ComparisonSkipped, ctx, add, del)
	}

	// An insertion at the very start and one at the very end: the shared
	// tail and the shared head respectively carry the whole file.
	body := strings.Join(r9File(1000), "\n") + "\n"
	got = CompareTexts(body, "first\n"+body)
	if ctx, add, del := r9Count(got.Lines); got.ComparisonSkipped || add != 1 || del != 0 || ctx != 1000 || got.Lines[0].Kind != DiffAdd {
		t.Fatalf("insert at start: skipped=%v context=%d add=%d del=%d", got.ComparisonSkipped, ctx, add, del)
	}
	got = CompareTexts(body, body+"last\n")
	if ctx, add, del := r9Count(got.Lines); got.ComparisonSkipped || add != 1 || del != 0 || ctx != 1000 || got.Lines[1000].Kind != DiffAdd {
		t.Fatalf("append at end: skipped=%v context=%d add=%d del=%d", got.ComparisonSkipped, ctx, add, del)
	}

	// Repeated lines: the head and tail must not overlap and count a line twice.
	got = CompareTexts("a\na\n", "a\na\na\n")
	if ctx, add, del := r9Count(got.Lines); add != 1 || del != 0 || ctx != 2 {
		t.Fatalf("repeated lines: context=%d add=%d del=%d, want 2/1/0", ctx, add, del)
	}
	got = CompareTexts("a\na\na\n", "a\n")
	if ctx, add, del := r9Count(got.Lines); add != 0 || del != 2 || ctx != 1 {
		t.Fatalf("repeated lines removed: context=%d add=%d del=%d, want 1/0/2", ctx, add, del)
	}
}

// Full-width punctuation directly after a mention used to be read as part of
// the file name, so nothing was attached and nothing was refused.
func TestRegressionR9_MentionAndFullWidthPunctuation(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"看一下 @main.go，谢谢", []string{"main.go"}},
		{"请修复 @main.go。", []string{"main.go"}},
		{"对比 @a.go、@b.go", []string{"a.go", "b.go"}},
		{"（见 @main.go）", []string{"main.go"}},
		{"（@main.go）", []string{"main.go"}},
		{"先看：@docs/读我.md；再看 @b.go！", []string{"docs/读我.md", "b.go"}},
		{"see @main.go, thanks", []string{"main.go"}},
		{"mail someone@example.com now", nil},
	}
	for _, c := range cases {
		got := MentionPaths(c.text)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("MentionPaths(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	// A file whose name really holds such a character is written quoted and
	// read back whole.
	for _, path := range []string{"笔记，草稿.md", "（旧）plan.md", "a、b.txt"} {
		written := FormatMention(path)
		if !strings.HasPrefix(written, `@"`) {
			t.Errorf("FormatMention(%q) = %q, want a quoted mention", path, written)
		}
		if got := MentionPaths("看 " + written + "，好"); len(got) != 1 || got[0] != path {
			t.Errorf("round trip of %q = %q", path, got)
		}
	}
	// The picker's query stops at the same character.
	text := "看 @main，谢谢"
	query, start, end, ok := MentionAt(text, len([]rune("看 @main")))
	if !ok || query != "main" || start != 2 || end != 7 {
		t.Errorf("MentionAt = %q [%d,%d) ok=%v, want main [2,7)", query, start, end, ok)
	}
}

// Inside a hunk the first character decides. A removed line whose text begins
// with "--" reads "---…" and an added "++i" reads "+++i": both were drawn as
// context, as if they were file headers.
func TestRegressionR9_UnifiedDiffContentThatLooksLikeAHeader(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/q.sql b/q.sql",
		"--- a/q.sql",
		"+++ b/q.sql",
		"@@ -1,3 +1,3 @@",
		" select 1;",
		"--- old comment",
		"+++counter;",
		" select 2;",
		"diff --git a/r.md b/r.md",
		"--- a/r.md",
		"+++ b/r.md",
		"@@ -5 +5 @@ heading",
		"----",
		"+text",
		`\ No newline at end of file`,
	}, "\n") + "\n"
	lines, omitted := ClassifyUnifiedDiff(diff)
	if omitted != 0 {
		t.Fatalf("omitted = %d", omitted)
	}
	want := []string{
		DiffContext, DiffContext, DiffContext, DiffContext,
		DiffContext, DiffDel, DiffAdd, DiffContext,
		DiffContext, DiffContext, DiffContext, DiffContext,
		DiffDel, DiffAdd, DiffContext,
	}
	if len(lines) != len(want) {
		t.Fatalf("%d lines, want %d", len(lines), len(want))
	}
	for i, line := range lines {
		if line.Kind != want[i] {
			t.Errorf("line %d %q = %s, want %s", i, line.Text, line.Kind, want[i])
		}
	}
}

func TestRegressionR9_HunkLineCounts(t *testing.T) {
	cases := []struct {
		header   string
		old, new int
	}{
		{"@@ -1,3 +1,4 @@", 3, 4},
		{"@@ -1 +1 @@ func main() {", 1, 1},
		{"@@ -0,0 +1,2 @@", 0, 2},
		{"@@ -3,2 +0,0 @@", 2, 0},
		{"@@@ -1,2 -1,2 +1,3 @@@", 0, 0},
		{"@@ -1,x +1,2 @@", 0, 0},
		{"@@ broken", 0, 0},
	}
	for _, c := range cases {
		if a, b := hunkLineCounts(c.header); a != c.old || b != c.new {
			t.Errorf("hunkLineCounts(%q) = %d, %d, want %d, %d", c.header, a, b, c.old, c.new)
		}
	}
}

func BenchmarkRegressionR9CompareOneLineEdit(b *testing.B) {
	for _, n := range []int{300, 632, 700, 3999} {
		oldLines := r9File(n)
		newLines := append([]string(nil), oldLines...)
		newLines[n/2] = "\tchanged := true"
		oldText, newText := strings.Join(oldLines, "\n")+"\n", strings.Join(newLines, "\n")+"\n"
		b.Run(fmt.Sprintf("lines=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				CompareTexts(oldText, newText)
			}
		})
	}
}
