// Package fxprofile lists agent profiles fx can see. It reads profile.json
// and a short prompt preview. It does not write profiles or pass --profile
// to ask or acp; that flag is only for an interactive fx.
package fxprofile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"fx-desktop/internal/safedir"
)

const (
	// maxProfileBytes is fx's limit for profile.json.
	maxProfileBytes = 64 << 10
	// maxPromptFileBytes is fx's limit for prompt.md. A larger file is a
	// profile fx refuses, not a profile with a long preview.
	maxPromptFileBytes = 256 << 10
	// maxPromptPreviewBytes is what the panel shows. The reader never asks
	// for more than this: a 16 MiB prompt must not be loaded to show 4 KiB.
	maxPromptPreviewBytes = 4 << 10
	// profileDirBatch is how many directory entries one read asks for. A
	// 10,000-entry directory must not be allocated or sorted as one slice,
	// and a cancellation between batches must stop the walk (P5).
	profileDirBatch = 64
)

var (
	// maxProfileEntries bounds one agents-directory enumeration. A larger
	// folder is reported as partial, never presented as the whole set.
	maxProfileEntries = 10000
)

// scanStopped explains why a walk ended early. It is a Problem as well as a
// Partial flag, so a caller can say what was actually skipped (P5).
func scanStopped(dir string, err error) string {
	if err != nil {
		return dir + ": scan stopped: " + err.Error()
	}
	return dir + ": scan stopped at the entry budget"
}

// Profile is one readable agent profile.
type Profile struct {
	Name, Source, Path, Description, Permission, Model, Effort, PromptMode string
	Tools                                                                  []string
	Ignored                                                                bool
	RepoPermissionIgnored                                                  bool
	Problem, PromptPreview                                                 string
}

// ScanResult is one scan. Problems names roots that could not be enumerated;
// Partial is true when a deadline or cancellation stopped the walk. A caller
// must show these instead of presenting an empty list as "no profiles" (P4).
type ScanResult struct {
	Profiles []Profile
	Problems []string
	Partial  bool
}

// Scan is ScanContext without cancellation, for callers that only need the
// list.
func Scan(stateRoot, workspace string) []Profile {
	return ScanContext(context.Background(), stateRoot, workspace).Profiles
}

// ScanContext returns built-in profiles, then profiles under the fx state root
// and the workspace. A folder named main is listed as ignored because fx
// ignores it. The same name can appear from more than one source.
func ScanContext(ctx context.Context, stateRoot, workspace string) ScanResult {
	if ctx == nil {
		ctx = context.Background()
	}
	result := ScanResult{Profiles: []Profile{
		{Name: "main", Source: "built-in", Description: "fx itself, with every tool"},
		{Name: "agent", Source: "built-in", Description: "A copy of the agent that starts it"},
	}}
	// Cancellation is observed before any work and between the two roots:
	// a pre-cancelled scan has established only the built-ins (P5).
	if err := ctx.Err(); err != nil {
		result.Partial = true
		result.Problems = append(result.Problems, "profile scan stopped: "+err.Error())
		return result
	}
	if stateRoot != "" {
		profiles, problems, partial := readAgents(ctx, filepath.Join(stateRoot, "agents"), "user", "")
		result.Profiles = append(result.Profiles, profiles...)
		result.Problems = append(result.Problems, problems...)
		result.Partial = result.Partial || partial
	}
	if err := ctx.Err(); err != nil {
		result.Partial = true
		result.Problems = append(result.Problems, "profile scan stopped before the workspace: "+err.Error())
		return result
	}
	if workspace != "" {
		repoRoot, err := resolveRoot(workspace)
		if err != "" {
			result.Problems = append(result.Problems, "workspace: "+err)
		} else {
			profiles, problems, partial := readAgents(ctx, filepath.Join(workspace, ".fx", "agents"), "workspace", repoRoot)
			result.Profiles = append(result.Profiles, profiles...)
			result.Problems = append(result.Problems, problems...)
			result.Partial = result.Partial || partial
		}
	}
	return result
}

