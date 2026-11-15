// Package safedir opens a directory for batched enumeration without letting
// a non-directory (notably a FIFO) block the caller in os.Open.
//
// A pre-open Stat rejects static special files on every platform; on Unix the
// open itself also carries O_DIRECTORY|O_NONBLOCK, so a path replaced between
// the check and the open still fails instead of waiting for a writer. The
// descriptor is verified after opening. None of this makes a path swap
// atomic, and a network filesystem syscall is not promised to honour a
// context cancellation in real time (S6).
package safedir

import (
	"errors"
	"os"
)

// ErrNotDirectory means the path cannot be enumerated as a directory. Callers
// surface it as a scan problem rather than treating the root as empty.
var ErrNotDirectory = errors.New("not a readable directory")

// Open returns a descriptor for batched ReadDir calls. It refuses anything
// that is not a directory both before and after the open.
func Open(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrNotDirectory
	}
	f, err := openDir(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.IsDir() {
		_ = f.Close()
		return nil, ErrNotDirectory
	}
	return f, nil
}
