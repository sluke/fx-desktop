//go:build unix

package acp

// Round-9 regression: a fake "fx" only.

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The direct child exits, but a background process it started still holds the
// stdout pipe. The client sets cmd.WaitDelay = 100ms "to bound inherited output
// pipes on process shutdown", so the death of fx must be noticed (Done) and,
// after Close, the child must be reported reaped (Exited) within a bound.
func TestRegressionR9_ExitedChildWithInheritedStdoutIsNoticed(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "holder.pid")
	child := stubChild(t, "/bin/sleep 30 &\necho $! > '"+pidFile+"'\nexit 0")
	c, err := Start(context.Background(), child, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	holder := 0
	t.Cleanup(func() {
		// Hard cleanup: kill the pipe holder, then the reader sees EOF and the
		// reaping goroutine finishes.
		if holder > 0 {
			_ = syscall.Kill(holder, syscall.SIGKILL)
		}
		c.Close()
		select {
		case <-c.Exited():
		case <-time.After(5 * time.Second):
			t.Log("probe cleanup: reaping goroutine still running")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				holder = pid
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if holder == 0 {
		t.Fatal("the fake fx never reported its pipe holder")
	}

	var problems []string
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		problems = append(problems, "Done() not closed 2s after the fx process exited: the conversation never learns fx is gone")
	}
	c.Close()
	select {
	case <-c.Exited():
	case <-time.After(2 * time.Second):
		problems = append(problems, "Exited() not closed 2s after Close(): cmd.Wait is only called after the stdout reader hits EOF, so WaitDelay=100ms is never armed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Shutdown(ctx, 100*time.Millisecond); err != nil {
		problems = append(problems, "Shutdown reports the already-dead child as unconfirmed: "+err.Error())
	}
	if len(problems) > 0 {
		t.Fatalf("direct child exited, pipe held by pid %d:\n - %s", holder, strings.Join(problems, "\n - "))
	}
}
