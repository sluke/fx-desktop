package app

import (
	"errors"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/testutil"
)

func timeNow() time.Time { return time.Now() }

// A session reports fx's sign-in state from its own status snapshot, and
// reports nothing when it has none: an unknown state must not harden into a
// claim about the user's credentials.
func TestSessionAuthNoticeComesFromFx(t *testing.T) {
	s := &Session{}
	s.fx = &FxStatus{Auth: "missing", AuthHelp: "fx needs access. Run fx login to sign in."}
	got := s.authNotice()
	if !got.NeedsSignIn() || got.Auth != "missing" {
		t.Fatalf("auth notice: %+v", got)
	}
	s.fx = &FxStatus{Auth: "configured provider", ConnectedProviders: []string{"magpie"}}
	if got := s.authNotice(); got.NeedsSignIn() || len(got.Providers) != 1 {
		t.Fatalf("signed in: %+v", got)
	}
	s.fx = nil
	if got := s.authNotice(); got.NeedsSignIn() || got.Auth != "" {
		t.Fatalf("no fx info must not read as signed out: %+v", got)
	}
}

func TestAgentStateUsesFxReportedValues(t *testing.T) {
	s := &Session{started: timeNow()}
	s.fx = &FxStatus{Model: "zenmux/deepseek/deepseek-v4.1-flash", PermissionMode: "auto"}
	s.usage = &FxUsage{}
	s.usage.Totals.TotalTokens = 128400
	s.usage.Totals.Spend = 1.8421

	a := s.agentState()
	if a.Model != "zenmux/deepseek/deepseek-v4.1-flash" {
		t.Fatalf("model: %q", a.Model)
	}
	if a.Permission != "auto" {
		t.Fatalf("permission: %q", a.Permission)
	}
	if a.Tokens != "128.4k" {
		t.Fatalf("tokens: %q", a.Tokens)
	}
	if a.Spend != "$1.84" {
		t.Fatalf("spend: %q", a.Spend)
	}
	if a.Status != "Running" {
		t.Fatalf("status: %q", a.Status)
	}
	if a.Elapsed == "" {
		t.Fatal("elapsed should be set")
	}
}

func TestAgentStatePermissionsOverrideStatus(t *testing.T) {
	s := &Session{started: timeNow()}
	s.fx = &FxStatus{Model: "m", PermissionMode: "auto"}
	s.perms = &FxPermissions{Mode: "full-access"}
	if got := s.agentState().Permission; got != "full-access" {
		t.Fatalf("permission: %q", got)
	}
}

func TestAgentStateZeroUsageHidesStats(t *testing.T) {
	s := &Session{started: timeNow()}
	a := s.agentState()
	if a.Tokens != "" || a.Spend != "" {
		t.Fatalf("empty usage should hide the stats: %+v", a)
	}
}

// A conversation reports its own processed tokens, not the AI Gateway
// account total: the strip sits next to Model and Context, both of which
// describe this run, and the account total is unavailable on other
// providers. Spend remains the account figure, since that is what it is.
func TestAgentStateUsesConversationTokens(t *testing.T) {
	s := &Session{started: timeNow(), chat: &chatSession{}}
	s.chat.inputTokens, s.chat.outputTokens = 1200, 90
	s.usage = &FxUsage{}
	s.usage.Totals.TotalTokens = 9_900_000
	s.usage.Totals.Spend = 3.5

	a := s.agentState()
	if a.Tokens != "1.3k" {
		t.Fatalf("tokens: %q", a.Tokens)
	}
	if a.Spend != "$3.50" {
		t.Fatalf("spend: %q", a.Spend)
	}
}

func TestAgentStateHidesAccountTokensInConversation(t *testing.T) {
	s := &Session{started: timeNow(), chat: &chatSession{}}
	s.usage = &FxUsage{}
	s.usage.Totals.TotalTokens = 9_900_000
	s.usage.Totals.Spend = 3.5

	a := s.agentState()
	if a.Tokens != "" {
		t.Fatalf("account tokens leaked into a fresh conversation: %q", a.Tokens)
	}
	if a.Spend != "$3.50" {
		t.Fatalf("spend: %q", a.Spend)
	}
}

