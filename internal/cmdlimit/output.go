// Package cmdlimit runs a command and keeps its output inside a byte budget.
// A stream past the budget is not decoded: the process is stopped and the
// caller gets ErrTooLarge instead of a truncated document.
package cmdlimit

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"
)

// ErrTooLarge means stdout grew past the capture budget. The returned bytes
// are not a partial document and must not be unmarshaled.
var ErrTooLarge = errors.New("command output exceeded the capture budget")

// limitedWriter keeps at most limit+1 bytes and drains everything else so the
// copying goroutine can always finish. Exceeding the limit stops the process
// when a stop callback is supplied.
type limitedWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	limit  int
	extra  bool
	onFull func()
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.extra {
		return len(p), nil
	}
	room := w.limit + 1 - w.buf.Len()
	if room < 0 {
		room = 0
	}
	keep := len(p)
	if keep > room {
		keep = room
	}
	_, _ = w.buf.Write(p[:keep])
	if keep < len(p) || w.buf.Len() > w.limit {
		w.extra = true
		if w.onFull != nil {
			w.onFull()
		}
	}
	return len(p), nil
}

func (w *limitedWriter) exceeded() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.extra
}

func (w *limitedWriter) data() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Bytes()
}

// Output runs cmd, capturing at most stdoutLimit bytes of stdout and
// stderrLimit bytes of stderr. Extra stderr is drained so a chatty stream
// cannot fill the pipe. Extra stdout stops the process and returns
// ErrTooLarge with no stdout.
//
// Stdout and stderr are attached as writers (not pipes we read ourselves) so
// exec owns the copying goroutines: WaitDelay then bounds a grandchild that
// keeps a pipe open after the direct child exits. Without that, Wait would
// close our read end and the copied bytes would be lost as a closed-pipe
// error instead of exec.ErrWaitDelay.
//
// A non-zero exit still returns the captured stdout with its error: doctor's
// exit-2 reports live in stdout and must remain decodable. WaitDelay is not a
// partial result and returns no bytes.
func Output(cmd *exec.Cmd, stdoutLimit, stderrLimit int) ([]byte, error) {
	if stdoutLimit < 1 {
		stdoutLimit = 1
	}
	if stderrLimit < 0 {
		stderrLimit = 0
	}
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 100 * time.Millisecond
	}
	out := &limitedWriter{limit: stdoutLimit}
	out.onFull = func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}
	errWriter := &limitedWriter{limit: stderrLimit}
	cmd.Stdout = out
	cmd.Stderr = errWriter
	runErr := cmd.Run()
	if out.exceeded() {
		return nil, ErrTooLarge
	}
	if runErr != nil {
		if errors.Is(runErr, exec.ErrWaitDelay) {
			return nil, runErr
		}
		return out.data(), runErr
	}
	return out.data(), nil
}

var _ io.Writer = (*limitedWriter)(nil)
