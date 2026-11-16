//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO named SKILL.md must be skipped, not opened: opening a pipe waits for
// a writer (P4).
func TestScanSkillsSkipsSpecialFiles(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".fx", "skills", "pipe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "SKILL.md"), 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	done := make(chan skillScanResult, 1)
	go func() { done <- scanSkills(context.Background(), workspace, "", "") }()
	select {
	case result := <-done:
		if len(result.Skills) != 0 || result.Partial {
			t.Fatalf("special file became a skill: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("skill scan blocked on a FIFO")
	}
}
