// Package cmdlimit runs a command and keeps its output inside a byte budget.
// A stream past the budget is not decoded: the process is stopped and the
// caller gets ErrTooLarge instead of a truncated document.
package cmdlimit

import (
	"bytes"
	"errors"
	"io"
	"os"
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
// A complete non-zero exit retains stdout for callers such as doctor. An
// incomplete stream returns no document, regardless of the exit status.
func Output(cmd *exec.Cmd, stdoutLimit, stderrLimit int) ([]byte, error) {
	data, truncated, err := Capture(cmd, stdoutLimit, stderrLimit)
	if truncated {
		return nil, ErrTooLarge
	}
	return data, err
}

// Capture also returns the bounded prefix when stdout exceeds its budget.
// Callers may use that prefix only when they explicitly support truncation.
func Capture(cmd *exec.Cmd, stdoutLimit, stderrLimit int) ([]byte, bool, error) {
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
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, false, err
	}
	defer reader.Close()
	cmd.Stdout = writer
	cmd.Stderr = &limitedWriter{limit: stderrLimit}
	if err := cmd.Start(); err != nil {
		writer.Close()
		return nil, false, err
	}
	writer.Close()
	readDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(out, reader)
		readDone <- err
	}()
	runErr := cmd.Wait()
	timer := time.NewTimer(cmd.WaitDelay)
	defer timer.Stop()
	var readErr error
	select {
	case readErr = <-readDone:
	case <-timer.C:
		reader.Close()
		<-readDone
		readErr = exec.ErrWaitDelay
	}
	if out.exceeded() {
		return out.data()[:stdoutLimit], true, nil
	}
	if readErr != nil || errors.Is(runErr, exec.ErrWaitDelay) {
		return nil, false, errors.Join(runErr, readErr)
	}
	return out.data(), false, runErr
}

var _ io.Writer = (*limitedWriter)(nil)
