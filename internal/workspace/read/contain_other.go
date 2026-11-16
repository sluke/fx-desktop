//go:build !unix

package read

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fx-desktop/internal/workspace"
)

// openContained is the non-unix fallback. It refuses a symlink whose resolved
// target leaves the real project root, then reads that path. Without openat
// and O_NOFOLLOW the final open is by path, so a concurrent replacement can
// still change the inode after the check.
func openContained(project, token string, limit int) (data []byte, tooBig bool, err error) {
	relPath, ok := workspace.ResolvePath(project, token)
	if !ok {
		return nil, false, errNotMention
	}
	root, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(relPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, errNotMention
		}
		return nil, false, err
	}
	real := relPath
	if info.Mode()&os.ModeSymlink != 0 {
		real, err = filepath.EvalSymlinks(relPath)
		if err != nil {
			return nil, false, &Refusal{fmt.Sprintf("@%s could not be read inside this project", token)}
		}
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, false, &Refusal{fmt.Sprintf("@%s is outside this project", token)}
	}
	file, err := os.Open(real)
	if err != nil {
		return nil, false, err
	}
	return readLimited(file, limit)
}
