package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"fx-desktop/internal/workspace"
)

// reviewing returns a window whose drawer has later.txt selected against a
// complete list that names it, with a diff on screen.
func reviewing(t *testing.T) *ProjectWindow {
	t.Helper()
	s := &ProjectWindow{}
	s.shell.ReviewPath = "later.txt"
	s.workspace.setGit(&GitInfo{Changes: []workspace.Change{
		{Path: "earlier.txt", Kind: "M", Adds: 1},
		{Path: "later.txt", Kind: "M", Adds: 2},
	}}, nil)
	gen := s.workspace.beginReviewRead("later.txt")
	if !s.workspace.publishReview(gen, "later.txt", []workspace.DiffLine{{Text: "+kept"}}, 0, false, nil) {
		t.Fatal("the fixture's diff was not published")
	}
	return s
}

func reviewGen(s *ProjectWindow) uint64 {
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	return s.workspace.reviewGen
}

// The four answers a working-tree read can give about the selected file. Only
// a complete status list that omits it may clear the selection (r10 §3).
func TestReviewSelectionFollowsListCompleteness(t *testing.T) {
	earlier := workspace.Change{Path: "earlier.txt", Kind: "M", Adds: 1}
	later := workspace.Change{Path: "later.txt", Kind: "M", Adds: 2}
	for _, tc := range []struct {
		name        string
		next        *GitInfo
		keep        bool
		unconfirmed bool
		incomplete  bool
	}{
		{"status cut and the file is missing", &GitInfo{ListTruncated: true, Changes: []workspace.Change{earlier}}, true, true, true},
		{"status cut but the file is listed", &GitInfo{ListTruncated: true, Changes: []workspace.Change{earlier, later}}, true, false, true},
		{"only the counts were cut and the file is missing", &GitInfo{CountsTruncated: true, Changes: []workspace.Change{earlier}}, false, false, true},
		{"complete status shows the file was committed", &GitInfo{Changes: []workspace.Change{earlier}}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := reviewing(t)
			s.workspace.setGit(tc.next, nil)
			s.reconcileReview()
			d := (&Coordinator{}).sessionReview(s)
			if tc.keep {
				if s.shell.ReviewPath != "later.txt" || d.Selected != "later.txt" || len(d.Diff) != 1 {
					t.Fatalf("selection lost: path=%q data=%+v", s.shell.ReviewPath, d)
				}
			} else if s.shell.ReviewPath != "" || d.Selected != "" || len(d.Diff) != 0 {
				t.Fatalf("selection survived a confirmed removal: path=%q data=%+v", s.shell.ReviewPath, d)
			}
			if d.SelectedUnconfirmed != tc.unconfirmed {
				t.Fatalf("SelectedUnconfirmed = %v, want %v", d.SelectedUnconfirmed, tc.unconfirmed)
			}
			if d.ListTruncated != tc.incomplete {
				t.Fatalf("the drawer's incomplete caption = %v, want %v", d.ListTruncated, tc.incomplete)
			}
		})
	}
}

// A partial list that omits the open file asks for that file's diff once — a
// single-path read with its own budget — and not again while later partial
// lists keep omitting it. A complete list then settles it either way.
func TestReviewPartialListRechecksTheFileOnce(t *testing.T) {
	isolateWithGit(t)
	s := reviewing(t)
	s.project = t.TempDir()
	s.shell.ReviewOpen = true
	partial := func() *GitInfo {
		return &GitInfo{ListTruncated: true, Changes: []workspace.Change{{Path: "earlier.txt", Kind: "M", Adds: 1}}}
	}
	before := reviewGen(s)
	s.workspace.setGit(partial(), nil)
	s.reconcileReview()
	if got := reviewGen(s); got != before+1 {
		t.Fatalf("the unconfirmed file was read %d times, want once", got-before)
	}
	for i := 0; i < 3; i++ {
		s.workspace.setGit(partial(), nil)
		s.reconcileReview()
	}
	if got := reviewGen(s); got != before+1 {
		t.Fatalf("a quiet partial tree cost %d more reads", got-before-1)
	}
	if s.shell.ReviewPath != "later.txt" {
		t.Fatal("the selection was lost across partial reads")
	}

	// The complete list names it again: the stamp moved, so it is read once.
	s.workspace.setGit(&GitInfo{Changes: []workspace.Change{{Path: "later.txt", Kind: "M", Adds: 2}}}, nil)
	s.reconcileReview()
	if got := reviewGen(s); got != before+2 || s.shell.ReviewPath != "later.txt" {
		t.Fatalf("gen moved %d, path %q", got-before, s.shell.ReviewPath)
	}
	// And a complete list without it is the confirmed removal.
	s.workspace.setGit(&GitInfo{}, nil)
	s.reconcileReview()
	if s.shell.ReviewPath != "" || (&Coordinator{}).sessionReview(s).Selected != "" {
		t.Fatal("a complete list did not clear the committed file")
	}
	s.cancelFeature("review")
}

