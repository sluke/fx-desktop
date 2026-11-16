//go:build unix

package read

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"fx-desktop/internal/workspace"
)

// openContained reads token only when the inode that is read stays inside the
// project's real root. An external symlink is a *Refusal, not a skipped
// mention. A missing name stays prose (errNotMention). A dangling or cyclic
// link is a *Refusal.
//
// Each path component is opened with O_NOFOLLOW from the parent directory
// descriptor, and the bytes are read from that descriptor. Following an
// intermediate symlink resolves the target and reopens that directory by its
// real path; that reopen is the remaining time-of-check gap. A target outside
// the real project root is refused instead of followed.
func openContained(project, token string, limit int) (data []byte, tooBig bool, err error) {
	relPath, ok := workspace.ResolvePath(project, token)
	if !ok {
		return nil, false, errNotMention
	}
	rel, err := filepath.Rel(project, relPath)
	if err != nil || !inside(rel) {
		return nil, false, errNotMention
	}
	root, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, false, err
	}
	dir, err := os.Open(root)
	if err != nil {
		return nil, false, err
	}
	defer dir.Close()
	return walkFrom(root, root, int(dir.Fd()), false, strings.Split(rel, string(filepath.Separator)), token, limit)
}

func walkFrom(root, current string, dirfd int, ownFD bool, parts []string, token string, limit int) ([]byte, bool, error) {
	defer func() {
		if ownFD {
			syscall.Close(dirfd)
		}
	}()
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, false, errNotMention
		}
		last := i == len(parts)-1
		fd, openErr := unix.Openat(dirfd, part, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			if errors.Is(openErr, syscall.ELOOP) || errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, syscall.EMLINK) {
				return followLink(root, current, dirfd, part, parts[i+1:], token, limit)
			}
			if errors.Is(openErr, syscall.ENOENT) {
				return nil, false, errNotMention
			}
			return nil, false, openErr
		}
		if last {
			return readLimited(os.NewFile(uintptr(fd), part), limit)
		}
		var stat syscall.Stat_t
		if err := syscall.Fstat(fd, &stat); err != nil {
			syscall.Close(fd)
			return nil, false, err
		}
		if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			syscall.Close(fd)
			return nil, false, errNotMention
		}
		if ownFD {
			syscall.Close(dirfd)
			ownFD = false
		}
		dirfd = fd
		ownFD = true
		current = filepath.Join(current, part)
	}
	return nil, false, errNotMention
}

func followLink(root, current string, dirfd int, name string, rest []string, token string, limit int) ([]byte, bool, error) {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(dirfd, name, buf)
	if err != nil {
		return nil, false, &Refusal{fmt.Sprintf("@%s could not be read inside this project", token)}
	}
	target := string(buf[:n])
	joined := target
	if !filepath.IsAbs(target) {
		joined = filepath.Join(current, target)
	}
	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return nil, false, &Refusal{fmt.Sprintf("@%s could not be read inside this project", token)}
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || !inside(rel) {
		return nil, false, &Refusal{fmt.Sprintf("@%s is outside this project", token)}
	}
	if len(rest) == 0 {
		file, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return nil, false, &Refusal{fmt.Sprintf("@%s is outside this project", token)}
		}
		return readLimited(file, limit)
	}
	info, err := os.Lstat(real)
	if err != nil || !info.IsDir() {
		return nil, false, errNotMention
	}
	// Reopen the resolved directory by its real path. A concurrent replacement
	// of that path can still change the directory that is entered; the next
	// component is opened with O_NOFOLLOW and checked against root again.
	fd, err := unix.Open(real, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false, &Refusal{fmt.Sprintf("@%s could not be read inside this project", token)}
	}
	return walkFrom(root, real, fd, true, rest, token, limit)
}

func inside(rel string) bool {
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
