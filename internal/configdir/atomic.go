package configdir

import (
	"os"
	"path/filepath"
)

// renameFile is os.Rename unless a test replaces it.
var renameFile = os.Rename

// WriteFile replaces path with data using a same-directory temporary file
// and rename, so an interrupted write cannot leave a truncated JSON document.
func WriteFile(path string, data []byte, perm os.FileMode) error {
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