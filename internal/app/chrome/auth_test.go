package chrome

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// fx is the only source of truth about signing in: ACP advertises no auth
// method (§12.1), so the shell repeats fx's own sentence instead of writing
// one of its own.
func TestAuthNoticeSpeaksOnlyWhenFxDoes(t *testing.T) {
	quiet := AuthNotice{Auth: "configured provider", Providers: []string{"magpie", "codex"}}
	if quiet.NeedsSignIn() {
		t.Fatal("a satisfied fx must not be reported as needing sign-in")
	}
	if got := quiet.AuthSummary(); !strings.Contains(got, "configured provider") || !strings.Contains(got, "magpie, codex") {
		t.Fatalf("summary: %q", got)
	}
	// fx's help text is the signal. The GUI does not keep its own list of
	// "bad" auth values, so a state fx adds later still reaches the user.
	loud := AuthNotice{Auth: "missing", Help: "fx needs access to Vercel AI Gateway. Run fx login to sign in."}
	if !loud.NeedsSignIn() {
		t.Fatal("fx's help text is what makes this actionable")
	}
	if strings.TrimSpace(AuthNotice{}.AuthSummary()) == "" {
		t.Fatal("an unknown state still has to render as something")
	}
}

// A start screen is where a signed-out user lands, so the guidance stands at
// the top of it, and the one button opens the CLI that can actually sign in.
func TestCenterPageShowsSignInGuidance(t *testing.T) {
	h, _ := testHost()
	h.CenterOnly = true
	const help = "fx needs access to Vercel AI Gateway. Run fx login to sign in, fx setup to use an API key, or set AI_GATEWAY_API_KEY."
	h.Auth = AuthNotice{Auth: "missing", Help: help}
	signedIn := 0
	h.Actions.SignIn = func() { signedIn++ }

	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, help, "Sign in with fx…")
	if err := ts.Click("Sign in with fx…"); err != nil {
		t.Fatal(err)
	}
	if signedIn != 1 {
		t.Fatalf("the button must open the sign-in flow, calls=%d", signedIn)
	}

	// Nothing is missing: no card, and no inventing a problem.
	h.Auth = AuthNotice{Auth: "configured provider", Providers: []string{"magpie"}}
	ts.Frame()
	mustNotHave(t, ts, "Sign in with fx…", help)
}

// The palette row is only offered where it can be run: a host that cannot
// open the CLI must not list it.
func TestPaletteOffersSignInOnlyWhenWired(t *testing.T) {
	h, sh := testHost()
	sh.PaletteOpen = true
	h.Actions.SignIn = func() {}
	ts := ui.NewTester(func(c *ui.Context) { Frame(c, h) }, 1280, 800)
	mustHave(t, ts, "fx: Sign in")

	h.Actions.SignIn = nil
	ts.Frame()
	mustNotHave(t, ts, "fx: Sign in")
}

// AuthNoticeCard is also usable on its own, without a shell around it.
func TestAuthNoticeCardStandsAlone(t *testing.T) {
	ts := ui.NewTester(func(c *ui.Context) {
		AuthNoticeCard(c, AuthNotice{Help: "Run fx login."}, func() {})
	}, 800, 300)
	mustHave(t, ts, "Sign in to fx", "Run fx login.", "Sign in with fx…")
}
