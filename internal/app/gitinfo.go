package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fx-desktop/internal/workspace"
)

// GitInfo is the working-tree state of a project, read from git. Changes is
// capped so a huge repository cannot flood the rail.
type GitInfo struct {
	Branch  string
	Changes []workspace.Change
	// Modified is each changed path's last write time, and LastCommit is when
	// git last recorded a commit. Together they are the reviewed watermark
	// §5 and §12.0 define: a change older than both has already been through a
	// commit or a review, so the rail does not raise it again.
	Modified   map[string]time.Time
	LastCommit time.Time
}

// unreviewed reports whether any change is newer than the watermark — the
// later of the last commit and the last time the user opened the drawer. A
// path git status named but whose timestamp could not be read (a deletion,
// an unreadable file) counts as unreviewed: the user has not seen it, and
// guessing "reviewed" would hide a change.
func (g *GitInfo) unreviewed(since time.Time) bool {
	if g == nil || len(g.Changes) == 0 {
		return false
	}
	watermark := g.LastCommit
	if since.After(watermark) {
		watermark = since
	}
	for _, ch := range g.Changes {
		t, ok := g.Modified[ch.Path]
		if !ok || t.After(watermark) {
			return true
		}
	}
	return false
}

// errNoRepo is the workspace's own answer that it is not a git work tree. It
// is not a failure: the panels say "Not a git repository" and stay calm.
var errNoRepo = errors.New("not a git repository")

// gitInfo runs git status and git diff in dir. The error is errNoRepo when
// dir is not inside a work tree — a fact about the workspace, not a failure —
// and the underlying failure when git exists but the tree could not be read.
func gitInfo(ctx context.Context, dir string) (*GitInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	info := &GitInfo{}
	// -z makes git emit each path as bytes instead of C-quoting it, and
	// separates the two paths of a rename with its own NUL. Quoted output
	// would name a file that does not exist (" leading", "quote\"x") and a
	// rename would read as one string with " -> " in the middle.
	status := exec.CommandContext(ctx, "git", "status", "--porcelain=v1", "-z", "-b", "--untracked-files=all")
	status.Dir = dir
	out, err := status.Output()
	if err != nil {
		if !inGitTree(dir) {
			return nil, errNoRepo
		}
		return nil, err
	}
	adds, dels := gitNumstat(ctx, dir)
	info.LastCommit = gitLastCommit(ctx, dir)
	info.Modified = map[string]time.Time{}

	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		switch {
		case record == "":
			continue
		case strings.HasPrefix(record, "## "):
			info.Branch = gitBranch(record[3:])
			continue
		case len(record) < 4:
			continue
		}
		xy, path := record[:2], record[3:]
		if isGitRename(xy) {
			// -z puts the new path first and the old path in the next
			// record. The drawer works on the file that exists now.
			i++
		}
		if path == "" {
			continue
		}
		if fi, statErr := os.Stat(filepath.Join(dir, path)); statErr == nil {
			info.Modified[path] = fi.ModTime()
		}
		info.Changes = append(info.Changes, workspace.Change{
			Path: path,
			Kind: gitMark(xy),
			Adds: adds[path],
			Dels: dels[path],
		})
	}
	return info, nil
}

// isGitRename reports a rename or copy in git's two-letter status. Both put
// an extra path in the -z stream after the one the UI shows.
func isGitRename(xy string) bool {
	return strings.ContainsAny(xy, "RC")
}

// inGitTree reports whether dir is inside a git work tree. A failed read is
// only "not a repository" where there truly is no repository: git's own
// message for that is localised, so the mark it looks for — a .git file or
// directory at dir or above it — is what decides. Anything else (git
// missing, the tree refused) is a real failure, and the panel says so.
func inGitTree(dir string) bool {
	for d := filepath.Clean(dir); ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return false
		}
		d = parent
	}
}

