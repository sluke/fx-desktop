package fxprofile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

func writeProfileFixture(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findProfile(t testing.TB, state, workspace, source, name string) Profile {
	t.Helper()
	for _, profile := range ScanContext(context.Background(), state, workspace).Profiles {
		if profile.Source == source && profile.Name == name {
			return profile
		}
	}
	t.Fatalf("profile %s/%s not found", source, name)
	return Profile{}
}

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

// The preview budget is in bytes, but a preview ending in half a rune is not
// a shorter preview (P4).
func TestPromptPreviewKeepsCompleteRunes(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "agents", "reviewer")
	writeProfileFixture(t, filepath.Join(dir, "profile.json"), `{"description":"reviewer"}`)
	writeProfileFixture(t, filepath.Join(dir, "prompt.md"), strings.Repeat("中", 2000))
	profile := findProfile(t, state, "", "user", "reviewer")
	if !utf8.ValidString(profile.PromptPreview) {
		t.Fatalf("preview splits UTF-8 at %d bytes", len(profile.PromptPreview))
	}
	if len(profile.PromptPreview) > maxPromptPreviewBytes {
		t.Fatalf("preview over budget: %d bytes", len(profile.PromptPreview))
	}
	if profile.Problem != "" {
		t.Fatalf("valid preview reported %q", profile.Problem)
	}
}

// A link inside the repository is still inside the repository and loads.
func TestWorkspaceProfileSymlinkInsideRepositoryLoads(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".fx", "agents", "reviewer")
	writeProfileFixture(t, filepath.Join(dir, "profile.json"), `{"description":"reviewer"}`)
	writeProfileFixture(t, filepath.Join(workspace, "prompts", "reviewer.md"), "in-repository prompt")
	if err := os.Symlink(filepath.Join(workspace, "prompts", "reviewer.md"), filepath.Join(dir, "prompt.md")); err != nil {
		t.Fatal(err)
	}
	profile := findProfile(t, "", workspace, "workspace", "reviewer")
	if profile.Problem != "" {
		t.Fatalf("valid in-repository link rejected: %s", profile.Problem)
	}
	if profile.PromptPreview != "in-repository prompt" {
		t.Fatalf("preview=%q", profile.PromptPreview)
	}
}

// A link that leaves the repository is not a repository profile, even when
// the file itself is regular.
func TestWorkspaceProfileFileSymlinkEscapeRejected(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	dir := filepath.Join(workspace, ".fx", "agents", "reviewer")
	writeProfileFixture(t, filepath.Join(dir, "profile.json"), `{"description":"reviewer"}`)
	writeProfileFixture(t, filepath.Join(outside, "prompt.md"), "outside")
	if err := os.Symlink(filepath.Join(outside, "prompt.md"), filepath.Join(dir, "prompt.md")); err != nil {
		t.Fatal(err)
	}
	profile := findProfile(t, "", workspace, "workspace", "reviewer")
	if profile.Problem == "" || profile.PromptPreview != "" {
		t.Fatalf("outside prompt link accepted: %+v", profile)
	}
}

// An ancestor directory link is enough to leave the repository: checking the
// leaf file's own Lstat cannot see it (P4).
func TestWorkspaceProfileAncestorSymlinkEscapeRejected(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	writeProfileFixture(t, filepath.Join(outside, "reviewer", "profile.json"), `{"description":"outside repository"}`)
	if err := os.Mkdir(filepath.Join(workspace, ".fx"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, ".fx", "agents")); err != nil {
		t.Fatal(err)
	}
	scan := ScanContext(context.Background(), "", workspace)
	for _, profile := range scan.Profiles {
		if profile.Source == "workspace" && profile.Problem == "" && profile.Description == "outside repository" {
			t.Fatalf("outside profile through a symlinked agents directory was accepted: %+v", profile)
		}
	}
	if len(scan.Problems) == 0 {
		t.Fatal("escaping agents directory was not reported")
	}
}

