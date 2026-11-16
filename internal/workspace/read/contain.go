package read

import (
	"errors"
	"io"
	"os"
)

// errNotMention is a path that is not a file to attach: missing, a directory,
// or not a mention at all. Resolve leaves those tokens as prose.
var errNotMention = errors.New("not a project file mention")

func readLimited(file *os.File, limit int) (data []byte, tooBig bool, err error) {
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, errNotMention
	}
	if info.Size() > int64(limit) {
		return nil, true, nil
	}
	data, err = io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return nil, true, nil
	}
	return data, false, nil
}
