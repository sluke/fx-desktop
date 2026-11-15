//go:build unix

package app

// S6: a static FIFO at a skill discovery root must be reported, never
// waited on. The probe runs in an isolated child process with a hard parent
// deadline: if Open ever blocks again, the cost is one bounded subprocess,
// not a hung test binary. Migrated from TestReviewR6SkillRootFIFO.

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

func TestSkillRootFIFODoesNotBlockScan(t *testing.T) {
	if root := os.Getenv("FX_R6_SKILL_FIFO_HELPER"); root != "" {
		// No deadline here: the open itself must return. Cancellation is a
		// separate contract, checked below with a pre-cancelled context.
		normal := scanSkills(context.Background(), root, "", "")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		canceled := scanSkills(ctx, root, "", "")
		fmt.Printf("normal: partial=%v problems=%d; canceled: partial=%v problems=%d\n",
			normal.Partial, len(normal.Problems), canceled.Partial, len(canceled.Problems))
		return
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".fx")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "skills"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSkillRootFIFODoesNotBlockScan$")
	cmd.Env = append(os.Environ(), "FX_R6_SKILL_FIFO_HELPER="+root)
	cmd.WaitDelay = 100 * time.Millisecond
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("static FIFO skill root blocked Open past the deadline: %s", out)
	}
	if err != nil {
		t.Fatalf("helper failed: %v (%s)", err, out)
	}
	text := string(out)
	if !strings.Contains(text, "normal: partial=false problems=1") {
		t.Fatalf("FIFO root was not reported as one unreadable root: %q", text)
	}
	if !strings.Contains(text, "canceled: partial=true") {
		t.Fatalf("pre-cancelled FIFO scan did not report partial: %q", text)
	}
}
