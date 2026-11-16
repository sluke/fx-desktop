//go:build unix

package fxcli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRegressionR8DoctorIncompleteNonzeroStream(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "fx")
	script := "#!/bin/sh\n/bin/sleep 30 &\nprintf '%s' \"$!\" > \"$0.pid\"\nprintf '%s' '{\"checks\":[{\"name\":\"fixture\",\"status\":\"fail\"}]}'\nexit 1\n"
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	defer func() {
		data, _ := os.ReadFile(exe + ".pid")
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		if pid > 0 {
			p, _ := os.FindProcess(pid)
			_ = p.Kill()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	report, err := ReadDoctor(ctx, exe, dir)
	t.Logf("elapsed=%v report=%+v err=%v", time.Since(start), report, err)
	if err == nil {
		t.Fatal("doctor accepted stdout without EOF after inherited-pipe wait was cut short")
	}
}