// gitLastCommit is when git last recorded a commit, zero when the repository
// has none yet — which reads as "nothing has been reviewed" rather than as
// "everything has".
func gitLastCommit(ctx context.Context, dir string) time.Time {
	log := exec.CommandContext(ctx, "git", "log", "-1", "--format=%ct")
	log.Dir = dir
	out, err := log.Output()
	if err != nil {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

// maxReviewDiffLines bounds how much of one file's diff is read into memory.
// The drawer draws fewer still; past this the app stops reading rather than
// holding a megabyte of text it will never show.
const maxReviewDiffLines = workspace.MaxUnifiedDiffLines

// gitFileDiff reads one path's full change as classified lines, returning the
// lines, how many past the cap were not read, and an error.
//
// An untracked file has no diff against HEAD, so it is compared against
// /dev/null: the drawer then shows what would be added instead of an empty
// pane that looks like "nothing changed here".
func gitFileDiff(ctx context.Context, dir, path string) ([]workspace.DiffLine, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	args := []string{"diff", "--no-color", "--", path}
	if gitIsUntracked(ctx, dir, path) {
		args = []string{"diff", "--no-color", "--no-index", "--", os.DevNull, path}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	// `git diff` exits 1 when it found differences, and `--no-index` does too:
	// output means success here, not failure.
	if err != nil && len(out) == 0 {
		return nil, 0, err
	}
	lines, omitted := classifyUnifiedDiff(string(out))
	return lines, omitted, nil
}

// gitIsUntracked reports whether git has never seen this path. `git
// ls-files --error-unmatch` exits non-zero when it is not in the index.
func gitIsUntracked(ctx context.Context, dir, path string) bool {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--error-unmatch", "--", path)
	cmd.Dir = dir
	return cmd.Run() != nil
}

// classifyUnifiedDiff classifies a unified diff. The classification is a fact
// about the text, so it lives in the workspace package; this wrapper keeps the
// app's name for it.
func classifyUnifiedDiff(text string) ([]workspace.DiffLine, int) {
	return workspace.ClassifyUnifiedDiff(text)
}

// diffLineKind classifies one unified-diff line.
func diffLineKind(line string) string { return workspace.UnifiedLineKind(line) }

// gitBranch reads the branch from the "## main...origin/main" header.
func gitBranch(head string) string {
	for _, prefix := range []string{"No commits yet on ", "Initial commit on "} {
		head = strings.TrimPrefix(head, prefix)
	}

	if i := strings.Index(head, "..."); i >= 0 {
		head = head[:i]
	}
	if i := strings.IndexAny(head, " ["); i >= 0 {
		head = head[:i]
	}
	return strings.TrimSpace(head)
}

// gitMark reduces a porcelain XY pair to one explorer mark.
func gitMark(xy string) string {
	switch {
	case strings.Contains(xy, "D"):
		return "D"
	case strings.Contains(xy, "A"), strings.Contains(xy, "?"):
		return "A"
	default:
		return "M"
	}
}

// gitNumstat maps path to added and deleted line counts of the unstaged
// diff. Untracked files have no diff and so no counts.
func gitNumstat(ctx context.Context, dir string) (map[string]int, map[string]int) {
	adds := map[string]int{}
	dels := map[string]int{}
	// -z keeps a path with spaces, tabs or quotes as the file's own name. Its
	// records are "adds<TAB>dels<TAB>path", NUL-terminated.
	diff := exec.CommandContext(ctx, "git", "diff", "--numstat", "-z")
	diff.Dir = dir
	out, err := diff.Output()
	if err != nil {
		return adds, dels
	}
	for _, record := range strings.Split(string(out), "\x00") {
		first := strings.IndexByte(record, '\t')
		if first < 0 {
			continue
		}
		second := strings.IndexByte(record[first+1:], '\t')
		if second < 0 {
			continue
		}
		second += first + 1
		path := record[second+1:]
		adds[path] = plusOrZero(record[:first])
		dels[path] = plusOrZero(record[first+1 : second])
	}
	return adds, dels
}

func plusOrZero(s string) int {
	if s == "-" {
		return 0
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}