// readAgents lists one agents directory. repoRoot is empty for the user's
// own profiles, which may link anywhere; a workspace scan must keep both the
// profile folder and every file it reads inside the repository. The directory
// is read in bounded batches, and every early return observes cancellation
// (P5). Time is bounded by ctx's deadline; entries by maxProfileEntries.
func readAgents(ctx context.Context, dir, source, repoRoot string) ([]Profile, []string, bool) {
	var out []Profile
	var problems []string
	// Every return goes through finish (C6): the rows already collected are
	// sorted and the partial flag is normalized on cancellation, entry-budget
	// and read-error paths too. Sorting never resumes a cancelled walk.
	finish := func(partial bool) ([]Profile, []string, bool) {
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out, problems, partial
	}
	if err := ctx.Err(); err != nil {
		problems = append(problems, scanStopped(dir, err))
		return finish(true)
	}
	// A static FIFO (or a link to one) must fail before a blocking Open.
	// safedir checks the path, the open flags and the opened descriptor (S6).
	f, err := safedir.Open(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if ctx.Err() != nil {
				problems = append(problems, scanStopped(dir, ctx.Err()))
				return finish(true)
			}
			return finish(false)
		}
		problems = append(problems, fmt.Sprintf("%s: %v", dir, err))
		return finish(ctx.Err() != nil)
	}
	defer f.Close()
	workspace := repoRoot != ""
	if workspace {
		if _, problem := contained(repoRoot, dir); problem != "" {
			problems = append(problems, problem)
			return finish(ctx.Err() != nil)
		}
	}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			problems = append(problems, scanStopped(dir, err))
			return finish(true)
		}
		entries, readErr := f.ReadDir(profileDirBatch)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				problems = append(problems, scanStopped(dir, err))
				return finish(true)
			}
			if total >= maxProfileEntries {
				problems = append(problems, scanStopped(dir, nil))
				return finish(true)
			}
			total++
			if !entryIsDir(dir, entry) {
				continue
			}
			name := entry.Name()
			profile := Profile{Name: name, Source: source, Path: filepath.Join(dir, name)}
			if !validName(name) {
				profile.Problem = "profile name is not one fx will load"
			}
			if name == "main" {
				profile.Ignored = true
				profile.Problem = "fx ignores a main folder"
			}
			if workspace {
				if _, problem := contained(repoRoot, profile.Path); problem != "" {
					profile.Problem = problem
					out = append(out, profile)
					continue
				}
			}
			readProfile(&profile, repoRoot)
			out = append(out, profile)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", dir, readErr))
			return finish(true)
		}
	}
	if err := ctx.Err(); err != nil {
		problems = append(problems, scanStopped(dir, err))
		return finish(true)
	}
	return finish(false)
}

// entryIsDir follows a directory symlink; the containment check for a
// workspace scan runs on the resolved folder.
func entryIsDir(dir string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, entry.Name()))
	return err == nil && info.IsDir()
}

func readProfile(profile *Profile, repoRoot string) {
	workspace := repoRoot != ""
	path := filepath.Join(profile.Path, "profile.json")
	data, found, tooLarge, problem := readBounded(path, maxProfileBytes, workspace, repoRoot)
	switch {
	case problem != "":
		if profile.Problem == "" {
			profile.Problem = problem
		}
		return
	case !found:
		if profile.Problem == "" {
			profile.Problem = "profile.json is missing"
		}
		return
	case tooLarge:
		profile.Problem = "profile.json is larger than fx accepts"
		return
	}
	var doc struct {
		Description    string   `json:"description"`
		Tools          []string `json:"tools"`
		PermissionMode string   `json:"permission_mode"`
		Model          string   `json:"model"`
		Effort         string   `json:"effort"`
		Prompt         string   `json:"prompt"`
	}
	if json.Unmarshal(data, &doc) != nil {
		profile.Problem = "profile.json is not readable"
		return
	}
	profile.Description = strings.TrimSpace(doc.Description)
	profile.Tools = doc.Tools
	profile.Model = strings.TrimSpace(doc.Model)
	profile.Effort = strings.TrimSpace(doc.Effort)
	profile.PromptMode = strings.TrimSpace(doc.Prompt)
	if profile.Description == "" && profile.Problem == "" {
		profile.Problem = "a profile without a description does not load"
	}
	if doc.PermissionMode != "" {
		if workspace {
			profile.RepoPermissionIgnored = true
		} else if doc.PermissionMode == "ask" || doc.PermissionMode == "auto" || doc.PermissionMode == "full-access" {
			profile.Permission = doc.PermissionMode
		} else if profile.Problem == "" {
			profile.Problem = "permission mode is not one fx accepts"
		}
	}

	promptPath := filepath.Join(profile.Path, "prompt.md")
	if problem := checkPromptSize(promptPath, workspace, repoRoot); problem != "" {
		profile.Problem = problem
		return
	}
	preview, found, _, problem := readBounded(promptPath, maxPromptPreviewBytes, workspace, repoRoot)
	if problem != "" {
		profile.Problem = problem
		return
	}
	if !found {
		return
	}
	profile.PromptPreview = strings.TrimSpace(string(validPrefix(preview)))
}

