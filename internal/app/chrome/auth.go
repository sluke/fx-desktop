package chrome

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// AuthNotice is what fx reports about being able to reach a provider.
//
// The words are fx's own: ACP advertises no way to sign in (fx 0.0.14 returns
// an empty authMethods, §12.1), so the one honest thing the GUI can do is
// repeat what fx says and offer the CLI that can. Paraphrasing an auth
// condition the GUI cannot observe would be a guess dressed up as a status.
type AuthNotice struct {
	// Help is fx's explanation of what is missing (its auth_help). Empty
	// means fx is satisfied and there is nothing to guide.
	Help string
	// Auth is fx's verbatim description of the credential in use.
	Auth string
	// Providers are the providers fx reports as connected.
	Providers []string
}

// NeedsSignIn reports whether fx says something is missing. The signal is the
// presence of fx's own help text rather than a value the GUI recognises, so a
// new auth state fx adds later still gets shown instead of silently ignored.
func (n AuthNotice) NeedsSignIn() bool { return strings.TrimSpace(n.Help) != "" }

// AuthSummary is the one-line state shown when nothing is missing.
func (n AuthNotice) AuthSummary() string {
	auth := strings.TrimSpace(n.Auth)
	if auth == "" {
		auth = "status unavailable"
	}
	if len(n.Providers) == 0 {
		return "Authentication: " + auth
	}
	return fmt.Sprintf("Authentication: %s · %s", auth, strings.Join(n.Providers, ", "))
}

// WarnColor is the attention tone, for content the app draws itself.
func WarnColor(c *ui.Context) ui.Color { return palette(c.Theme()).Warn }

// authCardWidth matches the width of the center page it sits above, so the
// guidance lines up with the page rather than floating wider than it.
const authCardWidth = 560

// AuthNoticeCard draws fx's sign-in guidance: what fx says is missing, and
// the only action the GUI can honestly offer — open the CLI that can sign in.
func AuthNoticeCard(c *ui.Context, n AuthNotice, onSignIn func()) {
	if !n.NeedsSignIn() {
		return
	}
	p := palette(c.Theme())
	ui.Row(c).FillWidth().MaxWidth(authCardWidth).AlignItems(ui.Start).Gap(Space5).Padding(Space7, Space8).
		Background(p.Surface).Radius(RadiusControl).
		BorderWidth(1).BorderColor(p.Warn).
		Children(func() {
			ui.Box(c).Width(8).Height(8).Radius(RadiusChip).Background(p.Warn).Shrink(0).Margin(4, 0, 0, 0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(Space4).Children(func() {
				strongText(c, "Sign in to fx", p.InkStrong)
				// fx's sentence, exactly as fx wrote it: no GUI paraphrase
				// can know which of the ways in the user still has.
				ui.Text(c, strings.TrimSpace(n.Help)).FontSize(TypeInput).TextColor(p.InkMuted).
					LineHeight(1.5).Wrap()
				ui.Row(c).AlignItems(ui.Center).Gap(Space5).Children(func() {
					// The button's "fx" and the CLI's own help above it
					// say where sign-in goes; a caption under the button
					// would explain the button instead.
					PrimaryButton(c, "Sign in with fx…", onSignIn)
				})
			})
		})
}
