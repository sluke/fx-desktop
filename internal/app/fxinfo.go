package app

import (
	"context"
	"time"

	"fx-desktop/internal/app/presenter"
	"fx-desktop/internal/fxcli"
)

// fxTimeout bounds every read-only fx query the GUI makes. Interactive
// sessions are unaffected: these are the short --json inspections.
const fxTimeout = 20 * time.Second

// The fx JSON wire shapes are the adapter's. The app keeps aliases so a call
// site names the fact it is reading, not the package that decoded it.
type FxStatus = fxcli.Status
type FxMode = fxcli.Mode
type FxDoctor = fxcli.Doctor
type FxDoctorCheck = fxcli.DoctorCheck
type FxSlackStatus = fxcli.SlackStatus
type FxSessionsConvert = fxcli.SessionsConvert
type FxModels = fxcli.Models
type FxModel = fxcli.Model
type FxMCP = fxcli.MCP
type FxMCPList = fxcli.MCPList
type FxMCPServer = fxcli.MCPServer
type FxMCPTool = fxcli.MCPTool
type FxMCPShow = fxcli.MCPShow
type FxUsage = fxcli.Usage
type FxSession = fxcli.Session
type FxSessionDetail = fxcli.SessionDetail
type FxSessionTurn = fxcli.SessionTurn
type FxPermissions = fxcli.Permissions
type FxAuth = fxcli.Auth

// fxJSON runs fx with args plus --json in dir and decodes the result. The
// argv and the decoding belong to the adapter; this is the app's name for it.
func fxJSON(ctx context.Context, fxPath, dir string, dst any, args ...string) error {
	return fxcli.Run(ctx, fxPath, dir, dst, args...)
}

// withTimeout gives a query its own deadline.
func withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return fxcli.WithTimeout(parent)
}

func fxStatus(ctx context.Context, fxPath, dir string) (*FxStatus, error) {
	return fxcli.ReadStatus(ctx, fxPath, dir)
}

func fxUsage(ctx context.Context, fxPath, dir string) (*FxUsage, error) {
	return fxcli.ReadUsage(ctx, fxPath, dir)
}

func fxPermissions(ctx context.Context, fxPath, dir string) (*FxPermissions, error) {
	return fxcli.ReadPermissions(ctx, fxPath, dir)
}

func fxModels(ctx context.Context, fxPath, dir string) (*FxModels, error) {
	return fxcli.ReadModels(ctx, fxPath, dir)
}

func fxDoctor(ctx context.Context, fxPath, dir string) (*FxDoctor, error) {
	return fxcli.ReadDoctor(ctx, fxPath, dir)
}

func fxSlackStatus(ctx context.Context, fxPath, dir string) (*FxSlackStatus, error) {
	return fxcli.ReadSlackStatus(ctx, fxPath, dir)
}

func fxSessionsConvert(ctx context.Context, fxPath, dir string) (*FxSessionsConvert, error) {
	return fxcli.ReadSessionsConvert(ctx, fxPath, dir)
}

func fxSessions(ctx context.Context, fxPath, dir string) ([]FxSession, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	page, err := readSessionPage(ctx, fxPath, dir, false, 5, "")
	if err != nil {
		return nil, err
	}
	return page.Sessions[:min(5, len(page.Sessions))], nil
}

// readFxAuth asks fx itself. A failure returns the zero value, which renders
// as "nothing known" rather than as "signed out".
func readFxAuth(fxPath, dir string) FxAuth { return fxcli.ReadAuth(fxPath, dir) }

// fxClearHistory was removed with the "Clear history" button: fx 0.0.14 has no
// way to delete saved sessions (`fx sessions` has no clear flag and ACP
// `session/remove` answers -32601), so the GUI stops offering the action
// (see the design ledger, §A.6.2).

// compactCount and relTime render a count and a timestamp for the UI; the
// formatting belongs to the presenter.
func compactCount(n int64) string { return presenter.CompactCount(n) }

func relTime(ms int64, now time.Time) string { return presenter.RelTime(ms, now) }
