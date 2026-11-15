//go:build unix

package safedir

import (
	"os"
	"syscall"
)

// openDir asks the kernel for a directory only. O_DIRECTORY makes a FIFO (or
// any other non-directory that replaced the path after the pre-check) fail
// with ENOTDIR instead of waiting for a peer; O_NONBLOCK is a second guard
// for platforms whose open would otherwise still wait. Go's os.OpenFile sets
// O_CLOEXEC on its own.
func openDir(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
}