// checkPromptSize rejects a prompt fx would refuse before opening it for the
// preview. A file that is not regular (a FIFO, a device) is rejected here
// too, so the read below never blocks on something that has no end.
func checkPromptSize(path string, workspace bool, repoRoot string) string {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ""
		}
		return "could not read prompt.md"
	}
	if !info.Mode().IsRegular() {
		return "prompt.md must be a regular file"
	}
	if workspace {
		if _, problem := contained(repoRoot, path); problem != "" {
			return problem
		}
	}
	if info.Size() > maxPromptFileBytes {
		return "prompt.md is larger than fx accepts"
	}
	return ""
}

// readBounded reads at most limit+1 bytes, so the caller can tell "larger
// than the limit" from "exactly the limit". It never reads a special file:
// stat happens before open, and only a regular file is opened.
func readBounded(path string, limit int64, workspace bool, repoRoot string) (data []byte, found bool, tooLarge bool, problem string) {
	name := filepath.Base(path)
	if workspace {
		if _, err := os.Lstat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, false, false, ""
			}
			return nil, false, false, "could not read " + name
		}
		if _, issue := contained(repoRoot, path); issue != "" {
			return nil, true, false, issue
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, false, ""
		}
		return nil, false, false, "could not read " + name
	}
	if !info.Mode().IsRegular() {
		return nil, true, false, name + " must be a regular file"
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, true, false, "could not read " + name
	}
	defer f.Close()
	// The pre-open Stat above is not an atomic guarantee: the path can be
	// replaced between it and Open. Verify the object we actually hold.
	// Boundary: on POSIX a FIFO that appeared in that gap still blocks
	// Open before this check can run; a platform-specific O_NONBLOCK open
	// is needed to close that window, and is not claimed here (P5).
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return nil, true, false, name + " must be a regular file"
	}
	if workspace {
		if _, issue := contained(repoRoot, path); issue != "" {
			return nil, true, false, issue
		}
	}
	buf, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, true, false, "could not read " + name
	}
	if int64(len(buf)) > limit {
		return buf[:limit], true, true, ""
	}
	return buf, true, false, ""
}

// contained resolves path and reports whether it stays inside root. Both
// sides are resolved, so an ancestor directory link cannot hide an escape;
// a link that resolves inside the repository is the same file and is
// allowed (P4).
func contained(root, path string) (string, string) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		resolvedRoot = root
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "a workspace profile must stay inside the repository"
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "a workspace profile must stay inside the repository"
	}
	return resolved, ""
}

func resolveRoot(path string) (string, string) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err.Error()
	}
	return resolved, ""
}

// validPrefix cuts p back to a complete rune. The preview budget is in bytes,
// but a preview ending in half a character is not a shorter preview (P4).
func validPrefix(p []byte) []byte {
	for i := 0; i < utf8.UTFMax-1 && len(p) > 0 && !utf8.Valid(p); i++ {
		p = p[:len(p)-1]
	}
	return p
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}
