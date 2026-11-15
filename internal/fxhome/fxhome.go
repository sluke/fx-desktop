// Package fxhome resolves the fx CLI state directory. It is not the
// fx-desktop preferences directory: FX_HOME moves rules, sessions and skills
// together, and a bad value must fail instead of falling back to another store.
package fxhome

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root is FX_HOME when it is set to an absolute path, otherwise ~/.fx.
func Root() (string, error) {
	if v := strings.TrimSpace(os.Getenv("FX_HOME")); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("FX_HOME must be an absolute path, got %q", v)
		}
		return filepath.Clean(v), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".fx"), nil
}
