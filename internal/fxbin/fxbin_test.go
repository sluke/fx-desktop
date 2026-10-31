package fxbin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"fx-desktop/internal/testutil"
)

func TestReleaseAssetName(t *testing.T) {
	name, err := ReleaseAssetName()
	if err != nil {
		t.Skip(err)
	}
	if !strings.HasPrefix(name, "fx-") || !strings.HasSuffix(name, ".tar.gz") {
		t.Fatalf("unexpected asset: %q", name)
	}
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		t.Skip("fx unsupported on this OS")
	}
}

func TestResolvePathEnvOverride(t *testing.T) {
	home := testutil.IsolateHome(t)
	fx := filepath.Join(home, "custom-fx")
	if err := os.WriteFile(fx, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FX_DESKTOP_FX_PATH", fx)
	got, err := ResolvePath()
	if err != nil || got != fx {
		t.Fatalf("ResolvePath() = %q, %v", got, err)
	}
}

func TestResolvePathEnvOverrideMissing(t *testing.T) {
	testutil.IsolateHome(t)
	t.Setenv("FX_DESKTOP_FX_PATH", "/no/such/fx")
	_, err := ResolvePath()
	if err == nil || !strings.Contains(err.Error(), "FX_DESKTOP_FX_PATH") {
		t.Fatalf("expected env error, got %v", err)
	}
}

func TestResolvePathHomeLocalBin(t *testing.T) {
	home := testutil.IsolateHome(t)
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fx := filepath.Join(binDir, "fx")
	if err := os.WriteFile(fx, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath()
	if err != nil || got != fx {
		t.Fatalf("ResolvePath() = %q, %v", got, err)
	}
}

func TestResolvePathHomeBin(t *testing.T) {
	home := testutil.IsolateHome(t)
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fx := filepath.Join(binDir, "fx")
	if err := os.WriteFile(fx, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath()
	if err != nil || got != fx {
		t.Fatalf("ResolvePath() = %q, %v", got, err)
	}
}

func TestResolvePathNotFound(t *testing.T) {
	testutil.IsolateHome(t)
	_, err := ResolvePath()
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestVersionFromScript(t *testing.T) {
	home := testutil.IsolateHome(t)
	fx := filepath.Join(home, "fx-mock")
	script := "#!/bin/sh\necho 'fx v0.0.13'\n"
	if err := os.WriteFile(fx, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := Version(fx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v, "0.0.13") {
		t.Fatalf("version %q", v)
	}
}

func TestVersionFromHelpFallback(t *testing.T) {
	home := testutil.IsolateHome(t)
	fx := filepath.Join(home, "fx-help")
	script := "#!/bin/sh\ncase \"$1\" in\n--version) exit 1 ;;\nhelp) echo 'fx v0.0.1-beta'; exit 0 ;;\nesac\nexit 1\n"
	if err := os.WriteFile(fx, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	v, err := Version(fx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v, "v0.0.1") {
		t.Fatalf("version %q", v)
	}
}

func TestVersionContextBoundsHungCLI(t *testing.T) {
	for _, arg := range []string{"--version", "help"} {
		t.Run(arg, func(t *testing.T) {
			home := testutil.IsolateHome(t)
			path := filepath.Join(home, "fx")
			script := "#!/bin/sh\nif [ \"$1\" = \"" + arg + "\" ]; then exec /bin/sleep 10; fi\nexit 1\n"
			if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := VersionContext(ctx, path)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected a deadline error, got %v", err)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("query exceeded its deadline: %v", elapsed)
			}
		})
	}
}

func TestVersionContextBoundsInheritedOutputPipe(t *testing.T) {
	home := testutil.IsolateHome(t)
	path := filepath.Join(home, "fx")
	// The wrapper exits but its child still owns stdout. A process timeout
	// alone does not stop Cmd.Output from waiting for that pipe to close.
	script := "#!/bin/sh\n/bin/sleep 1 &\necho 0.0.13\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := Version(path); err == nil {
		t.Fatal("expected inherited stdout to hit WaitDelay")
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("waiting for a child pipe stalled the query: %v", elapsed)
	}
}

func TestExtractFxBinary(t *testing.T) {
	home := testutil.IsolateHome(t)
	archive := filepath.Join(home, "fx.tar.gz")
	dest := filepath.Join(home, "fx-out")
	if err := writeTestArchive(archive, []byte("mock-binary")); err != nil {
		t.Fatal(err)
	}
	if err := extractFxBinary(archive, dest); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "mock-binary" {
		t.Fatalf("dest content %q err %v", b, err)
	}
}

func TestExtractFxBinaryMissing(t *testing.T) {
	home := testutil.IsolateHome(t)
	archive := filepath.Join(home, "empty.tar.gz")
	if err := writeArchiveWithoutFx(archive); err != nil {
		t.Fatal(err)
	}
	err := extractFxBinary(archive, filepath.Join(home, "out"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing fx error, got %v", err)
	}
}

func writeArchiveWithoutFx(path string) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("readme")
	if err := tw.WriteHeader(&tar.Header{Name: "README", Mode: 0o644, Size: int64(len(body))}); err != nil {
		return err
	}
	if _, err := tw.Write(body); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func writeTestArchive(path string, fxBody []byte) error {
	if fxBody == nil {
		fxBody = []byte("other")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "prefix/fx",
		Mode: 0o755,
		Size: int64(len(fxBody)),
	}); err != nil {
		return err
	}
	if _, err := tw.Write(fxBody); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func TestInstallFromMockRelease(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("fx platform")
	}
	asset, err := platformAsset()
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := buildArchiveBytes([]byte("#!/bin/sh\necho installed\n"))
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v0.0.13",
				Assets: []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				}{{Name: asset, URL: srv.URL + "/asset.tar.gz"}},
			})
		case r.URL.Path == "/asset.tar.gz":
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	testutil.IsolateHome(t)
	oldURL := releaseLatestURL
	oldClient := installHTTPClient
	releaseLatestURL = srv.URL + "/latest"
	installHTTPClient = srv.Client()
	t.Cleanup(func() {
		releaseLatestURL = oldURL
		installHTTPClient = oldClient
	})

	ctx := context.Background()
	path, err := Install(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want, err := InstallTarget()
	if err != nil {
		t.Fatal(err)
	}
	if path != want {
		t.Fatalf("install path %q want %q", path, want)
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		t.Fatalf("installed binary missing: %v", err)
	}
}

func buildArchiveBytes(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "fx", Mode: 0o755, Size: int64(len(body))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(body); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestInstallMissingAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ghRelease{TagName: "v0", Assets: nil})
	}))
	defer srv.Close()

	testutil.IsolateHome(t)
	oldURL := releaseLatestURL
	oldClient := installHTTPClient
	releaseLatestURL = srv.URL
	installHTTPClient = srv.Client()
	t.Cleanup(func() {
		releaseLatestURL = oldURL
		installHTTPClient = oldClient
	})

	_, err := Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected asset error, got %v", err)
	}
}

