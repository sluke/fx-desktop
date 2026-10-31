// Package fxsessions removes fx's saved conversations. fx has no delete
// command: a conversation is the directory ~/.fx/sessions/<id>, so deleting
// it is the supported removal path. The app owns the confirmation; this
// package only performs the validated disk work and cleans the per-workspace
// "continue here" pointers that would otherwise name a conversation that no
// longer exists.
//
// fx 0.0.15 moved every saved session to the current store and removed the
// experimental v2 flag: ~/.fx/sessions/<id> is now the only root. A stale
// FX_SESSIONS_V2 in the environment no longer changes where fx stores
// sessions, so deletion must ignore it too.
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

// root returns the directory holding one subdirectory per saved session.
// fx 0.0.15+ uses this store unconditionally; FX_SESSIONS_V2 is obsolete.
func root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fx", "sessions"), nil
}

// DeleteMany removes each saved conversation and returns the IDs whose
// directory was removed. A missing session counts as removed: a stale row
// then disappears on the next refresh instead of failing forever. Invalid
// IDs are reported and skipped. The app must never call this without the
// user's confirmation.
func DeleteMany(ids []string) (deleted []string, err error) {
	dir, err := root()
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("fx has no saved sessions at %s", dir)
		}
		return nil, fmt.Errorf("read fx sessions: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("fx sessions path is not a directory: %s", dir)
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
		if err := os.RemoveAll(filepath.Join(dir, id)); err != nil {
			problems = append(problems, fmt.Errorf("delete session %s: %w", id, err))
			continue
		}
		deleted = append(deleted, id)
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