// Reopening the drawer judges the remembered file the same way an automatic
// refresh does.
func TestToggleReviewKeepsAnUnconfirmedSelection(t *testing.T) {
	isolateWithGit(t)
	c := &Coordinator{}
	s := reviewing(t)
	s.project = t.TempDir()
	s.workspace.setGit(&GitInfo{ListTruncated: true, Changes: []workspace.Change{{Path: "earlier.txt", Kind: "M"}}}, nil)
	c.toggleReview(s)
	if !s.shell.ReviewOpen || s.shell.ReviewPath != "later.txt" {
		t.Fatalf("reopening on a partial list moved the selection to %q", s.shell.ReviewPath)
	}
	if !c.sessionReview(s).SelectedUnconfirmed {
		t.Fatal("the unconfirmed selection is not labelled")
	}
	c.toggleReview(s)

	s.workspace.setGit(&GitInfo{Changes: []workspace.Change{{Path: "earlier.txt", Kind: "M"}}}, nil)
	c.toggleReview(s)
	if s.shell.ReviewPath != "earlier.txt" {
		t.Fatalf("reopening on a complete list kept the committed file: %q", s.shell.ReviewPath)
	}
	s.cancelFeature("review")
}

// The collection layer reports the two budgets separately: a cut status list
// is a prefix, a cut numstat still lists every change.
func TestGitInfoSeparatesStatusAndCountTruncation(t *testing.T) {
	isolateWithGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	const files = 40
	for i := 0; i < files; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d.txt", i)), []byte("one\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "init")
	for i := 0; i < files; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d.txt", i)), []byte("one\ntwo\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()

	full, err := gitInfo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if full.ListTruncated || full.CountsTruncated || full.incomplete() || len(full.Changes) != files {
		t.Fatalf("a small tree read as incomplete: %+v", full)
	}

	cutStatus, err := gitInfoWithin(ctx, dir, 200, maxGitDiffBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cutStatus.ListTruncated || cutStatus.CountsTruncated || !cutStatus.incomplete() {
		t.Fatalf("cut status: list=%v counts=%v", cutStatus.ListTruncated, cutStatus.CountsTruncated)
	}
	if n := len(cutStatus.Changes); n == 0 || n >= files {
		t.Fatalf("cut status listed %d of %d changes", n, files)
	}
	for i, ch := range cutStatus.Changes {
		if want := fmt.Sprintf("file-%03d.txt", i); ch.Path != want {
			t.Fatalf("the cut list is not a prefix of whole records: [%d] = %q", i, ch.Path)
		}
	}

	cutCounts, err := gitInfoWithin(ctx, dir, maxGitDiffBytes, 200)
	if err != nil {
		t.Fatal(err)
	}
	if cutCounts.ListTruncated || !cutCounts.CountsTruncated || !cutCounts.incomplete() {
		t.Fatalf("cut counts: list=%v counts=%v", cutCounts.ListTruncated, cutCounts.CountsTruncated)
	}
	if len(cutCounts.Changes) != files {
		t.Fatalf("a cut numstat dropped changes: %d of %d", len(cutCounts.Changes), files)
	}

	// The window's verdict on a file past the cut: unconfirmed for the cut
	// status, a real change for the cut counts.
	last := fmt.Sprintf("file-%03d.txt", files-1)
	var w workspaceController
	w.setGit(cutStatus, nil)
	if known, listed, _, unconfirmed := w.reviewVerdict(last); !known || listed || !unconfirmed {
		t.Fatalf("past a cut status: known=%v listed=%v unconfirmed=%v", known, listed, unconfirmed)
	}
	w.setGit(cutCounts, nil)
	if known, listed, _, unconfirmed := w.reviewVerdict(last); !known || !listed || unconfirmed {
		t.Fatalf("with cut counts: known=%v listed=%v unconfirmed=%v", known, listed, unconfirmed)
	}
}
