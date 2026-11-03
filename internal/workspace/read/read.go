// Package read performs the workspace's file I/O: reading the files a message
// names and listing the project for the @ picker. It is separate from the
// workspace package's data model so the UI can depend on the model without
// depending on a process runner (§3.1).
package read

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"fx-desktop/internal/workspace"
)

// Refusal is a message that cannot be sent as written, with the reason a
// person can act on: a file over the limit fx accepts, or one that is not
// text. It is an error rather than a string so a refused message and a
// cancelled read cannot be confused (§B3).
type Refusal struct{ Reason string }

func (r *Refusal) Error() string { return r.Reason }

// Resolve reads every file a message names with @path. A token that names
// nothing is left as the text it is: the picker only writes paths that exist,
// so one that does not is prose rather than a mistake to report. A token that
// names something fx could not be given is a *Refusal — the message is held
// with the reason, instead of arriving as a mention that carries nothing.
//
// A cancelled or expired ctx returns ctx.Err(), never a successful empty
// result: the caller must not send the message without the content it named.
// It performs file I/O, so callers run it off the UI thread; the context is
// checked before and after every file, but a blocking filesystem call cannot
// be interrupted, only abandoned.
func Resolve(ctx context.Context, project, text string) ([]workspace.Mention, error) {
	if project == "" {
		return nil, nil
	}
	var mentions []workspace.Mention
	total := 0
	for _, token := range workspace.MentionPaths(text) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, ok := workspace.ResolvePath(project, token)
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > workspace.MaxMentionBytes {
			return nil, &Refusal{fmt.Sprintf("@%s is larger than the %s fx reads from one file",
				token, workspace.HumanSize(workspace.MaxMentionBytes))}
		}
		data, tooBig, err := readBounded(path, workspace.MaxMentionBytes)
		if err != nil {
			continue
		}
		if tooBig {
			// The file grew after the check above: refuse it rather than send
			// a silent truncation.
			return nil, &Refusal{fmt.Sprintf("@%s is larger than the %s fx reads from one file",
				token, workspace.HumanSize(workspace.MaxMentionBytes))}
		}
		if !utf8.Valid(data) {
			return nil, &Refusal{fmt.Sprintf("@%s is not a text file, so it cannot go in a message", token)}
		}
		total += len(data)
		if total > workspace.MaxMentionTotalBytes {
			return nil, &Refusal{fmt.Sprintf("these files are more than the %s fx reads in one message",
				workspace.HumanSize(workspace.MaxMentionTotalBytes))}
		}
		mentions = append(mentions, workspace.Mention{Path: token, Abs: path, Text: string(data)})
	}
	// A cancellation that arrives after the last read must not turn into a
	// successful submission: the caller would send a message whose content it
	// is no longer allowed to use.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return mentions, nil
}

// readBounded reads at most limit bytes and reports whether the file had more.
// The bounded read is what keeps a file that grows between Stat and Read from
// arriving truncated without anyone noticing.
func readBounded(path string, limit int) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return nil, true, nil
	}
	return data, false, nil
}

// ListFiles lists the files the @ picker offers: what git tracks, plus what it
// does not ignore. git already leaves out .git and the build output, so the
// picker offers the project rather than everything on disk.
func ListFiles(ctx context.Context, dir string) ([]workspace.File, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	list.Dir = dir
	out, err := list.Output()
	if err != nil {
		return nil, err
	}
	var files []workspace.File
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		files = append(files, workspace.File{
			Path: path, Name: name, Dir: workspace.MentionDir(path),
			LowerName: strings.ToLower(name), LowerPath: strings.ToLower(path),
		})
	}
	return files, nil
}
