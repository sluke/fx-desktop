//go:build unix

package cmdlimit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Run reports completeness on its own. exec's ErrWaitDelay is only returned
// when the process had no other error, so a non-zero exit hid a stream that
// never reached EOF.
func TestRegressionR9_RunReportsCompletenessApartFromExit(t *testing.T) {
	for _, code := range []int{0, 1, 2} {
		for _, held := range []bool{false, true} {
			name := "exit" + strconv.Itoa(code)
			if held {
				name += "/pipe-held"
			} else {
				name += "/eof"
			}
			t.Run(name, func(t *testing.T) {
				pidFile := filepath.Join(t.TempDir(), "holder.pid")
				script := "printf out; printf err >&2; "
				if held {
					script += "/bin/sleep 30 & echo $! > '" + pidFile + "'; "
				}
				script += "exit " + strconv.Itoa(code)
				t.Cleanup(func() {
					if data, err := os.ReadFile(pidFile); err == nil {
						if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 1 {
							_ = syscall.Kill(pid, syscall.SIGKILL)
						}
					}
				})
				cmd := exec.Command("/bin/sh", "-c", script)
				cmd.WaitDelay = 100 * time.Millisecond
				var stdout, stderr bytes.Buffer
				start := time.Now()
				complete, err := Run(cmd, &stdout, &stderr)
				if elapsed := time.Since(start); elapsed > 5*time.Second {
					t.Fatalf("Run took %v: the held pipe was not bounded", elapsed)
				}
				if complete == held {
					t.Errorf("complete = %v with held=%v", complete, held)
				}
				var exit *exec.ExitError
				switch {
				case code == 0 && err != nil:
					t.Errorf("exit 0 returned %v", err)
				case code != 0 && (!errors.As(err, &exit) || exit.ExitCode() != code):
					t.Errorf("exit %d returned %v", code, err)
				}
				if stdout.String() != "out" || stderr.String() != "err" {
					t.Errorf("streams = %q / %q", stdout.String(), stderr.String())
				}
			})
		}
	}
}

func TestRegressionR9_RunStartFailureAndCancel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	complete, err := Run(exec.Command(filepath.Join(t.TempDir(), "missing")), &stdout, &stderr)
	if err == nil || !complete {
		t.Fatalf("a command that never started: complete=%v err=%v", complete, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "printf ready; exec /bin/sleep 30")
	cmd.WaitDelay = 100 * time.Millisecond
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err = Run(cmd, &stdout, &stderr)
	if err == nil {
		t.Fatal("a cancelled command returned no error")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("cancellation took %v", elapsed)
	}
}
