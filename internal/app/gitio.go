package app

import (
	"context"
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
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.WaitDelay = 100 * time.Millisecond
	data, truncated, err := cmdlimit.Capture(cmd, maxBytes, 0)
	if ctx.Err() != nil {
		return gitCommandResult{err: ctx.Err()}
	}
	return gitCommandResult{data: data, truncated: truncated, err: err}
}
