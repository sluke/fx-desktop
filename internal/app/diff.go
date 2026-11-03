package app

import (
	"fx-desktop/internal/workspace"
)

// The comparison caps belong to the workspace package; the app keeps their
// names for the code and tests that talk about them.
const (
	maxDiffCells = workspace.MaxDiffCells
	maxDiffLines = workspace.MaxDiffLines
)

// lineDiff classifies each line of oldText/newText as context, added or
// removed. The comparison itself belongs to the workspace package — it is a
// fact about text, not about the palette — and it never fails: when the inputs
// are too large to compare it degrades to reporting the new text as added.
func lineDiff(oldText, newText string) []workspace.DiffLine {
	return workspace.CompareTexts(oldText, newText)
}
