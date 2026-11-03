package app

import (
	"testing"

	"fx-desktop/internal/workspace"
)

// N4: a read is admitted by its generation, not by the arrival order. The
// older git read failing last must not blank a snapshot a newer read already
// published: the check and the write share one critical section.
func TestStaleGitReadCannotOverwriteANewerSnapshot(t *testing.T) {
	s := &ProjectWindow{}
	old := s.workspace.beginGitRead()
	newer := s.workspace.beginGitRead()
	if !s.workspace.publishGit(newer, &GitInfo{Branch: "main"}, nil) {
		t.Fatal("the newest read was refused")
	}
	if s.workspace.publishGit(old, nil, errNoRepo) {
		t.Fatal("a stale read was published")
	}
	git, err := s.workspace.gitSnapshot()
	if git == nil || git.Branch != "main" || err != nil {
		t.Fatalf("the newer snapshot was overwritten: %+v, %v", git, err)
	}
}

// N4: review A→B→A. The first A's late reply has the path that is on screen
// again, so a path comparison alone would accept it; its generation is stale.
func TestStaleReviewReplyCannotWinAfterReturningToTheSamePath(t *testing.T) {
	s := &ProjectWindow{}
	firstA := s.workspace.beginReviewRead("a.go")
	s.workspace.beginReviewRead("b.go")
	lastA := s.workspace.beginReviewRead("a.go")

	if s.workspace.publishReview(firstA, "a.go", []workspace.DiffLine{{Kind: "+", Text: "old"}}, 0, nil) {
		t.Fatal("the first A's reply was published after A→B→A")
	}
	if !s.workspace.publishReview(lastA, "a.go", []workspace.DiffLine{{Kind: "+", Text: "new"}}, 0, nil) {
		t.Fatal("the newest A's reply was refused")
	}
	s.workspace.mu.Lock()
	defer s.workspace.mu.Unlock()
	if len(s.workspace.reviewDiff) != 1 || s.workspace.reviewDiff[0].Text != "new" {
		t.Fatalf("review diff = %+v", s.workspace.reviewDiff)
	}
	if s.workspace.reviewBusy {
		t.Fatal("the published reply left the drawer reading")
	}
}

// N4: fx and fx-saved both write `saved` under different scopes. Whichever
// was asked for last owns the field: an older fx answer must not revive the
// list a newer saved-list read already replaced.
func TestOlderFxReplyCannotReviveTheSavedList(t *testing.T) {
	s := &ProjectWindow{}
	fxGen, fxSaved := s.beginFxRead()
	savedGen := s.beginSavedRead()

	// The fx read (older, for saved) answers first and still owns status.
	if !s.publishFx(fxGen, fxSaved, &FxStatus{Model: "m"}, nil, nil, []FxSession{{ID: "old"}}, nil) {
		t.Fatal("the newest fx read was refused")
	}
	s.mu.Lock()
	saved := append([]FxSession(nil), s.saved...)
	s.mu.Unlock()
	if len(saved) != 0 {
		t.Fatalf("an older fx reply wrote the saved list: %+v", saved)
	}

	// The saved list read (newer) publishes its answer.
	if !s.publishSaved(savedGen, []FxSession{{ID: "new"}}) {
		t.Fatal("the newest saved read was refused")
	}

	// A second fx reply that was requested before the saved read cannot
	// revive the old list either.
	if !s.publishFx(fxGen, fxSaved, nil, nil, nil, []FxSession{{ID: "old"}}, nil) {
		t.Fatal("the fx status generation changed unexpectedly")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) != 1 || s.saved[0].ID != "new" {
		t.Fatalf("saved = %+v, want the newer read's answer", s.saved)
	}
}

// A read retired by a close or a newer request is not published even when its
// value was computed: the context is checked before admission.
func TestCancelledReadPublishesNothing(t *testing.T) {
	s := &ProjectWindow{coord: NewCoordinator(), project: t.TempDir()}
	ctx, cancel := s.scope("git")
	cancel()
	s.readGit(ctx, s.workspace.beginGitRead())
	git, err := s.workspace.gitSnapshot()
	if git != nil || err != nil {
		t.Fatalf("a cancelled read published a result: %+v, %v", git, err)
	}
}
