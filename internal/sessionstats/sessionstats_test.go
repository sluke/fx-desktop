package sessionstats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fx-desktop/internal/configdir"
	"fx-desktop/internal/testutil"
)

func TestStoreRoundTrip(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Stats{ContextUsed: 12000, ContextTotal: 200000, InputTokens: 900, OutputTokens: 40}
	if err := store.Put("abc", want); err != nil {
		t.Fatal(err)
	}
	got := store.Get("abc")
	if got.ContextUsed != want.ContextUsed || got.ContextTotal != want.ContextTotal ||
		got.InputTokens != want.InputTokens || got.OutputTokens != want.OutputTokens {
		t.Fatalf("in-memory record = %+v, want %+v", got, want)
	}
	if got.UpdatedMS == 0 {
		t.Fatal("record has no timestamp")
	}

	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got = again.Get("abc")
	if got.ContextUsed != 12000 || got.ContextTotal != 200000 || got.InputTokens != 900 || got.OutputTokens != 40 {
		t.Fatalf("reloaded record = %+v", got)
	}

	path, err := configdir.SessionStatsPath()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("stored file is not JSON: %v", err)
	}
	if f.Version != 1 {
		t.Fatalf("version = %d", f.Version)
	}
	// The atomic write must not leave its temporary file behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestStoreMissingFileIsEmpty(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := Load()
	if err != nil {
		t.Fatalf("a missing store is not an error: %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("missing store has %d records", store.Len())
	}
	if got := store.Get("anything"); !got.Empty() {
		t.Fatalf("missing record = %+v", got)
	}
}

func TestStoreCorruptFileStartsEmptyAndRepairs(t *testing.T) {
	testutil.IsolateHome(t)
	path, err := configdir.SessionStatsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := Load()
	if err == nil {
		t.Fatal("a damaged store should be reported")
	}
	if store == nil || store.Len() != 0 {
		t.Fatalf("damaged store should start empty: %+v", store)
	}
	if err := store.Put("fixed", Stats{InputTokens: 5}); err != nil {
		t.Fatal(err)
	}
	if again, err := Load(); err != nil {
		t.Fatalf("write did not repair the file: %v", err)
	} else if got := again.Get("fixed"); got.InputTokens != 5 {
		t.Fatalf("repaired record = %+v", got)
	}
}

func TestStoreSkipsEmptyIDsAndRecords(t *testing.T) {
	store := New("")
	if err := store.Put("", Stats{InputTokens: 10}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put("empty", Stats{}); err != nil {
		t.Fatal(err)
	}
	if store.Len() != 0 {
		t.Fatalf("empty records were stored: %d", store.Len())
	}
}

func TestStoreDeleteForgetsRecordsAndPersists(t *testing.T) {
	testutil.IsolateHome(t)
	store, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"drop1", "drop2", "keep"} {
		if err := store.Put(id, Stats{InputTokens: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Delete("drop1", "drop2", "missing", ""); err != nil {
		t.Fatal(err)
	}
	if got := store.Get("drop1"); !got.Empty() {
		t.Fatalf("deleted record still in memory: %+v", got)
	}
	if got := store.Get("keep"); got.InputTokens != 10 {
		t.Fatalf("unrelated record removed: %+v", got)
	}
	if store.Len() != 1 {
		t.Fatalf("store holds %d records", store.Len())
	}
	again, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Get("drop1"); !got.Empty() {
		t.Fatalf("deleted record survived a reload: %+v", got)
	}
	if got := again.Get("keep"); got.InputTokens != 10 {
		t.Fatalf("reloaded record = %+v", got)
	}
}

func TestStoreNilDeleteIsANoOp(t *testing.T) {
	var store *Store
	if err := store.Delete("anything"); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCapsEntries(t *testing.T) {
	store := New("")
	for i := 0; i < maxEntries+5; i++ {
		if err := store.Put(idFor(i), Stats{InputTokens: i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if got := store.Len(); got != maxEntries {
		t.Fatalf("store holds %d records, want %d", got, maxEntries)
	}
	// The newest write must survive the pruning of the older ones.
	if got := store.Get(idFor(maxEntries + 4)); got.InputTokens != maxEntries+5 {
		t.Fatalf("newest record was pruned: %+v", got)
	}
}

func idFor(i int) string {
	const digits = "0123456789"
	return string([]byte{'s', digits[(i/100)%10], digits[(i/10)%10], digits[i%10]})
}
