package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"fx-desktop/internal/app/chrome"
)

// Mentions are held to fx's own sizes, so a file a message attaches is no
// larger than the project instructions fx reads itself: 64 KiB for one file,
// 128 KiB for all of them together (§12.3.3). A file over that is refused
// where it names the message, rather than sent as a mention that carries a
// truncation nobody asked for.
const (
	maxMentionBytes      = 64 * 1024
	maxMentionTotalBytes = 128 * 1024
)

// promptMention is one file a message names with @path: the workspace path as
// written, the absolute path fx is told, and the bytes it is given. The
// content rides in the block because that is where fx reads it from — a
// resource target alone only scopes the project rules that apply to the file
// (§12.3.3).
type promptMention struct {
	Path string
	Abs  string
	Text string
}

// workspacePath resolves a mention to a file in the project. An absolute path,
// or one that climbs out of the project, is not a mention: the file is read
// and given to fx, so it has to be one of this project's own.
func workspacePath(project, token string) (string, bool) {
	if token == "" || filepath.IsAbs(token) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(token))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(project, clean), true
}

// resolveMentions reads every file a message names with @path. A token that
// names nothing is left as the text it is: the picker only writes paths that
// exist, so one that does not is prose rather than a mistake to report. A
// token that names something fx could not be given is a refusal — the message
// is held with the reason, instead of arriving as a mention that carries
// nothing.
func resolveMentions(project, text string) ([]promptMention, string) {
	if project == "" {
		return nil, ""
	}
	var mentions []promptMention
	total := 0
	for _, token := range chrome.MentionPaths(text) {
		path, ok := workspacePath(project, token)
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxMentionBytes {
			return nil, fmt.Sprintf("@%s is larger than the %s fx reads from one file",
				token, chrome.HumanSize(maxMentionBytes))
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !utf8.Valid(data) {
			return nil, fmt.Sprintf("@%s is not a text file, so it cannot go in a message", token)
		}
		total += len(data)
		if total > maxMentionTotalBytes {
			return nil, fmt.Sprintf("these files are more than the %s fx reads in one message",
				chrome.HumanSize(maxMentionTotalBytes))
		}
		mentions = append(mentions, promptMention{Path: token, Abs: path, Text: string(data)})
	}
	return mentions, ""
}

// workspaceFiles lists the files the @ picker offers: what git tracks, plus
// what it does not ignore. git already leaves out .git and the build output,
// so the picker offers the project rather than everything on disk.
func workspaceFiles(ctx context.Context, dir string) ([]chrome.MentionFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	list := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	list.Dir = dir
	out, err := list.Output()
	if err != nil {
		return nil, err
	}
	var files []chrome.MentionFile
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		name := filepath.Base(path)
		files = append(files, chrome.MentionFile{
			Path: path, Name: name, Dir: mentionDir(path),
			LowerName: strings.ToLower(name), LowerPath: strings.ToLower(path),
		})
	}
	return files, nil
}

// mentionDir is a file's folder as a row shows it: the folder, and nothing for
// a file at the top of the project.
func mentionDir(path string) string {
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// openMentions reads the project's files for the @ picker. It runs off the UI
// thread and repaints: the picker opens on a keystroke, and listing a large
// project must not stall the message being written. Each opening rereads, so a
// file added a moment ago is offered.
func (c *Coordinator) openMentions(s *Session) {
	if s == nil || s.project == "" {
		return
	}
	s.mu.Lock()
	if s.mentionLoading {
		s.mu.Unlock()
		return
	}
	s.mentionLoading, s.mentionErr = true, nil
	project := s.project
	s.mu.Unlock()
	s.invalidate()
	go func() {
		files, err := workspaceFiles(context.Background(), project)
		s.mu.Lock()
		s.mentionLoading, s.mentionErr = false, err
		if err == nil {
			s.mentionFiles = files
		}
		s.mu.Unlock()
		s.invalidate()
	}()
}

// sessionMentionFiles projects the file list into the picker. It reads only:
// the render pass must not start a git call of its own.
func (c *Coordinator) sessionMentionFiles(s *Session) ([]chrome.MentionFile, bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	problem := ""
	if s.mentionErr != nil {
		problem = "This project's files could not be listed"
	}
	return s.mentionFiles, s.mentionLoading, problem
}