// fx's byte limits are checked before the content is used: 64 KiB for
// profile.json and 256 KiB for prompt.md.
func TestProfileFileLimits(t *testing.T) {
	state := t.TempDir()
	okDir := filepath.Join(state, "agents", "ok")
	writeProfileFixture(t, filepath.Join(okDir, "profile.json"), `{"description":"ok"}`)
	writeProfileFixture(t, filepath.Join(okDir, "prompt.md"), strings.Repeat("x", maxPromptFileBytes))
	if profile := findProfile(t, state, "", "user", "ok"); profile.Problem != "" || len(profile.PromptPreview) != maxPromptPreviewBytes {
		t.Fatalf("at-limit prompt: problem=%q preview=%d", profile.Problem, len(profile.PromptPreview))
	}

	bigPrompt := filepath.Join(state, "agents", "big-prompt")
	writeProfileFixture(t, filepath.Join(bigPrompt, "profile.json"), `{"description":"big"}`)
	writeProfileFixture(t, filepath.Join(bigPrompt, "prompt.md"), strings.Repeat("x", maxPromptFileBytes+1))
	if profile := findProfile(t, state, "", "user", "big-prompt"); !strings.Contains(profile.Problem, "larger") {
		t.Fatalf("oversize prompt problem=%q", profile.Problem)
	}

	bigProfile := filepath.Join(state, "agents", "big-profile")
	writeProfileFixture(t, filepath.Join(bigProfile, "profile.json"), `{"description":"`+strings.Repeat("x", maxProfileBytes)+`"}`)
	if profile := findProfile(t, state, "", "user", "big-profile"); !strings.Contains(profile.Problem, "larger") {
		t.Fatalf("oversize profile.json problem=%q", profile.Problem)
	}
}

// A FIFO named prompt.md must not block the scan: the reader checks the type
// before opening (P4).
func TestProfileSpecialFileDoesNotBlock(t *testing.T) {
	if err := syscall.Mkfifo(filepath.Join(t.TempDir(), "probe"), 0o600); err != nil {
		t.Skip("mkfifo unavailable")
	}
	state := t.TempDir()
	dir := filepath.Join(state, "agents", "pipe")
	writeProfileFixture(t, filepath.Join(dir, "profile.json"), `{"description":"pipe"}`)
	if err := syscall.Mkfifo(filepath.Join(dir, "prompt.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan Profile, 1)
	go func() { done <- findProfile(t, state, "", "user", "pipe") }()
	select {
	case profile := <-done:
		if !strings.Contains(profile.Problem, "regular file") {
			t.Fatalf("FIFO prompt problem=%q", profile.Problem)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan blocked on a FIFO")
	}
}

// The 4 KiB preview must cost 4 KiB whatever the prompt's size. If the reader
// still loads the file, 16 MiB costs sixteen times 1 MiB.
func TestPromptPreviewAllocationDoesNotScaleWithFileSize(t *testing.T) {
	run := func(size int) int64 {
		dir := t.TempDir()
		writeProfileFixture(t, filepath.Join(dir, "profile.json"), `{"description":"reviewer"}`)
		writeProfileFixture(t, filepath.Join(dir, "prompt.md"), strings.Repeat("x", size))
		profile := Profile{Path: dir}
		result := testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				profile.PromptPreview = ""
				readProfile(&profile, "")
				if len(profile.PromptPreview) > maxPromptPreviewBytes {
					b.Fatal("unbounded preview")
				}
			}
		})
		return result.AllocedBytesPerOp()
	}
	small := run(1 << 20)
	large := run(16 << 20)
	if large > small+(256<<10) {
		t.Fatalf("16 MiB prompt allocated %d B/op, 1 MiB allocated %d B/op", large, small)
	}
}

func BenchmarkProfilePrompt(b *testing.B) {
	for _, size := range []int{1 << 20, 16 << 20} {
		b.Run(strings.TrimSpace(time.Duration(size).String()), func(b *testing.B) {
			dir := b.TempDir()
			writeProfileFixture(b, filepath.Join(dir, "profile.json"), `{"description":"reviewer"}`)
			writeProfileFixture(b, filepath.Join(dir, "prompt.md"), strings.Repeat("x", size))
			profile := Profile{Path: dir}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				profile.PromptPreview = ""
				readProfile(&profile, "")
			}
		})
	}
}
