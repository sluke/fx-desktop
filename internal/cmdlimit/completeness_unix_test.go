//go:build unix

package cmdlimit

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOutputDistinguishesExitFromEOF(t *testing.T) {
	for _, code := range []int{0, 1, 2} {
		for _, inherited := range []bool{false, true} {
			t.Run(fmt.Sprintf("exit%d/inherited%t", code, inherited), func(t *testing.T) {
				pidFile := filepath.Join(t.TempDir(), "pid")
				script := "printf report; "
				if inherited {
					script += "/bin/sleep 30 & printf '%s' \"$!\" > \"$1\"; "
				}
				script += fmt.Sprintf("exit %d", code)
				cmd := exec.Command("/bin/sh", "-c", script, "fixture", pidFile)
				cmd.WaitDelay = 100 * time.Millisecond
				t.Cleanup(func() {
					b, _ := os.ReadFile(pidFile)
					pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
					if pid > 0 {
						if child, err := os.FindProcess(pid); err == nil {
							_ = child.Kill()
						}
					}
				})
				out, err := Output(cmd, 1024, 1024)
				if inherited {
					if !errors.Is(err, exec.ErrWaitDelay) || len(out) != 0 {
						t.Fatalf("incomplete output = %q, error = %v", out, err)
					}
					return
				}
				if string(out) != "report" {
					t.Fatalf("complete output = %q", out)
				}
				if code == 0 && err != nil {
					t.Fatal(err)
				}
				if code != 0 {
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != code {
						t.Fatalf("exit error = %v", err)
					}
				}
			})
		}
	}
}
