package app

import (
	"context"
	"sync"
)

// featureScopes gives each kind of background work its own cancellable child
// of the window context. Starting a newer query cancels the older one, so a
// superseded `git status`, history page or fx command stops instead of running
// to completion for a result nobody will publish. The generation checks stay,
// because cancelling cannot retract a callback that has already been queued
// (§4.4).
type featureScopes struct {
	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}

// scope returns a context for one feature of this window, cancelling the
// scope that feature had before. The caller must still call the returned
// cancel when it is done, so an idle window does not keep a timer or process
// alive.
func (s *ProjectWindow) scope(feature string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(s.lifetime())
	s.scopes.mu.Lock()
	if previous := s.scopes.cancel[feature]; previous != nil {
		previous()
	}
	if s.scopes.cancel == nil {
		s.scopes.cancel = make(map[string]context.CancelFunc, 8)
	}
	s.scopes.cancel[feature] = cancel
	s.scopes.mu.Unlock()
	return ctx, cancel
}
