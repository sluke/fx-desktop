package fxsessions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fx-desktop/internal/testutil"
)

func sessionPath(t *testing.T, home, id string) string {
	t.Helper()
	return filepath.Join(home, ".fx", "sessions", id)
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteManyRemovesSessionsAndMatchingContinuePointers(t *testing.T) {
	home := testutil.IsolateHome(t)
	for _, id := range []string{"keep", "drop1", "drop2"} {
		writeFile(t, filepath.Join(sessionPath(t, home, id), "session.json"), "{}")
	}
	pointers := filepath.Join(home, ".fx", "continue")
	writeFile(t, filepath.Join(pointers, "one"), "drop1\n")
	writeFile(t, filepath.Join(pointers, "two"), "drop2")
	writeFile(t, filepath.Join(pointers, "three"), "keep\n")

	deleted, err := DeleteMany([]string{"drop1", "drop2", "drop1"})
	if err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}
	if len(deleted) != 2 || deleted[0] != "drop1" || deleted[1] != "drop2" {
		t.Fatalf("deleted = %v", deleted)
	}
	for _, id := range []string{"drop1", "drop2"} {
		if _, err := os.Stat(sessionPath(t, home, id)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", id, err)
		}
	}
	if _, err := os.Stat(sessionPath(t, home, "keep")); err != nil {
		t.Fatalf("unrelated session removed: %v", err)
	}
	for _, name := range []string{"one", "two"} {
		if _, err := os.Stat(filepath.Join(pointers, name)); !os.IsNotExist(err) {
			t.Fatalf("continue pointer %s still exists: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(pointers, "three")); err != nil {
		t.Fatalf("pointer to another session removed: %v", err)
	}
}

// fx 0.0.16 keeps saved conversations in ~/.fx/sessions/v2/<id> and lists
// them by scanning that directory; the flat ~/.fx/sessions/<id> layout is the
// store earlier builds converted away from. DeleteMany must remove the
// directory wherever the running fx keeps it — a delete that only touches
// the flat path silently succeeds on a path that does not exist (os.RemoveAll
// is nil on ENOENT) and the row reappears on the next refresh.
func TestDeleteManyRemovesSessionsFromTheV2Store(t *testing.T) {
	home := testutil.IsolateHome(t)
	for _, id := range []string{"dropA", "keepB"} {
		writeFile(t, filepath.Join(home, ".fx", "sessions", "v2", id, "log.jsonl"), "{}")
	}
	writeFile(t, filepath.Join(sessionPath(t, home, "dropC"), "session.json"), "{}")
	writeFile(t, filepath.Join(home, ".fx", "continue", "p"), "dropA\n")

	deleted, err := DeleteMany([]string{"dropA", "dropC", "not-there"})
	if err != nil {
		t.Fatalf("DeleteMany: %v", err)
	}
	if len(deleted) != 3 {
		t.Fatalf("deleted = %v, want all three (missing counts as deleted)", deleted)
	}
	for _, id := range []string{"dropA", "dropC"} {
		if _, err := os.Stat(sessionPath(t, home, id)); !os.IsNotExist(err) {
			t.Fatalf("flat %s still exists: %v", id, err)
		}
		if _, err := os.Stat(filepath.Join(home, ".fx", "sessions", "v2", id)); !os.IsNotExist(err) {
			t.Fatalf("v2 %s still exists: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".fx", "sessions", "v2", "keepB")); err != nil {
		t.Fatalf("unrelated v2 session removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".fx", "continue", "p")); !os.IsNotExist(err) {
		t.Fatalf("continue pointer still exists: %v", err)
	}
}

func TestDeleteManyMissingSessionCountsAsDeleted(t *testing.T) {
	home := testutil.IsolateHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".fx", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	deleted, err := DeleteMany([]string{"already-gone"})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != "already-gone" {
		t.Fatalf("deleted = %v", deleted)
	}
}

func TestDeleteManyRejectsUnsafeIDs(t *testing.T) {
	home := testutil.IsolateHome(t)
	root := filepath.Join(home, ".fx", "sessions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, ".fx", "keep")
	writeFile(t, victim, "do not remove")

	for _, id := range []string{"", ".", "..", "../keep", "a/b", `a\b`, "a.b", "a b", strings.Repeat("a", maxIDLength+1)} {
		deleted, err := DeleteMany([]string{id})
		if err == nil {
			t.Fatalf("id %q was accepted", id)
		}
		if len(deleted) != 0 {
			t.Fatalf("id %q deleted %v", id, deleted)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("unsafe id reached outside the session root: %v", err)
	}
}

func TestDeleteManyMissingRootIsAnError(t *testing.T) {
	testutil.IsolateHome(t)
	if _, err := DeleteMany([]string{"anything"}); err == nil {
		t.Fatal("a missing sessions root should be reported")
	}
}

func TestDeleteManyIgnoresLegacyV2Environment(t *testing.T) {
	home := testutil.IsolateHome(t)
	t.Setenv("FX_SESSIONS_V2", "1")
	writeFile(t, filepath.Join(home, ".fx", "sessions", "new", "session.json"), "{}")
	writeFile(t, filepath.Join(home, ".fx", "sessions", "v2", "legacy", "session.json"), "{}")

	deleted, err := DeleteMany([]string{"new"})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != "new" {
		t.Fatalf("deleted = %v", deleted)
	}
	if _, err := os.Stat(filepath.Join(home, ".fx", "sessions", "new")); !os.IsNotExist(err) {
		t.Fatalf("current-store session still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".fx", "sessions", "v2", "legacy")); err != nil {
		t.Fatalf("legacy v2 directory should be untouched: %v", err)
	}
}
