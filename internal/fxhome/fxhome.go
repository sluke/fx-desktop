// Package fxhome resolves the fx CLI state directory. It is not the
// fx-desktop preferences directory: FX_HOME moves rules, sessions and skills
// together, and a bad value must fail instead of falling back to another store.
package fxhome

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Root is FX_HOME when it is set, otherwise ~/.fx. Both candidates go
// through the same validation: the default is still a state root, so a file,
// a forbidden alias or an unreadable ancestor must fail there too (H5).
// A set value must name an absolute folder that is neither the filesystem
// root nor the home folder: fx keeps its state private to the user, and
// treating those as a state root would let a read or delete reach the whole
// disk (H4).
func Root() (string, error) {
	// An empty FX_HOME means "not set", which is how the environment reports
	// a variable that was exported empty. A non-empty value is used exactly
	// as written: leading or trailing spaces are part of the path, and a
	// value that is only spaces is not an absolute path.
	if v, ok := os.LookupEnv("FX_HOME"); ok && v != "" {
		return resolve(v, "FX_HOME")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return resolve(filepath.Join(home, ".fx"), "default state root")
}

// resolve validates one state-root candidate. A folder that does not exist
// yet is legal — fx creates it on first use — but it must not be a file, and
// its existing ancestors must not put it under a forbidden root. source only
// names where the candidate came from, so an error says which one to fix.
func resolve(value, source string) (string, error) {
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path, got %q", source, value)
	}
	root := filepath.Clean(value)
	if isFilesystemRoot(root) {
		return "", fmt.Errorf("%s must not be the filesystem root, got %q", source, value)
	}
	home, homeResolved := homePaths()
	if home != "" && samePath(root, home) {
		return "", fmt.Errorf("%s must not be the home folder, got %q", source, value)
	}

	// A link that names "/" or the home folder is still that folder. Compare
	// the resolved path as well as the spelling, so an alias cannot smuggle
	// a forbidden root past the check above.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		if isFilesystemRoot(resolved) {
			return "", fmt.Errorf("%s must not reach the filesystem root through a link, got %q", source, value)
		}
		if homeResolved != "" && samePath(resolved, homeResolved) {
			return "", fmt.Errorf("%s must not reach the home folder through a link, got %q", source, value)
		}
	}

	info, err := os.Stat(root)
	switch {
	case err == nil && !info.IsDir():
		return "", fmt.Errorf("%s must be a folder, got a file at %q", source, value)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		// Permission denied, a file in place of a parent folder, and similar
		// failures are reported rather than silently replaced by another
		// store.
		return "", fmt.Errorf("%s %q: %w", source, value, err)
	}
	return root, nil
}

// homePaths returns the home folder and, when it exists, its resolved form.
// FX_HOME must work with no HOME at all, so a missing home is not an error
// here: there is simply nothing to compare against.
func homePaths() (string, string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", ""
	}
	home = filepath.Clean(home)
	resolved := home
	if r, err := filepath.EvalSymlinks(home); err == nil {
		resolved = r
	}
	return home, resolved
}

// isFilesystemRoot reports whether p has no parent: "/" on Unix, a volume
// root such as `C:\` on Windows.
func isFilesystemRoot(p string) bool {
	return filepath.Dir(p) == p
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
