package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fx-desktop/internal/app/chrome"
	"fx-desktop/internal/fxhome"
	"fx-desktop/internal/safedir"
)

// A SKILL.md header is a small YAML block. These caps stop a file with no
// closing marker, or a huge one, from being read as a header: the parser stops
// at the marker or at the cap, whichever comes first (P4).
const (
	maxSkillHeaderBytes = 64 << 10
	maxSkillHeaderLines = 512
	// skillDirBatch bounds one directory read. A big discovery root is
	// enumerated in bounded pieces so cancellation stops real work instead
	// of only skipping the results of a finished walk (P5).
	skillDirBatch = 64
)

var (
	// maxSkillEntries bounds one scan across all discovery roots.
	maxSkillEntries = 10000
)

type skillRoot struct{ path, source string }

// skillRoots follows the discovery roots fx documents in its TUI. The order
// is workspace first, then user-wide compatibility roots, so a project skill
// is the one a user sees first.
func skillRoots(workspace, stateRoot, home string) []skillRoot {
	var roots []skillRoot
	if workspace != "" {
		for _, name := range []string{filepath.Join(".fx", "skills"), "skills", filepath.Join(".opencode", "skills"), filepath.Join(".codex", "skills"), filepath.Join(".claude", "skills"), filepath.Join(".agents", "skills"), filepath.Join(".claw", "skills")} {
			roots = append(roots, skillRoot{filepath.Join(workspace, name), "workspace " + name})
		}
	}
	if stateRoot != "" {
		roots = append(roots, skillRoot{filepath.Join(stateRoot, "skills"), "global .fx/skills"})
	}
	if home != "" {
		for _, name := range []string{filepath.Join(".config", "opencode", "skills"), filepath.Join(".codex", "skills"), filepath.Join(".claude", "skills"), filepath.Join(".agents", "skills"), filepath.Join(".claw", "skills")} {
			roots = append(roots, skillRoot{filepath.Join(home, name), "global " + name})
		}
	}
	return roots
}

// skillScanResult is one walk of the discovery roots. Problems names roots
// that could not be read; Partial is true when cancellation stopped the walk.
type skillScanResult struct {
	Skills   []chrome.SkillInfo
	Problems []string
	Partial  bool
}

func scanSkills(ctx context.Context, workspace, stateRoot, home string) skillScanResult {
	var result skillScanResult
	if ctx == nil {
		ctx = context.Background()
	}
	seen := make(map[string]bool)
	total := 0
	problem := func(text string) {
		if text != "" {
			result.Problems = append(result.Problems, text)
		}
	}
	// Every return goes through finish (C6). Sorting and the Partial/Problems
	// contract are result normalization, not a side effect of the happy path:
	// a budget, a cancellation or an unreadable root must leave the rows it
	// did collect in the same deterministic order. Sorting never resumes a
	// cancelled walk; it only orders what was already read.
	finish := func(reason string, partial bool) skillScanResult {
		sort.SliceStable(result.Skills, func(i, j int) bool {
			if result.Skills[i].Name != result.Skills[j].Name {
				return result.Skills[i].Name < result.Skills[j].Name
			}
			return result.Skills[i].Source < result.Skills[j].Source
		})
		if partial {
			result.Partial = true
			problem(reason)
		}
		return result
	}
	if err := ctx.Err(); err != nil {
		return finish("skill scan stopped: "+err.Error(), true)
	}
	for _, root := range skillRoots(workspace, stateRoot, home) {
		if err := ctx.Err(); err != nil {
			return finish("skill scan stopped: "+err.Error(), true)
		}
		f, err := safedir.Open(root.path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				problem(root.path + ": " + err.Error())
			}
			if ctx.Err() != nil {
				return finish("skill scan stopped: "+ctx.Err().Error(), true)
			}
			continue
		}
		done := false
		for !done {
			if err := ctx.Err(); err != nil {
				f.Close()
				return finish("skill scan stopped: "+err.Error(), true)
			}
			entries, readErr := f.ReadDir(skillDirBatch)
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					f.Close()
					return finish("skill scan stopped: "+err.Error(), true)
				}
				if total >= maxSkillEntries {
					f.Close()
					return finish("skill scan stopped at the entry budget", true)
				}
				total++
				dir := filepath.Join(root.path, entry.Name())
				info, err := os.Stat(dir)
				if err != nil || !info.IsDir() {
					continue
				}
				skillFile := filepath.Join(dir, "SKILL.md")
				// A special file here would block the reader and is not a skill
				// fx can load (P4).
				st, err := os.Stat(skillFile)
				if err != nil || !st.Mode().IsRegular() {
					continue
				}
				resolved, _ := filepath.EvalSymlinks(dir)
				if resolved == "" {
					resolved = dir
				}
				if seen[resolved] {
					continue
				}
				seen[resolved] = true
				name, description := parseSkillHeader(skillFile)
				if name == "" {
					name = entry.Name()
				}
				result.Skills = append(result.Skills, chrome.SkillInfo{Name: name, Description: description, Source: root.source, Path: dir})
			}
			if errors.Is(readErr, io.EOF) {
				done = true
			} else if readErr != nil {
				problem(root.path + ": " + readErr.Error())
				done = true
			}
		}
		f.Close()
	}
	if err := ctx.Err(); err != nil {
		return finish("skill scan stopped: "+err.Error(), true)
	}
	return finish("", false)
}

