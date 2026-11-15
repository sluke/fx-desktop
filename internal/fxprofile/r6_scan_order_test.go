package fxprofile

// C6: the profile scanner has the same result contract as skills. A budget
// or cancellation must not bypass the sorting of rows already collected.
// The r6 probe only demonstrated this for skills; this test covers the
// sibling code path with the same structure.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Created in an order that is deliberately not lexical: a missing final
// sort on any early-return path becomes visible.
var r6AgentNames = []string{"name9", "name1", "name5", "name3", "name7"}

func makeNamedAgentDirs(t testing.TB, state string) {
	t.Helper()
	dir := filepath.Join(state, "agents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range r6AgentNames {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "profile.json"), []byte(`{"description":"fixture"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func r6ProfileNames(result ScanResult) []string {
	var names []string
	for _, profile := range result.Profiles[2:] { // built-ins keep their fixed order
		names = append(names, profile.Name)
	}
	return names
}

func TestProfilePartialResultsAreSorted(t *testing.T) {
	previous := maxProfileEntries
	maxProfileEntries = 3
	t.Cleanup(func() { maxProfileEntries = previous })
	state := t.TempDir()
	makeNamedAgentDirs(t, state)
	result := ScanContext(context.Background(), state, "")
	if !result.Partial {
		t.Fatal("entry budget did not report a partial scan")
	}
	names := r6ProfileNames(result)
	if len(names) == 0 || !sort.StringsAreSorted(names) {
		t.Fatalf("budget path bypassed stable ordering: %q", names)
	}
}

func TestProfileCancelledResultsAreSorted(t *testing.T) {
	state := t.TempDir()
	makeNamedAgentDirs(t, state)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ScanContext(ctx, state, "")
	if !result.Partial || len(r6ProfileNames(result)) != 0 {
		t.Fatalf("pre-cancelled scan read rows: %+v", result)
	}
}
