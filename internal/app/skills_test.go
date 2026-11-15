package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestScanSkillsReadsFxDiscoveryRoots(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	alpha := filepath.Join(workspace, ".fx", "skills", "alpha")
	if err := os.MkdirAll(alpha, 0o700); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: alpha-skill\ndescription: >\n  Helps with alpha\n  and beta.\n---\n\nInstructions.\n"
	if err := os.WriteFile(filepath.Join(alpha, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	beta := filepath.Join(home, ".claude", "skills", "beta")
	if err := os.MkdirAll(beta, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beta, "SKILL.md"), []byte("---\nname: beta-skill\ndescription: Global beta\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skills := scanSkills(context.Background(), workspace, "", home).Skills
	if len(skills) != 2 {
		t.Fatalf("skills=%+v", skills)
	}
	byName := make(map[string]bool)
	for _, skill := range skills {
		byName[skill.Name] = true
	}
	if !byName["alpha-skill"] || !byName["beta-skill"] {
		t.Fatalf("missing parsed names: %+v", skills)
	}
	for _, skill := range skills {
		if skill.Name == "alpha-skill" && skill.Description != "Helps with alpha and beta." {
			t.Fatalf("block description=%q", skill.Description)
		}
	}
}

func TestScanSkillsUsesFXStateRoot(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".fx", "skills", "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".fx", "skills", "old", "SKILL.md"), []byte("---\nname: old-skill\ndescription: old\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state, "skills", "moved"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state, "skills", "moved", "SKILL.md"), []byte("---\nname: moved-skill\ndescription: moved\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	skills := scanSkills(context.Background(), workspace, state, home).Skills
	names := map[string]bool{}
	for _, skill := range skills {
		names[skill.Name] = true
	}
	if !names["moved-skill"] || names["old-skill"] {
		t.Fatalf("skills = %+v", skills)
	}
}

func TestParseSkillHeaderFallsBackWhenFrontmatterIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(path, []byte("# Just a skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name, description := parseSkillHeader(path)
	if name != "" || description != "" {
		t.Fatalf("unexpected header: %q %q", name, description)
	}
}

// The parser stops at the closing marker: the body of a large SKILL.md is
// never read or allocated.
func TestParseSkillHeaderStopsAtClosingMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	body := "---\nname: fixture\ndescription: small\n---\n" + strings.Repeat("x", 16<<20)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	small := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if name, _ := parseSkillHeader(path); name != "fixture" {
				b.Fatalf("name=%q", name)
			}
		}
	})
	if got := small.AllocedBytesPerOp(); got > 128<<10 {
		t.Fatalf("header parse allocated %d B/op for a 16 MiB skill", got)
	}
}

// A frontmatter that never closes is bounded by the header cap instead of
// being read to the end of the file.
func TestParseSkillHeaderWithoutClosingMarkerIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	body := "---\nname: unterminated\n" + strings.Repeat("description: still going\n", 1<<19)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			parseSkillHeader(path)
		}
	})
	if got := result.AllocedBytesPerOp(); got > 256<<10 {
		t.Fatalf("unterminated header allocated %d B/op", got)
	}
}

// A FIFO named SKILL.md must be skipped, not opened: opening a pipe waits for
// a writer (P4).
func TestScanSkillsSkipsSpecialFiles(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".fx", "skills", "pipe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "SKILL.md"), 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	done := make(chan skillScanResult, 1)
	go func() { done <- scanSkills(context.Background(), workspace, "", "") }()
	select {
	case result := <-done:
		if len(result.Skills) != 0 || result.Partial {
			t.Fatalf("special file became a skill: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("skill scan blocked on a FIFO")
	}
}

func TestScanSkillsReportsCancellation(t *testing.T) {
	workspace := t.TempDir()
	for i := 0; i < 4; i++ {
		dir := filepath.Join(workspace, ".fx", "skills", fmt.Sprintf("s%d", i))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := scanSkills(ctx, workspace, "", "")
	if !result.Partial {
		t.Fatal("cancelled scan did not report partial")
	}
}

func BenchmarkSkillHeader(b *testing.B) {
	path := filepath.Join(b.TempDir(), "SKILL.md")
	body := "---\nname: fixture\ndescription: small\n---\n" + strings.Repeat("x", 16<<20)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if name, _ := parseSkillHeader(path); name != "fixture" {
			b.Fatal(name)
		}
	}
}
