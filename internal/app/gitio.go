package app

import (
	"bytes"
	"context"
	"io"
	"os/exec"
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
	// Discard stderr instead of inheriting it, and do not buffer it: a chatty
	// git must not fill an unread pipe or grow without a bound.
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return gitCommandResult{err: err}
	}
	if err := cmd.Start(); err != nil {
		return gitCommandResult{err: err}
	}

	limit := int64(maxBytes) + 1
	readDone := make(chan struct {
		data []byte
		err  error
	}, 1)
	go func() {
		var buf bytes.Buffer
		_, err := io.Copy(&buf, io.LimitReader(stdout, limit))
		readDone <- struct {
			data []byte
			err  error
		}{buf.Bytes(), err}
	}()

	var raw struct {
		data []byte
		err  error
	}
	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-readDone
		_ = cmd.Wait()
		return gitCommandResult{err: ctx.Err()}
	case raw = <-readDone:
	}
	if raw.err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return gitCommandResult{err: raw.err}
	}

	truncated := len(raw.data) > maxBytes
	data := raw.data
	if truncated {
		data = data[:maxBytes]
		// Stop the child instead of draining the rest of a huge stream.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	waitErr := cmd.Wait()
	if truncated {
		return gitCommandResult{data: data, truncated: true}
	}
	if waitErr != nil {
		if len(data) == 0 {
			return gitCommandResult{err: waitErr}
		}
		return gitCommandResult{data: data, err: waitErr}
	}
	return gitCommandResult{data: data}
}