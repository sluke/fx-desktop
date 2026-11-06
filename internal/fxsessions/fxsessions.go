// Package fxsessions removes fx's saved conversations. fx has no delete
// command: a conversation is the directory holding its transcript inside the
// sessions store, so deleting it is the supported removal path — fx lists
// sessions by scanning the store, so a removed directory disappears from
// every listing. The app owns the confirmation; this package only performs
// the validated disk work and cleans the per-workspace "continue here"
// pointers that would otherwise name a conversation that no longer exists.
//
// fx 0.0.16 keeps sessions in the v2 substore, ~/.fx/sessions/v2/<id>, and
// the flat ~/.fx/sessions/<id> layout is the store earlier builds converted
// away from. Both are deleted from: a delete that only touched the flat path
// succeeded on a path that did not exist (os.RemoveAll is nil on ENOENT) and
// the session reappeared on the next refresh. A stale FX_SESSIONS_V2 in the
// environment never changes where fx stores sessions, so deletion ignores it
// too.
package fxsessions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxIDLength bounds the path component. Real fx IDs are short base64url
// tokens; the limit only keeps a damaged response from constructing a long
// path.
const maxIDLength = 128

// ValidID reports whether id is safe to use as one path component under the
// sessions root. fx IDs use letters, digits, '-' and '_'; anything else could
// escape the root or name the catalog file beside it.
func ValidID(id string) bool {
	if id == "" || len(id) > maxIDLength {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

// root returns the directory that anchors the sessions store. fx keeps the
// current store in the v2 subdirectory of this path and the converted-away
// flat layout beside it; both must exist under one anchor for the store to
// be recognized.
func root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fx", "sessions"), nil
}

// storeDirs names the directories that may hold one session directory each,
// newest store first. Reserved store-internal entries are never treated as
// sessions: "v2" is the current substore itself and "trash+v1" is fx's own
// recovery area, and neither is a conversation ID fx would report.
func storeDirs(base string) []string {
	return []string{filepath.Join(base, "v2"), base}
}

var reservedStoreNames = map[string]bool{"v2": true, "trash+v1": true}

// DeleteMany removes each saved conversation and returns the IDs whose
// directory was removed. A session is looked up in every store: the v2
// substore first, then the flat layout. A missing session counts as removed:
// a stale row then disappears on the next refresh instead of failing
// forever. Invalid IDs are reported and skipped. The app must never call
// this without the user's confirmation.
func DeleteMany(ids []string) (deleted []string, err error) {
	base, err := root()
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("fx has no saved sessions at %s", base)
		}
		return nil, fmt.Errorf("read fx sessions: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("fx sessions path is not a directory: %s", base)
	}

	var problems []error
	seen := make(map[string]bool, len(ids))
	deleted = make([]string, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if !ValidID(id) {
			problems = append(problems, errors.New("refusing to delete an invalid session id"))
			continue
		}
		removed, failed := false, false
		for _, dir := range storeDirs(base) {
			if dir == base && reservedStoreNames[id] {
				continue
			}
			target := filepath.Join(dir, id)
			if _, err := os.Lstat(target); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				problems = append(problems, fmt.Errorf("delete session %s: %w", id, err))
				failed = true
				continue
			}
			if err := os.RemoveAll(target); err != nil {
				problems = append(problems, fmt.Errorf("delete session %s: %w", id, err))
				failed = true
				continue
			}
			removed = true
		}
		if removed || !failed {
			deleted = append(deleted, id)
		}
	}
	if len(deleted) > 0 {
		if err := removeContinuePointers(deleted); err != nil {
			problems = append(problems, err)
		}
	}
	return deleted, errors.Join(problems...)
}

// removeContinuePointers drops the last-session pointers that name a removed
// conversation. Without this fx -c could try to resume a session that is no
// longer on disk; pointers to other sessions are left alone. The binary
// .resume-catalog beside the sessions is a cache, not the source of truth:
// fx validates it against the directories and rebuilds it, so it is left
// untouched.
func removeContinuePointers(ids []string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".fx", "continue")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read fx continue pointers: %w", err)
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var problems []error
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				problems = append(problems, fmt.Errorf("read fx continue pointer %s: %w", entry.Name(), err))
			}
			continue
		}
		if !want[strings.TrimSpace(string(b))] {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			problems = append(problems, fmt.Errorf("remove fx continue pointer %s: %w", entry.Name(), err))
		}
	}
	return errors.Join(problems...)
}
