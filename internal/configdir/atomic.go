package configdir

import (
	"os"
	"path/filepath"
)

// renameFile is os.Rename unless a test replaces it.
var renameFile = os.Rename

// WriteFile replaces path with data using a same-directory temporary file
// and rename, so an interrupted write cannot leave a truncated JSON document.
//
// A config file that is a symbolic link (a dotfiles checkout) keeps being
// one: the file the link names is replaced, next to where it lives. Renaming
// over the link itself turned it into a regular file and left the linked
// file holding the old bytes. A link whose target cannot be resolved is
// replaced, as before.
//
// The temporary file is synced before the rename, so a crash shortly after
// cannot leave the new name pointing at a file whose bytes never reached
// the disk.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if target, err := filepath.EvalSymlinks(path); err == nil {
			path = target
		}
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(name)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := renameFile(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