func TestAgentStateExitedAndAttention(t *testing.T) {
	s := &Session{started: timeNow()}
	s.exited = true
	if got := s.agentState().Status; got != "Exited" {
		t.Fatalf("status: %q", got)
	}
	s.exited, s.attention = false, true
	if got := s.agentState().Status; got != "Attention" {
		t.Fatalf("status: %q", got)
	}
}

func TestAgentStateUsesModelOverride(t *testing.T) {
	s := &Session{started: timeNow(), model: "codex/gpt-5.5"}
	s.fx = &FxStatus{Model: "zenmux/deepseek/deepseek-v4.1-flash"}
	// FX_DESKTOP_MODEL is what the session was launched with, so it wins.
	if got := s.agentState().Model; got != "codex/gpt-5.5" {
		t.Fatalf("model: %q", got)
	}
}

func TestRefreshSkipsCommandSessions(t *testing.T) {
	// Command sessions have no workspace; both loaders must no-op rather
	// than run fx or git in the process directory.
	s := &Session{}
	s.refreshFxInfo()
	if s.fx != nil || s.saved != nil {
		t.Fatalf("command session should stay empty: %+v", s)
	}
}

func TestInvalidateWithoutWindowIsSafe(t *testing.T) {
	s := &Session{}
	s.invalidate() // must not panic
}

// The window is assigned by the app loop and read by terminal callbacks on
// their own goroutine. It only stays race-free while both sides go through
// the accessors — this test fails under -race if anyone reverts to the bare
// field.
func TestWindowSnapshotIsRaceFree(t *testing.T) {
	s := &Session{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			_ = s.winSnapshot()
			s.invalidate()
		}
	}()
	for i := 0; i < 500; i++ {
		s.setWin(nil)
	}
	<-done
}

func TestSetStateWithoutWindowIsSafe(t *testing.T) {
	s := &Session{}
	s.setExited(0)
	s.setAttention()
	if !s.exited || !s.attention {
		t.Fatal("state should be set even without a window")
	}
}

func TestFormatElapsedBounds(t *testing.T) {
	if formatElapsed(-time.Second) != "00:00" {
		t.Fatal("negative durations clamp to zero")
	}
	if got := formatElapsed(0); got != "00:00" {
		t.Fatalf("zero: %q", got)
	}
}

// A failed `fx sessions` is not an empty result: the panel must keep the
// sessions it already has instead of blanking them.
func TestRefreshFxInfoKeepsSavedOnError(t *testing.T) {
	testutil.IsolateHome(t)
	s := &Session{
		coord:   NewCoordinator(),
		project: "/p/proj",
		stop:    make(chan struct{}),
	}
	s.mu.Lock()
	s.saved = []FxSession{{ID: "keep-me"}}
	s.mu.Unlock()
	// fxPath is empty, so every query fails.
	s.refreshFxInfo()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) != 1 || s.saved[0].ID != "keep-me" {
		t.Fatalf("saved sessions were blanked by a failed query: %+v", s.saved)
	}
}

// setExited must carry the exit code: "Exited" and "Exited 1" are
// different facts for the user reading the agent strip.
func TestSetExitedRecordsCode(t *testing.T) {
	s := &Session{stop: make(chan struct{}), started: timeNow()}
	s.setExited(1)
	if a := s.agentState(); a.Status != "Exited 1" {
		t.Fatalf("status = %q, want \"Exited 1\"", a.Status)
	}
	s2 := &Session{stop: make(chan struct{}), started: timeNow()}
	s2.setExited(0)
	if a := s2.agentState(); a.Status != "Exited" {
		t.Fatalf("status = %q, want \"Exited\"", a.Status)
	}
}

// A workspace read that comes back "not a repository" must be recorded as
// that and not as a failure, so the panel can say which it is rather than
// spinning on "Reading workspace…".
func TestRefreshGitRecordsNoRepository(t *testing.T) {
	isolateWithGit(t)
	s := &Session{coord: NewCoordinator(), project: t.TempDir(), stop: make(chan struct{})}
	s.refreshGit()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.git != nil {
		t.Fatal("expected no git info outside a repository")
	}
	if !errors.Is(s.gitErr, errNoRepo) {
		t.Fatalf("gitErr = %v, want errNoRepo", s.gitErr)
	}
	if got := workspaceState(s.git, s.gitErr); got != chrome.WorkspaceNotRepo {
		t.Fatalf("workspace state = %v, want %v", got, chrome.WorkspaceNotRepo)
	}
}
