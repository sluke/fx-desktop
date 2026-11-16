//go:build unix

package cmdlimit

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A direct child exits while its helper still owns stdout. exec must bound
// that inherited pipe with WaitDelay, and the bytes read before the deadline
// are not a complete result.
func TestOutputWaitDelayIsNotAPartialResult(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "wrapper")
	script := "#!/bin/sh\n" +
		"/bin/sleep 30 &\n" +
		"printf '%s\\n' \"$!\" > \"$0.pid\"\n" +
		"printf 'partial'\n" +
		"exit 0\n"
	if err := os.WriteFile(exe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.WaitDelay = 100 * time.Millisecond
	got, err := Output(cmd, 1024, 1024)

	// Reclaim the helper regardless of the assertions below.
	if data, readErr := os.ReadFile(exe + ".pid"); readErr == nil {
		if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
			if holder, findErr := os.FindProcess(pid); findErr == nil {
				defer func() { _ = holder.Kill() }()
			}
		}
	}

	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("inherited pipe was not bounded by WaitDelay: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("partial stdout published on WaitDelay: %q", got)
	}
}
