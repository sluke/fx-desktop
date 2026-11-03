// Package workspace owns the project's own facts: the files it holds, the
// references a message names, the changes git reports and the line
// classification a diff needs. It knows nothing about windows, palettes or
// MyGo; the app maps its data into the UI's presentation types.
package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// File is one file the @ picker can offer. Path is what a mention writes into
// the message — the project path, the way fx's own picker writes it; Name and
// Dir are what a row shows.
type File struct {
	Path string
	Name string
	Dir  string
	// LowerName and LowerPath are Name and Path folded once, when the project
	// is listed, so a frame does not fold the whole project again.
	LowerName string
	LowerPath string
}

// Mention is one file a message names with @path: the workspace path as
// written, the absolute path fx is told, and the bytes it is given. The
// content rides in the block because that is where fx reads it from.
type Mention struct {
	Path string
	Abs  string
	Text string
}

// Mentions are held to fx's own sizes, so a file a message attaches is no
// larger than the project instructions fx reads itself: 64 KiB for one file,
// 128 KiB for all of them together. A file over that is refused where it names
// the message, rather than sent as a mention that carries a truncation nobody
// asked for.
const (
	MaxMentionBytes      = 64 * 1024
	MaxMentionTotalBytes = 128 * 1024
)

// MentionBreak reports whether a rune leaves an @ opening a word: the space
// between words, or the punctuation a sentence wraps a name in.
func MentionBreak(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("([{\"'", r)
}

// MentionEnd reports whether a rune ends the path a mention names.
func MentionEnd(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(",;:)]}\"'", r)
}

// TrimMention drops the punctuation a sentence leaves on the end of a name:
// "@main.go," names main.go. Only the end is touched, so a name with dots in
// it keeps them.
func TrimMention(path string) string { return strings.TrimRight(path, ".,;:!?") }

// MentionPaths lists the paths a message names with @path, in the order they
// appear and without repeats. The picker and the sender must read a message the
// same way, so the rule lives here, once.
func MentionPaths(text string) []string {
	var paths []string
	seen := make(map[string]bool)
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' || (i > 0 && !MentionBreak(runes[i-1])) {
			continue
		}
		end := i + 1
		for end < len(runes) && !MentionEnd(runes[end]) {
			end++
		}
		if path := TrimMention(string(runes[i+1 : end])); path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
		i = end - 1
	}
	return paths
}

// ResolvePath resolves a mention to a file in the project. An absolute path,
// or one that climbs out of the project, is not a mention: the file is read
// and given to fx, so it has to be one of this project's own.
func ResolvePath(project, token string) (string, bool) {
	if token == "" || filepath.IsAbs(token) {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(token))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.Join(project, clean), true
}

// MentionDir is a file's folder as a row shows it: the folder, and nothing for
// a file at the top of the project.
func MentionDir(path string) string {
	dir := filepath.ToSlash(filepath.Dir(path))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// HumanSize renders a byte count for a sentence about a limit.
func HumanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