// parseSkillHeader streams the small YAML frontmatter fx uses and stops at
// the closing marker or at the header cap. It never loads the rest of the
// skill: a 16 MiB SKILL.md costs one header, not one file. It stays
// forgiving — a bad header leaves the directory name to the caller — but a
// file without a closing marker cannot make it read forever.
func parseSkillHeader(path string) (name, description string) {
	f, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", ""
	}
	reader := bufio.NewReaderSize(io.LimitReader(f, maxSkillHeaderBytes), 4096)
	first, err := reader.ReadString('\n')
	if strings.TrimSpace(first) != "---" {
		return "", ""
	}
	if err != nil && first == "" {
		return "", ""
	}
	blockKey := ""
	var block []string
	flush := func() {
		if blockKey == "description" && len(block) > 0 {
			description = strings.TrimSpace(strings.Join(block, " "))
		}
		blockKey = ""
		block = nil
	}
	for lines := 0; lines < maxSkillHeaderLines; lines++ {
		raw, readErr := reader.ReadString('\n')
		if raw == "" && readErr != nil {
			break
		}
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if line == "---" || line == "..." {
			flush()
			return name, description
		}
		if blockKey != "" {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || line == "" {
				block = append(block, strings.TrimSpace(line))
			} else {
				flush()
			}
		}
		if blockKey == "" {
			key, value, ok := strings.Cut(line, ":")
			if ok {
				key = strings.TrimSpace(key)
				value = strings.TrimSpace(value)
				switch key {
				case "name":
					name = strings.Trim(value, `"'`)
				case "description":
					if value == ">" || value == ">-" || value == "|" || value == "|-" {
						blockKey = "description"
					} else {
						description = strings.Trim(value, `"'`)
					}
				}
			}
		}
		if readErr != nil {
			break
		}
	}
	flush()
	return name, description
}

func (c *Coordinator) runSkills(s *ProjectWindow) {
	state := &s.shell.Tools
	if state.Busy {
		return
	}
	state.Generation++
	generation := state.Generation
	state.Busy = true
	state.SkillProblem = ""
	s.invalidate()
	workspace := s.project
	// Fix the context and generation in the thread that received the
	// request. Creating the scope inside the goroutine let a late-starting
	// old worker cancel a newer request's context (P5).
	scanCtx, stop := s.scope("skills")
	go func() {
		defer stop()
		home, err := os.UserHomeDir()
		if err != nil {
			home = ""
		}
		stateRoot, rootErr := fxhome.Root()
		if rootErr != nil {
			stateRoot = ""
		}
		scan := scanSkills(scanCtx, workspace, stateRoot, home)
		s.dispatch(func() {
			if state.Generation != generation || !state.Open {
				return
			}
			state.Busy = false
			state.Skills = scan.Skills
			switch {
			case rootErr != nil:
				// Never fall back to another store and never present a
				// partial scan as complete: the state roots were skipped
				// for a reason the user has to fix (H4).
				state.SkillProblem = "fx state root: " + rootErr.Error()
			case scan.Partial:
				state.SkillProblem = "Skill scan stopped early."
			case len(scan.Problems) > 0:
				state.SkillProblem = strings.Join(scan.Problems, "; ")
			case len(scan.Skills) == 0:
				state.SkillProblem = "No skills found in fx's discovery roots."
			}
			s.invalidate()
		})
	}()
}
