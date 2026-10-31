package app

import (
	"net/url"
	"path/filepath"
	"strings"
)

// ProjectFromURL parses fx-desktop://open?path=/abs/project.
func ProjectFromURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	if u.Scheme != "fx-desktop" {
		return "", false
	}
	switch strings.ToLower(u.Host) {
	case "open", "":
		path := u.Query().Get("path")
		if path == "" {
			return "", false
		}
		return filepath.Clean(path), true
	default:
		return "", false
	}
}

// ProjectFromArg returns an absolute project path from a CLI argument.
func ProjectFromArg(arg string, wd string) (string, bool) {
	if arg == "" {
		return "", false
	}
	if strings.Contains(arg, "://") {
		return ProjectFromURL(arg)
	}
	// A leading dash marks a launch flag, not a project: treating it as a
	// path would open an error dialog for every flag a second instance or
	// `mygo dev` happens to pass.
	if strings.HasPrefix(arg, "-") {
		return "", false
	}
	if !filepath.IsAbs(arg) {
		arg = filepath.Join(wd, arg)
	}
	return filepath.Clean(arg), true
}
