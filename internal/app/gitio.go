package app

import (
	"context"
	"os"
	"os/exec"
	"time"

	"fx-desktop/internal/cmdlimit"
)

// maxGitDiffBytes bounds raw unified-diff bytes read from git before
// classification. Output caps in the workspace package bound visible lines.
const maxGitDiffBytes = 1 << 20

// gitCommandResult is bounded git stdout together with how the read ended.
type gitCommandResult struct {
	data      []byte
	truncated bool
	err       error
}

func gitCommandOutput(ctx context.Context, dir string, maxBytes int, args ...string) gitCommandResult {
	if maxBytes <= 0 {
		maxBytes = maxGitDiffBytes
	}
	// These reads run in the background: when a window opens, every thirty
	// seconds, and when the drawer loads a file. By default `git status` and
	// `git diff` also rewrite the index to refresh its cached stat data, and
	// hold index.lock while they do; a command the agent or the user runs at
	// that moment fails on the lock. Git's advice for background callers is
	// to turn the optional locks off (git-status(1), BACKGROUND REFRESH).
	// That covers status. diff refreshes the index under its own setting, so
	// that is switched off for these reads as well.
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "diff.autoRefreshIndex=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	cmd.WaitDelay = 100 * time.Millisecond
	data, truncated, err := cmdlimit.Capture(cmd, maxBytes, 0)
	if ctx.Err() != nil {
		return gitCommandResult{err: ctx.Err()}
	}
	return gitCommandResult{data: data, truncated: truncated, err: err}
}
