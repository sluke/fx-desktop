package configdir

import (
	"os"
	"path/filepath"
	"runtime"
)

const appFolder = "fx-desktop"

// Root returns the per-user config directory for fx-desktop data files.
func Root() (string, error) {
	var base string
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "Library", "Application Support", appFolder)
	default:
		cfg, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(cfg, appFolder)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", err
	}
	return base, nil
}

func join(name string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, name), nil
}

// PrefsPath is prefs.json.
func PrefsPath() (string, error) { return join("prefs.json") }

// ProjectsPath is projects.json.
func ProjectsPath() (string, error) { return join("projects.json") }

// SessionStatsPath is session-stats.json: the last known context and token
// counts of each fx conversation, kept because fx replays no usage on load.
func SessionStatsPath() (string, error) { return join("session-stats.json") }
