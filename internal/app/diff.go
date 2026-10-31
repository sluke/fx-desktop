package app

import (
	"strings"

	"fx-desktop/internal/app/chrome"
)

// The diff we render is deliberately modest. fx hands us a whole-file
// {Path, OldText, NewText} for an edit, not a unified diff, so turning it
// into +/- colouring needs a local comparison. A longest-common-subsequence
// over lines is enough for that, and its quadratic table is why everything
// here is capped: a 5k-line file must degrade to "show the new text" rather
// than allocate a 5k×5k table on the event goroutine.
const (
	// maxDiffCells bounds the LCS table (old×new lines).
	maxDiffCells = 400_000
	// maxDiffLines bounds either side before we give up on comparing at all.
	maxDiffLines = 4000
)

// lineDiff classifies each line of oldText/newText as context, added or
// removed. It never fails: when the inputs are too large to compare it
// degrades to reporting the new text as added, which is honest about having
// skipped the comparison.
func lineDiff(oldText, newText string) []chrome.DiffLine {
	old := splitDiffLines(oldText)
	next := splitDiffLines(newText)
	if len(old) > maxDiffLines || len(next) > maxDiffLines || len(old)*len(next) > maxDiffCells {
		out := make([]chrome.DiffLine, 0, len(next))
		for _, line := range next {
			out = append(out, chrome.DiffLine{Kind: chrome.DiffAdd, Text: line})
		}
		return out
	}
	return lcsDiff(old, next)
}

// splitDiffLines splits on \n and drops the single trailing empty line a
// final newline produces, so "a\n" and "a" compare equal.
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
func lcsDiff(a, b []string) []chrome.DiffLine {
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

	out := make([]chrome.DiffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, chrome.DiffLine{Kind: chrome.DiffContext, Text: a[i]})
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			out = append(out, chrome.DiffLine{Kind: chrome.DiffDel, Text: a[i]})
			i++
		default:
			out = append(out, chrome.DiffLine{Kind: chrome.DiffAdd, Text: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, chrome.DiffLine{Kind: chrome.DiffDel, Text: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, chrome.DiffLine{Kind: chrome.DiffAdd, Text: b[j]})
	}
	return out
}
