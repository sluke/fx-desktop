package workspace

import (
	"strings"
)

// Change is one working-tree change as the workspace read it. Kind is the
// reduced porcelain mark ("M", "A", "D", "R" or "?"); the presenter decides how
// to draw it.
type Change struct {
	Path string
	Kind string // "M", "A", "D", "R" or "?"
	Adds int
	Dels int
}

// Line kinds. They are semantic — a line was added, removed or kept — not
// colors; the UI maps them.
const (
	DiffContext = "context"
	DiffAdd     = "add"
	DiffDel     = "del"
)

// DiffLine is one classified line of a diff.
type DiffLine struct {
	Kind string
	Text string
}

// Limits for the local comparison. fx hands us a whole-file {Path, OldText,
// NewText} for an edit, not a unified diff, so turning it into +/- colouring
// needs a local comparison. A longest-common-subsequence over lines is enough
// for that, and its quadratic table is why everything here is capped: a
// 5k-line file must degrade to "show the new text" rather than allocate a
// 5k×5k table on the event goroutine.
const (
	// MaxDiffCells bounds the LCS table (old×new lines).
	MaxDiffCells = 400_000
	// MaxDiffLines bounds either side before we give up on comparing at all.
	MaxDiffLines = 4000
	// MaxUnifiedDiffLines bounds how much of one file's unified diff is read
	// into memory.
	MaxUnifiedDiffLines = 4000
)

// CompareTexts classifies each line of oldText/newText as context, added or
// removed. It never fails: when the inputs are too large to compare it
// degrades to reporting the new text as added, which is honest about having
// skipped the comparison.
func CompareTexts(oldText, newText string) []DiffLine {
	old := splitDiffLines(oldText)
	next := splitDiffLines(newText)
	if len(old) > MaxDiffLines || len(next) > MaxDiffLines || len(old)*len(next) > MaxDiffCells {
		out := make([]DiffLine, 0, len(next))
		for _, line := range next {
			out = append(out, DiffLine{Kind: DiffAdd, Text: line})
		}
		return out
	}
	return lcsDiff(old, next)
}

// splitDiffLines splits on \n and drops the single trailing empty line a final
// newline produces, so "a\n" and "a" compare equal.
func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// lcsDiff is the classic dynamic-programming LCS over lines: walk both
// sequences, emit a context line when they agree and otherwise emit the side
// the table says moves the comparison forward.
func lcsDiff(a, b []string) []DiffLine {
	n, m := len(a), len(b)
	// table[i][j] = length of the LCS of a[i:] and b[j:].
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				table[i][j] = table[i+1][j+1] + 1
			case table[i+1][j] >= table[i][j+1]:
				table[i][j] = table[i+1][j]
			default:
				table[i][j] = table[i][j+1]
			}
		}
	}

	out := make([]DiffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{Kind: DiffContext, Text: a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			out = append(out, DiffLine{Kind: DiffDel, Text: a[i]})
			i++
		default:
			out = append(out, DiffLine{Kind: DiffAdd, Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{Kind: DiffDel, Text: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{Kind: DiffAdd, Text: b[j]})
	}
	return out
}

// ClassifyUnifiedDiff turns a unified diff into classified lines. Header and
// hunk lines are kept as context so the reader can see where a change sits;
// they are not content, but dropping them silently would misrepresent the
// file.
func ClassifyUnifiedDiff(text string) ([]DiffLine, int) {
	if text == "" {
		return nil, 0
	}
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	omitted := 0
	if len(raw) > MaxUnifiedDiffLines {
		omitted = len(raw) - MaxUnifiedDiffLines
		raw = raw[:MaxUnifiedDiffLines]
	}
	out := make([]DiffLine, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimRight(line, "\r")
		out = append(out, DiffLine{Kind: UnifiedLineKind(line), Text: line})
	}
	return out, omitted
}

// UnifiedLineKind classifies one unified-diff line by its first character. The
// three-dash and three-plus file headers are not additions or deletions, so
// they are checked before the one-character rules.
func UnifiedLineKind(line string) string {
	switch {
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		return DiffContext
	case strings.HasPrefix(line, "+"):
		return DiffAdd
	case strings.HasPrefix(line, "-"):
		return DiffDel
	default:
		return DiffContext
	}
}
