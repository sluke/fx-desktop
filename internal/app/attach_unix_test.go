//go:build unix

package app

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"fx-desktop/internal/app/chrome"
)

// N5: a FIFO (or any non-regular file) is refused without being opened.
// Opening a FIFO for reading blocks until a writer appears, so the old
// stat-then-ReadFile path could hang the worker forever.
func TestAttachRefusesANonRegularFileWithoutOpeningIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.png")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := readImageFile(context.Background(), path, chrome.MaxImageBytes)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("a FIFO was read or refused for the wrong reason: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO blocked the worker")
	}
}
