package fxbin

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	githubLatestAPI = "https://api.github.com/repos/vercel-labs/fx/releases/latest"
	installDirName  = ".local/bin"
)

// releaseLatestURL is the GitHub releases API endpoint (overridable in tests).
var releaseLatestURL = githubLatestAPI

// installHTTPClient when non-nil is used for Install downloads (tests).
var installHTTPClient *http.Client

// ResolvePath finds the fx executable.
func ResolvePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("FX_DESKTOP_FX_PATH")); override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return override, nil
		}
		return "", fmt.Errorf("FX_DESKTOP_FX_PATH is not a file: %s", override)
	}
	if p, err := exec.LookPath("fx"); err == nil {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	for _, rel := range []string{filepath.Join(installDirName, "fx"), filepath.Join("bin", "fx")} {
		candidate := filepath.Join(home, rel)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("fx not found; install it from https://fx.sh or use Install")
}

// Version runs a bounded, read-only version query. It must still be called
// off the UI thread: even a healthy CLI can take time to start.
func Version(fxPath string) (string, error) {
	return VersionContext(context.Background(), fxPath)
}

// VersionContext shares one deadline across --version and the help fallback.
// WaitDelay also bounds inherited output pipes: killing a wrapper does not
// necessarily close stdout held by one of its children.
func VersionContext(parent context.Context, fxPath string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	query := func(arg string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, fxPath, arg)
		cmd.WaitDelay = 100 * time.Millisecond
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return out, err
	}
	out, err := query("--version")
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) {
			return "", err
		}
		// Some builds only expose version via help.
		out, err = query("help")
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "fx ") || strings.Contains(line, "v0.") {
				return line, nil
			}
		}
		return strings.TrimSpace(string(out)), nil
	}
	return strings.TrimSpace(string(out)), nil
}

// Unrunnable reports whether err means the executable could not be used at
// all, as opposed to a CLI that did not answer in time.
//
// A version query that times out says nothing about the binary: fx can be
// slow to start on a cold cache. A binary the OS refused to run — killed
// before it got going because its code signature is invalid, not an
// executable format, not executable — will never work, and the caller needs
// to tell the user instead of letting every session fail silently.
func Unrunnable(err error) bool {
	if err == nil {
		return false
	}
	// A deadline or cancellation is transient: keep using the binary.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	// WaitDelay fires when a killed process left a child holding the
	// output pipe. The binary's own state is still unknown.
	if errors.Is(err, exec.ErrWaitDelay) {
		return false
	}
	return true
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func platformAsset() (string, error) {
	var goos, arch string
	switch runtime.GOOS {
	case "darwin":
		goos = "macos"
	case "linux":
		goos = "linux"
	default:
		return "", fmt.Errorf("fx does not support %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "arm64":
		arch = "aarch64"
	case "amd64":
		arch = "x86_64"
	default:
		return "", fmt.Errorf("unsupported architecture %s", runtime.GOARCH)
	}
	return fmt.Sprintf("fx-%s-%s.tar.gz", goos, arch), nil
}

// InstallDir is ~/.local/bin where official installer places fx.
func InstallDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, installDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// InstallTarget is the path Install writes to.
func InstallTarget() (string, error) {
	dir, err := InstallDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "fx"), nil
}

// Install downloads the latest fx release binary for this platform.
//
// The install is atomic. The archive is downloaded to a temporary file, its
// digest is checked against the release's published .sha256, and the binary
// is extracted to a temporary file that is renamed over the target only once
// it is complete. A download that dies halfway therefore leaves the
// previously working CLI untouched instead of replacing it with a truncated
// file that macOS refuses to execute (SIGKILL, Code Signature Invalid).
func Install(ctx context.Context) (string, error) {
	assetName, err := platformAsset()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseLatestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "fx-desktop")
	resp, err := installDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API: %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	var downloadURL string
	for _, a := range rel.Assets {
		if a.Name == assetName {
			downloadURL = a.URL
			break
		}
	}
	if downloadURL == "" {
		return "", fmt.Errorf("release asset %q not found", assetName)
	}
	target, err := InstallTarget()
	if err != nil {
		return "", err
	}
	archivePath, err := downloadArchive(ctx, downloadURL, target)
	if err != nil {
		return "", err
	}
	defer os.Remove(archivePath)
	if err := verifyChecksum(ctx, downloadURL, assetName, archivePath); err != nil {
		return "", err
	}
	if err := extractFxBinary(archivePath, target); err != nil {
		return "", err
	}
	return target, nil
}

// downloadArchive streams the release asset to a temporary file next to the
// install target. The temporary file is removed on failure.
func downloadArchive(ctx context.Context, downloadURL, target string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "fx-desktop")
	resp, err := installDo(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", resp.Status)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "fx-download-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", err
	}
	return tmpPath, nil
}

// verifyChecksum compares the downloaded archive against the digest the
// release publishes next to it. The digest is the only way to tell a
// complete download from a truncated one before the binary is replaced.
//
// A release that publishes no digest still installs: the check is a guard
// against corruption, not a new requirement that upstream could break.
func verifyChecksum(ctx context.Context, downloadURL, assetName, archivePath string) error {
	digestURL := downloadURL + ".sha256"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, digestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "fx-desktop")
	resp, err := installDo(req)
	if err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// No published digest: nothing to compare against.
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	want, err := parseChecksum(string(body))
	if err != nil {
		return err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("checksum: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", assetName, got, want)
	}
	return nil
}

// parseChecksum reads the hex digest from a sha256sum-style line. The file
// holds "<digest>  <name>"; only the digest is needed.
func parseChecksum(text string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		field := strings.Fields(line)
		if len(field) == 0 {
			continue
		}
		digest := field[0]
		if len(digest) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(digest); err != nil {
			continue
		}
		return digest, nil
	}
	return "", errors.New("checksum: no sha256 digest found")
}

// extractFxBinary extracts the fx member from the archive to dest.
//
// The member is written to a temporary file next to dest and renamed into
// place only after the whole member has been read. Writing straight to dest
// truncated a working CLI whenever the download failed part-way through.
func extractFxBinary(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if name != "fx" {
			continue
		}
		out, err := os.CreateTemp(filepath.Dir(dest), ".fx-extract-*")
		if err != nil {
			return err
		}
		outPath := out.Name()
		defer os.Remove(outPath)
		// io.Copy reports the error when the member ends before the size
		// the header promised, so a short payload is never installed.
		written, err := io.Copy(out, tr)
		if err != nil {
			out.Close()
			return fmt.Errorf("extract fx: %w", err)
		}
		if written != hdr.Size {
			out.Close()
			return fmt.Errorf("extract fx: got %d bytes, want %d", written, hdr.Size)
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := os.Chmod(outPath, 0o755); err != nil {
			return err
		}
		// Rename over the target: the swap is atomic on the same
		// filesystem, so the old binary stays usable until the new one
		// is complete on disk.
		if err := os.Rename(outPath, dest); err != nil {
			return err
		}
		return nil
	}
	return errors.New("fx binary not found in archive")
}

// Upgrade runs `fx upgrade`.
func Upgrade(ctx context.Context, fxPath string) error {
	cmd := exec.CommandContext(ctx, fxPath, "upgrade")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ReleaseAssetName exposes the expected tarball name (for tests).
func ReleaseAssetName() (string, error) { return platformAsset() }

func installDo(req *http.Request) (*http.Response, error) {
	if installHTTPClient != nil {
		return installHTTPClient.Do(req)
	}
	return http.DefaultClient.Do(req)
}

// DefaultHTTPTimeout for install operations.
const DefaultHTTPTimeout = 5 * time.Minute
