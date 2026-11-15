package fxprofile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanListsSourcesAndIgnoresRepoPermissionMode(t *testing.T) {
	state := t.TempDir()
	workspace := t.TempDir()
	user := filepath.Join(state, "agents", "reviewer")
	repo := filepath.Join(workspace, ".fx", "agents", "reviewer")
	mainDir := filepath.Join(workspace, ".fx", "agents", "main")
	for _, dir := range []string{user, repo, mainDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(user, "profile.json"), []byte(`{"description":"User reviewer","permission_mode":"ask","tools":["read_file"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(user, "prompt.md"), []byte("Be careful.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "profile.json"), []byte(`{"description":"Repo reviewer","permission_mode":"full-access"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainDir, "profile.json"), []byte(`{"description":"Should be ignored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles := Scan(state, workspace)
	bySource := map[string]Profile{}
	for _, profile := range profiles {
		bySource[profile.Source+"/"+profile.Name] = profile
	}
	if bySource["built-in/main"].Description == "" || bySource["built-in/agent"].Name != "agent" {
		t.Fatalf("built-ins=%+v", profiles[:2])
	}
	userProfile := bySource["user/reviewer"]
	if userProfile.Permission != "ask" || userProfile.PromptPreview != "Be careful." || userProfile.RepoPermissionIgnored {
		t.Fatalf("user profile=%+v", userProfile)
	}
	repoProfile := bySource["workspace/reviewer"]
	if !repoProfile.RepoPermissionIgnored || repoProfile.Permission != "" {
		t.Fatalf("repo profile=%+v", repoProfile)
	}
	if !bySource["workspace/main"].Ignored {
		t.Fatal("workspace main folder was treated as usable")
	}
}
