package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

// A mention is @path in the project. What the picker writes and what the
// sender reads must be the same grammar, so it lives here.
func TestMentionPathsReadsWhatAMessageNames(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{"look at @main.go", []string{"main.go"}},
		{"@a.go and @b.go", []string{"a.go", "b.go"}},
		{"mail me at me@example.com", nil},
		{"(@internal/app/console.go)", []string{"internal/app/console.go"}},
		{"@main.go, then @main.go again", []string{"main.go"}},
		{"@main.go. @main.go!", []string{"main.go"}},
	}
	for _, test := range tests {
		got := MentionPaths(test.text)
		if strings.Join(got, "|") != strings.Join(test.want, "|") {
			t.Errorf("MentionPaths(%q) = %v, want %v", test.text, got, test.want)
		}
	}
}

// A mention may only name a file inside the project: the content is read and
// handed to fx, so it has to be one of the project's own.
func TestResolvePathRefusesEscapes(t *testing.T) {
	project := t.TempDir()
	if _, ok := ResolvePath(project, "../outside.txt"); ok {
		t.Fatal("a path climbing out of the project is not a mention")
	}
	if _, ok := ResolvePath(project, "/etc/passwd"); ok {
		t.Fatal("an absolute path is not a mention")
	}
	if _, ok := ResolvePath(project, "."); ok {
		t.Fatal("the project itself is not a mention")
	}
	want := filepath.Join(project, "internal/app.go")
	if got, ok := ResolvePath(project, "internal/app.go"); !ok || got != want {
		t.Fatalf("ResolvePath = %q, %v", got, ok)
	}
}

// The mention sizes and human labels are the workspace's facts.
func TestHumanSizeReadsLikeAPersonWroteIt(t *testing.T) {
	for _, test := range []struct {
		n    int
		want string
	}{{512, "512 B"}, {2048, "2 KB"}, {4 << 20, "4.0 MB"}} {
		if got := HumanSize(test.n); got != test.want {
			t.Errorf("HumanSize(%d) = %q, want %q", test.n, got, test.want)
		}
	}
}

// A unified diff is classified by its first character; the file headers are
// context, not an addition and a deletion.
func TestClassifyUnifiedDiff(t *testing.T) {
	lines, omitted := ClassifyUnifiedDiff("--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n same\n")
	if omitted != 0 || len(lines) != 6 {
		t.Fatalf("lines = %+v, omitted = %d", lines, omitted)
	}
	want := []string{DiffContext, DiffContext, DiffContext, DiffDel, DiffAdd, DiffContext}
	for i, kind := range want {
		if lines[i].Kind != kind {
			t.Fatalf("line %d = %q, want %q", i, lines[i].Kind, kind)
		}
	}
}

// A whole-file edit is compared line by line, and a file too large to compare
// degrades to "all added" rather than allocating the table.
func TestCompareTexts(t *testing.T) {
	lines := CompareTexts("a\nb\n", "a\nc\n")
	if len(lines) != 3 || lines[1].Kind != DiffDel || lines[2].Kind != DiffAdd {
		t.Fatalf("lines = %+v", lines)
	}
	huge := strings.Repeat("x\n", MaxDiffLines+1)
	if lines := CompareTexts("", huge); len(lines) != MaxDiffLines+1 || lines[0].Kind != DiffAdd {
		t.Fatalf("degradation = %d lines", len(lines))
	}
}
