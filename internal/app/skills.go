package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fx-desktop/internal/app/chrome"
)

type skillRoot struct{ path, source string }

// skillRoots follows the discovery roots fx documents in its TUI. The order
// is workspace first, then user-wide compatibility roots, so a project skill
// is the one a user sees first.
func skillRoots(workspace, home string) []skillRoot {
	var roots []skillRoot
	if workspace != "" {
		for _, name := range []string{filepath.Join(".fx", "skills"), "skills", filepath.Join(".opencode", "skills"), filepath.Join(".codex", "skills"), filepath.Join(".claude", "skills"), filepath.Join(".agents", "skills"), filepath.Join(".claw", "skills")} {
			roots = append(roots, skillRoot{filepath.Join(workspace, name), "workspace " + name})
		}
	}
	if home != "" {
		for _, name := range []string{filepath.Join(".fx", "skills"), filepath.Join(".config", "opencode", "skills"), filepath.Join(".codex", "skills"), filepath.Join(".claude", "skills"), filepath.Join(".agents", "skills"), filepath.Join(".claw", "skills")} {
			roots = append(roots, skillRoot{filepath.Join(home, name), "global " + name})
		}
	}
	return roots
}

func scanSkills(workspace, home string) []chrome.SkillInfo {
	var out []chrome.SkillInfo
	seen := make(map[string]bool)
	for _, root := range skillRoots(workspace, home) {
		entries, err := os.ReadDir(root.path)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			dir := filepath.Join(root.path, entry.Name())
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			skillFile := filepath.Join(dir, "SKILL.md")
			if st, err := os.Stat(skillFile); err != nil || st.IsDir() {
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
			out = append(out, chrome.SkillInfo{Name: name, Description: description, Source: root.source, Path: dir})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Source < out[j].Source
	})
	return out
}

// parseSkillHeader reads only the small YAML frontmatter fx uses. It is
// deliberately forgiving: a bad header still leaves a usable name from the
// directory, and the full skill stays owned by fx.
func parseSkillHeader(path string) (name, description string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	if len(data) > 256*1024 {
		data = data[:256*1024]
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", ""
	}
	lines := strings.Split(text[4:], "\n")
	blockKey := ""
	var block []string
	flush := func() {
		if blockKey == "description" && len(block) > 0 {
			description = strings.TrimSpace(strings.Join(block, " "))
		}
		blockKey = ""
		block = nil
	}
	for _, line := range lines {
		if line == "---" || line == "..." {
			break
		}
		if blockKey != "" {
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || line == "" {
				block = append(block, strings.TrimSpace(line))
				continue
			}
			flush()
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			name = strings.Trim(value, `"'`)
		case "description":
			if value == ">" || value == ">-" || value == "|" || value == "|-" {
				blockKey = "description"
				continue
			}
			description = strings.Trim(value, `"'`)
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
	go func() {
		home, err := os.UserHomeDir()
		if err != nil {
			home = ""
		}
		skills := scanSkills(workspace, home)
		s.dispatch(func() {
			if state.Generation != generation || !state.Open {
				return
			}
			state.Busy = false
			state.Skills = skills
			if len(skills) == 0 {
				state.SkillProblem = "No skills found in fx's discovery roots."
			}
			s.invalidate()
		})
	}()
}
