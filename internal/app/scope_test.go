package app

import (
	"context"
	"testing"
	"time"
)

// A newer query of the same feature retires the older one: two `git status`
// runs for one window would otherwise both finish, and the older answer has no
// reader left.
func TestScopeCancelsThePreviousQueryOfTheSameFeature(t *testing.T) {
	s := &ProjectWindow{}
	first, stopFirst := s.scope("history")
	defer stopFirst()
	second, stopSecond := s.scope("history")
	defer stopSecond()
	select {
	case <-first.Done():
	case <-time.After(time.Second):
		t.Fatal("a newer query must cancel the older one")
	}
	select {
	case <-second.Done():
		t.Fatal("the newer query was cancelled by the older one")
	default:
	}
}

// Features do not cancel each other: refreshing git must not retire a history
// request, and reloading history must not retire the conversation.
func TestScopeIsPerFeature(t *testing.T) {
	s := &ProjectWindow{}
	git, stopGit := s.scope("git")
	defer stopGit()
	history, stopHistory := s.scope("history")
	defer stopHistory()
	for name, ctx := range map[string]context.Context{"git": git, "history": history} {
		select {
		case <-ctx.Done():
			t.Fatalf("%s was cancelled by another feature", name)
		default:
		}
	}
}

// Closing the window ends every background feature, which is what makes the
// window's lifetime the tasks' lifetime.
func TestScopeFollowsTheWindowLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &ProjectWindow{ctx: ctx, cancel: cancel}
	feature, stop := s.scope("review")
	defer stop()
	cancel()
	select {
	case <-feature.Done():
	case <-time.After(time.Second):
		t.Fatal("closing the window did not cancel its background work")
	}
}

// A superseded query's own cancel must not reach the newer one: the older
// worker still defers its stop when it unwinds.
func TestScopeStopsAreIndependent(t *testing.T) {
	s := &ProjectWindow{}
	first, stopFirst := s.scope("fx")
	second, stopSecond := s.scope("fx")
	defer stopSecond()
	stopFirst()
	select {
	case <-second.Done():
		t.Fatal("the finished worker cancelled its successor")
	default:
	}
	select {
	case <-first.Done():
	default:
		t.Fatal("the finished worker did not cancel itself")
	}
}
