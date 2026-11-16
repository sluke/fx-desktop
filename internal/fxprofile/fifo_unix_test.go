//go:build unix

package fxprofile

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

// A FIFO named prompt.md must not block the scan: the reader checks the type
// before opening (P4).
func TestProfileSpecialFileDoesNotBlock(t *testing.T) {
	if err := syscall.Mkfifo(filepath.Join(t.TempDir(), "probe"), 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	state := t.TempDir()
	dir := filepath.Join(state, "agents", "pipe")
	writeProfileFixture(t, filepath.Join(dir, "profile.json"), `{"description":"pipe"}`)
	if err := syscall.Mkfifo(filepath.Join(dir, "prompt.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan Profile, 1)
	go func() { done <- findProfile(t, state, "", "user", "pipe") }()
	select {
	case profile := <-done:
		if !strings.Contains(profile.Problem, "regular file") {
			t.Fatalf("FIFO prompt problem=%q", profile.Problem)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan blocked on a FIFO")
	}
}

// A FIFO where prompt.md belongs must be rejected before Open. The probe
// runs in an isolated child process: an accidental blocking Open then costs
// one bounded subprocess, not the whole test binary. t.TempDir cleans up.
func TestFIFOReplacementProbeIsIsolated(t *testing.T) {
	if os.Getenv("FX_FIFO_CHILD") == "1" {
		_, _, _, problem := readBounded(os.Getenv("FX_FIFO_PATH"), 4096, false, "")
		fmt.Fprintln(os.Stdout, problem)
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "prompt.md")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFIFOReplacementProbeIsIsolated$")
	cmd.Env = append(os.Environ(), "FX_FIFO_CHILD=1", "FX_FIFO_PATH="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("child blocked or failed: %v (%s)", err, out)
	}
	if !strings.Contains(string(out), "regular file") {
		t.Fatalf("FIFO was not rejected as a regular file: %q", out)
	}
}
