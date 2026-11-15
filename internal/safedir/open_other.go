//go:build !unix

package safedir

import "os"

// openDir has no O_DIRECTORY on these platforms. The pre-open Stat in Open is
// the guard against static special files, and the post-open f.Stat checks the
// object actually opened. A path swapped between those two checks is not
// claimed to be handled atomically.
func openDir(path string) (*os.File, error) {
	return os.Open(path)
}
