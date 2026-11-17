package fxsessions

import (
	"os"
	"path/filepath"
	"testing"

	"fx-desktop/internal/testutil"
)

// the reserved-name guard is an exact-case map lookup. On a
// case-insensitive volume (default APFS) the valid id "V2" names the v2
// substore itself, and RemoveAll takes every saved conversation with it.
func TestRegressionR9_DeleteCaseAliasOfStoreKeepsOtherSessions(t *testing.T) {
	home := testutil.IsolateHome(t)
	store := filepath.Join(home, ".fx", "sessions", "v2")
	for _, id := range []string{"keepA", "keepB"} {
		writeFile(t, filepath.Join(store, id, "session.json"), "{}")
	}
	if _, err := os.Stat(filepath.Join(home, ".fx", "sessions", "V2")); err != nil {
		t.Skip("case-sensitive filesystem")
	}
	if !ValidID("V2") {
		t.Fatal("precondition: V2 is a valid id")
	}
	deleted, err := DeleteMany([]string{"V2"})
	t.Logf("DeleteMany([V2]) = %v, %v", deleted, err)
	for _, id := range []string{"keepA", "keepB"} {
		if _, err := os.Stat(filepath.Join(store, id, "session.json")); err != nil {
			t.Errorf("unrelated session %s was removed: %v", id, err)
		}
	}
	if _, err := os.Stat(store); err != nil {
		t.Errorf("the v2 substore itself was removed: %v", err)
	}
}
