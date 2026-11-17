package fxbin

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"fx-desktop/internal/testutil"
)

func r9ReleaseServer(t *testing.T, digestStatus int) *httptest.Server {
	t.Helper()
	asset, err := platformAsset()
	if err != nil {
		t.Skip(err)
	}
	archive, err := buildArchiveBytes([]byte("#!/bin/sh\necho 'fx 0.0.17'\n"))
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v0.0.17",
				Assets: []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				}{{Name: asset, URL: srv.URL + "/asset.tar.gz"}},
			})
		case "/asset.tar.gz":
			_, _ = w.Write(archive)
		case "/asset.tar.gz.sha256":
			if digestStatus == 0 {
				_, _ = fmt.Fprintf(w, "%x  %s\n", sha256.Sum256(archive), asset)
				return
			}
			w.WriteHeader(digestStatus)
			_, _ = w.Write([]byte("upstream error"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Only "this release publishes no digest" skips verification. A digest
// request that was refused or failed used to install the archive unverified,
// while a transport error on the same request stopped the install.
func TestRegressionR9_ChecksumFetchFailureIsNotNoChecksum(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("fx platform")
	}
	for _, status := range []int{http.StatusInternalServerError, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := r9ReleaseServer(t, status)
			installAgainst(t, srv.URL+"/latest", srv.Client())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path, err := Install(ctx)
			if err == nil {
				t.Fatalf("digest request answered HTTP %d and the archive was installed unverified at %s", status, path)
			}
			if target, terr := InstallTarget(); terr == nil {
				if _, serr := os.Stat(target); serr == nil {
					t.Errorf("a refused install left a binary at %s", target)
				}
				left, _ := filepath.Glob(filepath.Join(filepath.Dir(target), "*fx-*"))
				if len(left) != 0 {
					t.Errorf("temporary files left behind: %v", left)
				}
			}
		})
	}
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(fmt.Sprintf("%d still installs", status), func(t *testing.T) {
			srv := r9ReleaseServer(t, status)
			installAgainst(t, srv.URL+"/latest", srv.Client())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := Install(ctx); err != nil {
				t.Fatalf("a release without a digest must keep installing: %v", err)
			}
		})
	}
}

// ResolvePath prefers PATH to the managed install, so a damaged fx on PATH
// was resolved again after a successful repair. A caller that knows which
// path failed now gets the managed binary instead.
func TestRegressionR9_ResolvePathAvoidingAnUnrunnableBinary(t *testing.T) {
	home := testutil.IsolateHome(t)
	pathDir := filepath.Join(t.TempDir(), "pathbin")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	onPath := filepath.Join(pathDir, "fx")
	if err := os.WriteFile(onPath, []byte("\x00\x01not an executable\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", pathDir)
	t.Setenv("FX_DESKTOP_FX_PATH", "")

	if got, err := ResolvePathAvoiding(onPath); err != nil || got != onPath {
		t.Fatalf("with no managed install there is nothing else to offer: %q, %v", got, err)
	}
	if _, ok := ManagedPath(); ok {
		t.Fatal("precondition: no managed install yet")
	}

	managed := filepath.Join(home, ".local", "bin", "fx")
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\necho 'fx 0.0.17'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := ManagedPath(); !ok || got != managed {
		t.Fatalf("ManagedPath = %q, %v", got, ok)
	}
	if got, err := ResolvePath(); err != nil || got != onPath {
		t.Fatalf("ResolvePath keeps its order: %q, %v", got, err)
	}
	if got, err := ResolvePathAvoiding(); err != nil || got != onPath {
		t.Fatalf("nothing to avoid: %q, %v", got, err)
	}
	if got, err := ResolvePathAvoiding(onPath); err != nil || got != managed {
		t.Fatalf("avoiding the damaged PATH binary = %q, %v; want the managed install", got, err)
	}
	// Both damaged: no candidate is handed back and forth.
	if got, err := ResolvePathAvoiding(onPath, managed); err != nil || got != onPath {
		t.Fatalf("with every candidate unrunnable = %q, %v; want the resolved path unchanged", got, err)
	}

	// An explicit override is the user's choice and is never replaced.
	override := filepath.Join(t.TempDir(), "custom-fx")
	if err := os.WriteFile(override, []byte("\x00broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_DESKTOP_FX_PATH", override)
	if !Overridden() {
		t.Fatal("Overridden() = false with the variable set")
	}
	if got, err := ResolvePathAvoiding(override); err != nil || got != override {
		t.Fatalf("an unrunnable override = %q, %v; want it returned as it is", got, err)
	}
	t.Setenv("FX_DESKTOP_FX_PATH", filepath.Join(t.TempDir(), "gone", "fx"))
	if got, err := ResolvePathAvoiding(onPath); err == nil {
		t.Fatalf("an override naming a missing file resolved to %q", got)
	}
}
