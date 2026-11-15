package app

// C6: a partial scan is still a result with a display contract. Migrated in
// part from TestReviewR6SkillPartialOrder.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"fx-desktop/internal/app/chrome"
)

// makeNamedSkills creates entries whose directory order is the reverse of
// their declared names, so a missing final sort is visible.
func makeNamedSkills(t testing.TB, workspace string, n int) {
	t.Helper()
	dir := filepath.Join(workspace, ".fx", "skills")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := os.Mkdir(filepath.Join(dir, fmt.Sprintf("d%d", i)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := f.ReadDir(-1)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	for i, entry := range entries {
		header := fmt.Sprintf("---\nname: name%d\ndescription: fixture\n---\n", n-i)
		if err := os.WriteFile(filepath.Join(dir, entry.Name(), "SKILL.md"), []byte(header), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSkillPartialResultsAreSorted(t *testing.T) {
	previous := maxSkillEntries
	maxSkillEntries = 2
	t.Cleanup(func() { maxSkillEntries = previous })
	workspace := t.TempDir()
	makeNamedSkills(t, workspace, 3)
	result := scanSkills(context.Background(), workspace, "", "")
	if !result.Partial {
		t.Fatal("entry budget did not report a partial scan")
	}
	var names []string
	for _, skill := range result.Skills {
		names = append(names, skill.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("budget path bypassed stable ordering: %q", names)
	}
}

// Every early return — budget, pre-cancellation, cancellation between
// batches and a read error path — must order the rows it did collect.
func TestSkillPartialResultsAreNormalizedOnEveryEarlyReturn(t *testing.T) {
	workspace := t.TempDir()
	makeNamedSkills(t, workspace, 3)
	withBudget := func(budget int, ctx context.Context) []string {
		previous := maxSkillEntries
		maxSkillEntries = budget
		defer func() { maxSkillEntries = previous }()
		var names []string
		for _, skill := range scanSkills(ctx, workspace, "", "").Skills {
			names = append(names, skill.Name)
		}
		return names
	}
	for _, tc := range []struct {
		name   string
		budget int
		ctx    context.Context
	}{
		{"budget", 2, context.Background()},
		{"complete", 10000, context.Background()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withBudget(tc.budget, tc.ctx)
			if !sort.StringsAreSorted(got) {
				t.Fatalf("unsorted partial rows: %q", got)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := withBudget(10000, canceled); len(got) != 0 {
		t.Fatalf("pre-cancelled scan returned rows: %q", got)
	}
}

// The panel is a second layer: a partial scan must arrive there sorted, with
// an explicit "stopped early" notice rather than a short list shown as all.
func TestSkillPartialResultsReachPanel(t *testing.T) {
	previous := maxSkillEntries
	maxSkillEntries = 2
	t.Cleanup(func() { maxSkillEntries = previous })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FX_HOME", filepath.Join(home, ".fx"))
	workspace := t.TempDir()
	makeNamedSkills(t, workspace, 3)
	c := &Coordinator{}
	loop := &manualLoop{}
	s := &ProjectWindow{coord: c, project: workspace, shell: chrome.DefaultShell(), loop: loop.dispatch}
	s.shell.Tools.Open = true
	c.runSkills(s)
	loop.await(t)
	loop.drain()
	if !strings.Contains(s.shell.Tools.SkillProblem, "stopped early") {
		t.Fatalf("partial scan was not explained to the panel: %q", s.shell.Tools.SkillProblem)
	}
	var names []string
	for _, skill := range s.shell.Tools.Skills {
		names = append(names, skill.Name)
	}
	if len(names) == 0 || !sort.StringsAreSorted(names) {
		t.Fatalf("panel received unsorted partial rows: %q", names)
	}
}
