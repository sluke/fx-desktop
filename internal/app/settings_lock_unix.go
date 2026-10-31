//go:build !windows

package app

import (
	"os"
	"syscall"
)

// lockSettingsFile takes the same advisory lock file fx uses while it writes
// ~/.fx/settings.json. The lock is held until the returned function runs.
func lockSettingsFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
