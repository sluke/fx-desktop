package main

import (
	"testing"

	"fx-desktop/internal/app"
	"fx-desktop/internal/fxbin"
	"fx-desktop/internal/prefs"
)

// Smoke tests that core packages link from the main module.
func TestModuleIntegrations(t *testing.T) {
	if prefs.Defaults().FontSize != 14 {
		t.Fatal("prefs defaults")
	}
	_, err := fxbin.ReleaseAssetName()
	if err != nil {
		t.Skip(err)
	}
	if _, ok := app.ProjectFromURL("fx-desktop://open?path=/x"); !ok {
		t.Fatal("deeplink")
	}
}
