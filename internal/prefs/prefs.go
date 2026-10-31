package prefs

import (
	"encoding/json"
	"os"

	"fx-desktop/internal/configdir"
)

// Appearance controls terminal chrome.
type Appearance string

const (
	AppearanceSystem Appearance = "system"
	AppearanceDark   Appearance = "dark"
	AppearanceLight  Appearance = "light"
)

// Prefs are user settings persisted on disk.
type Prefs struct {
	DefaultProjectDir   string     `json:"defaultProjectDir,omitempty"`
	FontFamily          string     `json:"fontFamily,omitempty"`
	FontSize            float64    `json:"fontSize,omitempty"`
	Appearance          Appearance `json:"appearance,omitempty"`
	RestoreLastProject  bool       `json:"restoreLastProject,omitempty"`
	TrackRecentProjects bool       `json:"trackRecentProjects,omitempty"`
	CloseToTray         bool       `json:"closeToTray,omitempty"`
	CheckUpdatesOnStart bool       `json:"checkUpdatesOnStart,omitempty"`
	LastProject         string     `json:"lastProject,omitempty"`
}

// Defaults returns factory defaults.
func Defaults() Prefs {
	return Prefs{
		FontSize:            14,
		Appearance:          AppearanceSystem,
		RestoreLastProject:  true,
		TrackRecentProjects: true,
		CheckUpdatesOnStart: false,
	}
}

// Load reads prefs from disk, or returns defaults when missing.
func Load() (Prefs, error) {
	p := Defaults()
	path, err := configdir.PrefsPath()
	if err != nil {
		return p, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return Defaults(), err
	}
	if p.FontSize <= 0 {
		p.FontSize = 14
	}
	if p.Appearance == "" {
		p.Appearance = AppearanceSystem
	}
	return p, nil
}

// Save writes prefs to disk.
func (p Prefs) Save() error {
	path, err := configdir.PrefsPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
