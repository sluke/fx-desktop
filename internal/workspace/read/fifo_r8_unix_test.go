//go:build unix

package read

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRegressionR8MentionFIFO(t *testing.T) {
	if root := os.Getenv("FX_R8_FIFO_ROOT"); root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		got, err := Resolve(ctx, root, "@"+os.Getenv("FX_R8_FIFO_TOKEN"))
		if err != nil || len(got) != 0 {
			t.Fatalf("FIFO result: mentions=%v error=%v", got, err)
		}
		return
	}
	for _, token := range []string{"pipe", "link", "pipe/child"} {
		t.Run(token, func(t *testing.T) {
			root := t.TempDir()
			if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("pipe", filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRegressionR8MentionFIFO$")
			cmd.Env = append(os.Environ(), "FX_R8_FIFO_ROOT="+root, "FX_R8_FIFO_TOKEN="+token)
			cmd.WaitDelay = 100 * time.Millisecond
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("static FIFO blocked Resolve despite 50ms context; helper killed at 2s: %s", out)
			}
			if err != nil {
				t.Fatalf("helper: %v %s", err, out)
			}
		})
	}
}
