//go:build unix

package fxprofile

// S6: a static FIFO at <stateRoot>/agents must be reported, never waited on.
// The probe runs in an isolated child process with a hard parent deadline.
// Migrated from TestReviewR6ProfileRootFIFO.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProfileRootFIFODoesNotBlockScan(t *testing.T) {
	if root := os.Getenv("FX_R6_PROFILE_FIFO_HELPER"); root != "" {
		normal := ScanContext(context.Background(), root, "")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		canceled := ScanContext(ctx, root, "")
		fmt.Printf("normal: partial=%v problems=%d profiles=%d; canceled: partial=%v problems=%d\n",
			normal.Partial, len(normal.Problems), len(normal.Profiles),
			canceled.Partial, len(canceled.Problems))
		return
	}
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "agents"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProfileRootFIFODoesNotBlockScan$")
	cmd.Env = append(os.Environ(), "FX_R6_PROFILE_FIFO_HELPER="+root)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("static FIFO agents root blocked Open past the deadline: %s", out)
	}
	if err != nil {
		t.Fatalf("helper failed: %v (%s)", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "normal: partial=false problems=1 profiles=2") {
		t.Fatalf("FIFO root was not reported as one unreadable root: %q", text)
	}
	if !strings.Contains(text, "canceled: partial=true") {
		t.Fatalf("pre-cancelled FIFO scan did not report partial: %q", text)
	}
}
