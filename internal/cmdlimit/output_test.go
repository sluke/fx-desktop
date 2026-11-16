package cmdlimit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, name string, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func catCmd(t *testing.T, path string) *exec.Cmd {
	t.Helper()
	return exec.Command("/bin/cat", path)
}

func TestOutputBudgetBoundary(t *testing.T) {
	const limit = 4096
	for _, size := range []int{limit - 1, limit, limit + 1} {
		path := writeFixture(t, "payload", size)
		got, err := Output(catCmd(t, path), limit, 1024)
		if size <= limit {
			if err != nil || len(got) != size {
				t.Fatalf("%d bytes within budget: err=%v len=%d", size, err, len(got))
			}
			continue
		}
		if !errors.Is(err, ErrTooLarge) || len(got) != 0 {
			t.Fatalf("%d bytes over budget: err=%v len=%d", size, err, len(got))
		}
	}
}

func TestOutputKeepsStdoutOnNonzeroExit(t *testing.T) {
	// Nonzero exit preserves complete stdout for the caller to interpret.
	cmd := exec.Command("/bin/sh", "-c", "printf report; exit 2")
	got, err := Output(cmd, 1024, 1024)
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected an exit error, got %v", err)
	}
	if string(got) != "report" {
		t.Fatalf("stdout lost on non-zero exit: %q", got)
	}
}

func TestOutputDrainsChattyStderr(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "printf noise >&2; printf ok")
	cmd.Stderr = nil
	got, err := Output(cmd, 1024, 16)
	if err != nil || string(got) != "ok" {
		t.Fatalf("stderr noise changed stdout: %q err=%v", got, err)
	}
}

func TestOutputCancellationIsNotASuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := writeFixture(t, "payload", 32)
	cmd := exec.CommandContext(ctx, "/bin/cat", path)
	_, err := Output(cmd, 1024, 1024)
	if err == nil {
		t.Fatal("a cancelled command returned success")
	}
}
