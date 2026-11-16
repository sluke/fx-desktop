//go:build unix

package read

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestListFilesDoesNotPublishAfterCancel(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := filepath.Join(dir, "pid")
	script := "#!/bin/sh\n/bin/sleep 30 &\nprintf '%s\\n' \"$!\" > '" + pid + "'\nprintf 'fixture.txt\\0'\nexit 0\n"
	t.Cleanup(func() {
		data, _ := os.ReadFile(pid)
		childPID, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if childPID > 0 {
			if child, err := os.FindProcess(childPID); err == nil {
				_ = child.Kill()
			}
		}
	})
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := ListFiles(ctx, dir)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pid); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ListFiles returned a file list after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListFiles did not return within 2s of cancellation")
	}
}
