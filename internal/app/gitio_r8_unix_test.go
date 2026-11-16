//go:build unix

package app

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRegressionR8GitWaitDelayBeforeReadCompletes(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "git")
	script := "#!/bin/sh\n/bin/sleep 30 &\nprintf '%s' \"$!\" > \"$0.pid\"\nprintf 'partial'\nexit 0\n"
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan gitCommandResult, 1)
	go func() { done <- gitCommandOutput(ctx, dir, 1024, "status") }()
	var pid int
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		data, _ := os.ReadFile(exe + ".pid")
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("helper did not signal startup")
	}
	child, _ := os.FindProcess(pid)
	defer child.Kill()
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatalf("incomplete pipe returned success: %q", r.data)
		}
	case <-time.After(time.Second):
		t.Error("100ms WaitDelay did not bound inherited stdout; still blocked 1s after helper startup")
		_ = child.Kill()
		select {
		case r := <-done:
			t.Logf("after cleanup data=%q err=%v", r.data, r.err)
		case <-time.After(3 * time.Second):
			t.Fatal("runner did not drain")
		}
	}
}
