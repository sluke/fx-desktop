package app

import (
	"net/url"
	"path/filepath"
	"strings"
)

// ProjectFromURL parses fx-desktop://open?path=/abs/project.
//
// The path must be absolute. A link is handed over by the system from
// wherever it was clicked, so a relative path would be resolved against
// whatever directory the app happened to start in.
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
		if path == "" || !filepath.IsAbs(path) {
			return "", false
		}
		return filepath.Clean(path), true
	default:
		return "", false
	}
}

// ProjectRequest is a project a launch argument names, and whether a link
// named it. A path typed after the command is the user's own request; a link
// can come from a web page, and is opened only once the user has agreed to
// the folder (Coordinator.OpenProjectFromLink).
type ProjectRequest struct {
	Path     string
	FromLink bool
}

// ProjectRequestFromArg reads one launch argument.
func ProjectRequestFromArg(arg string, wd string) (ProjectRequest, bool) {
	if arg == "" {
		return ProjectRequest{}, false
	}
	if strings.Contains(arg, "://") {
		path, ok := ProjectFromURL(arg)
		return ProjectRequest{Path: path, FromLink: true}, ok
	}
	// A leading dash marks a launch flag, not a project: treating it as a
	// path would open an error dialog for every flag a second instance or
	// `mygo dev` happens to pass.
	if strings.HasPrefix(arg, "-") {
		return ProjectRequest{}, false
	}
	if !filepath.IsAbs(arg) {
		arg = filepath.Join(wd, arg)
	}
	return ProjectRequest{Path: filepath.Clean(arg)}, true
}

// ProjectFromArg returns an absolute project path from a CLI argument.
func ProjectFromArg(arg string, wd string) (string, bool) {
	request, ok := ProjectRequestFromArg(arg, wd)
	return request.Path, ok
}
