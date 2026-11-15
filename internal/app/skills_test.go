package app

import (
	"os"
	"path/filepath"
	"testing"
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
	skills := scanSkills(workspace, "", home)
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
	skills := scanSkills(workspace, state, home)
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