func TestInstallDirAndTarget(t *testing.T) {
	home := testutil.IsolateHome(t)
	dir, err := InstallDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("install dir %q not under home %q", dir, home)
	}
	target, err := InstallTarget()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(target) != "fx" {
		t.Fatalf("target %q", target)
	}
}

// installAgainst points Install at a mock server until the test ends.
func installAgainst(t *testing.T, url string, client *http.Client) {
	t.Helper()
	testutil.IsolateHome(t)
	oldURL, oldClient := releaseLatestURL, installHTTPClient
	releaseLatestURL = url
	installHTTPClient = client
	t.Cleanup(func() {
		releaseLatestURL = oldURL
		installHTTPClient = oldClient
	})
}

func TestInstallRejectsNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	installAgainst(t, srv.URL, srv.Client())

	_, err := Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "GitHub API") {
		t.Fatalf("expected a GitHub API error, got %v", err)
	}
}

func TestInstallRejectsMalformedReleaseJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	installAgainst(t, srv.URL, srv.Client())

	if _, err := Install(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestInstallFailsWhenDownloadIsBroken(t *testing.T) {
	asset, err := platformAsset()
	if err != nil {
		t.Skip("fx platform")
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v0.0.13",
				Assets: []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				}{{Name: asset, URL: srv.URL + "/asset.tar.gz"}},
			})
		default:
			// Not a gzip stream, so the extract step must fail.
			_, _ = w.Write([]byte("this is not a tarball"))
		}
	}))
	defer srv.Close()
	installAgainst(t, srv.URL+"/latest", srv.Client())

	if _, err := Install(context.Background()); err == nil {
		t.Fatal("expected an extract error for a corrupt download")
	}
}

func TestUpgradeInvokesBinary(t *testing.T) {
	home := testutil.IsolateHome(t)
	logFile := filepath.Join(home, "upgrade.log")
	fx := filepath.Join(home, "fx")
	script := "#!/bin/sh\necho upgrade >>" + logFile + "\n"
	if err := os.WriteFile(fx, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Upgrade(context.Background(), fx); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(logFile)
	if err != nil || !strings.Contains(string(b), "upgrade") {
		t.Fatalf("upgrade log %q err %v", b, err)
	}
}

// A download that dies halfway must not leave a half-written binary where a
// working one used to be. The old code opened ~/.local/bin/fx with O_TRUNC
// and copied into it, so an interrupted transfer replaced a good CLI with a
// truncated file that macOS then refuses to run (SIGKILL, Code Signature
// Invalid). The install has to land atomically or not at all.
func TestInstallKeepsWorkingBinaryWhenDownloadIsTruncated(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("fx platform")
	}
	asset, err := platformAsset()
	if err != nil {
		t.Fatal(err)
	}
	// A large member so that the tar header is fully readable and the copy
	// dies part-way through the file, which is what the real failure looked
	// like: the header was fine, the payload was not.
	body := bytes.Repeat([]byte("A"), 4<<20)
	full, err := buildArchiveBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	// Cut the compressed stream so it ends mid-member.
	truncated := full[:len(full)*3/4]

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v0.0.13",
				Assets: []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				}{{Name: asset, URL: srv.URL + "/asset.tar.gz"}},
			})
		default:
			_, _ = w.Write(truncated)
		}
	}))
	defer srv.Close()

	testutil.IsolateHome(t)
	installAgainst(t, srv.URL+"/latest", srv.Client())

	// A working CLI is already installed.
	target, err := InstallTarget()
	if err != nil {
		t.Fatal(err)
	}
	good := []byte("#!/bin/sh\necho previous-good\n")
	if err := os.WriteFile(target, good, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Install(context.Background()); err == nil {
		t.Fatal("expected an error for a truncated download")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("the previous binary must survive a failed install: %v", err)
	}
	if string(got) != string(good) {
		t.Fatalf("a failed install replaced the working binary:\n got %q\nwant %q", got, good)
	}
}

