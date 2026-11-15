package fxhome

import (
	"path/filepath"
	"testing"
)

func TestRootUsesFXHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("FX_HOME", root)
	got, err := Root()
	if err != nil || got != root {
		t.Fatalf("Root() = %q, %v", got, err)
	}
}

func TestRootRejectsRelativeFXHome(t *testing.T) {
	t.Setenv("FX_HOME", "relative/fx")
	if _, err := Root(); err == nil {
		t.Fatal("relative FX_HOME was accepted")
	}
}

func TestRootDefaultsToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FX_HOME", "")
	got, err := Root()
	if err != nil || got != filepath.Join(home, ".fx") {
		t.Fatalf("Root() = %q, %v", got, err)
	}
}