// The published release ships a .sha256 next to every tarball. Verifying it
// is the only way to tell a complete download from a truncated one before
// the truncated file replaces a working CLI.
func TestInstallRejectsChecksumMismatch(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("fx platform")
	}
	asset, err := platformAsset()
	if err != nil {
		t.Fatal(err)
	}
	archiveBytes, err := buildArchiveBytes([]byte("#!/bin/sh\necho installed\n"))
	if err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v0.0.13",
				Assets: []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				}{{Name: asset, URL: srv.URL + "/asset.tar.gz"}},
			})
		case "/asset.tar.gz":
			_, _ = w.Write(archiveBytes)
		case "/asset.tar.gz.sha256":
			// Deliberately wrong digest.
			_, _ = w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  " + asset + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	testutil.IsolateHome(t)
	installAgainst(t, srv.URL+"/latest", srv.Client())

	if _, err := Install(context.Background()); err == nil {
		t.Fatal("expected a checksum mismatch error")
	} else if !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected a checksum error, got %v", err)
	}
	target, err := InstallTarget()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("a checksum mismatch must not install a binary: %v", err)
	}
}

// A release without a published checksum must still install: the digest is
// an extra guard, not a new requirement that upstream could break.
func TestInstallSucceedsWhenChecksumMatches(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("fx platform")
	}
	asset, err := platformAsset()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("#!/bin/sh\necho installed\n")
	archiveBytes, err := buildArchiveBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archiveBytes)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(ghRelease{
				TagName: "v0.0.13",
				Assets: []struct {
					Name string `json:"name"`
					URL  string `json:"browser_download_url"`
				}{{Name: asset, URL: srv.URL + "/asset.tar.gz"}},
			})
		case "/asset.tar.gz":
			_, _ = w.Write(archiveBytes)
		case "/asset.tar.gz.sha256":
			_, _ = w.Write([]byte(fmt.Sprintf("%x  %s\n", digest, asset)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	testutil.IsolateHome(t)
	installAgainst(t, srv.URL+"/latest", srv.Client())

	path, err := Install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("installed %q want %q", got, body)
	}
}

// The extracted file must be the complete member, not a prefix of it: the
// old code copied whatever the tar reader produced and ignored the size the
// header promised.
func TestExtractRejectsTruncatedMember(t *testing.T) {
	dir := t.TempDir()
	body := []byte("#!/bin/sh\necho installed\n")
	full, err := buildArchiveBytes(body)
	if err != nil {
		t.Fatal(err)
	}
	// A tar whose member header promises more bytes than the file holds.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "fx", Mode: 0o755, Size: int64(len(body)) * 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	truncated := buf.Bytes()
	if len(truncated) >= len(full) {
		t.Fatalf("expected the truncated archive to be smaller")
	}

	archive := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archive, truncated, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "fx")
	if err := extractFxBinary(archive, dest); err == nil {
		t.Fatal("expected an error for a truncated member")
	}
}

// A CLI that is merely slow must not be reported as broken: the caller
// sends the user to reinstall a perfectly good binary otherwise.
func TestUnrunnableDistinguishesTimeoutFromBrokenBinary(t *testing.T) {
	if Unrunnable(nil) {
		t.Fatal("no error is not an unrunnable binary")
	}
	if Unrunnable(context.DeadlineExceeded) {
		t.Fatal("a timeout says nothing about the binary")
	}
	if Unrunnable(context.Canceled) {
		t.Fatal("a cancellation says nothing about the binary")
	}
	if Unrunnable(exec.ErrWaitDelay) {
		t.Fatal("an inherited output pipe says nothing about the binary")
	}
	if !Unrunnable(errors.New("signal: killed")) {
		t.Fatal("a binary the OS killed cannot be used")
	}
	if !Unrunnable(errors.New("fork/exec: bad CPU type")) {
		t.Fatal("a binary that cannot be executed cannot be used")
	}
}

// The distinction has to hold for a real slow binary, not just for a
// synthetic error value.
func TestVersionTimeoutIsNotUnrunnable(t *testing.T) {
	home := testutil.IsolateHome(t)
	path := filepath.Join(home, "fx")
	script := "#!/bin/sh\nexec /bin/sleep 10\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := VersionContext(ctx, path)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	if Unrunnable(err) {
		t.Fatalf("a slow binary was reported as unrunnable: %v", err)
	}
}
