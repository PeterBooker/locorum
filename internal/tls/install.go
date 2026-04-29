package tls

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/PeterBooker/locorum/internal/utils"
)

// MkcertVersion is the pinned upstream release used by EnsureBinary. Bumping
// this string is the only place we re-point the auto-download.
const MkcertVersion = "v1.4.4"

// downloadTimeout caps a single binary download. mkcert is ~5 MB; one minute
// is generous on a slow link and short enough to surface dead networks.
const downloadTimeout = 60 * time.Second

// EnsureBinary returns the path to a usable mkcert binary, downloading it
// into m.binDir if no existing binary is found. Safe to call repeatedly:
// once a binary exists in any of the resolution slots, the download is
// skipped. Errors only when no binary is present and the download fails
// (unsupported platform, network failure, write failure).
func (m *Mkcert) EnsureBinary(ctx context.Context) (string, error) {
	if bin := m.resolveBinary(); bin != "" {
		return bin, nil
	}
	if m.binDir == "" {
		return "", fmt.Errorf("mkcert not found and auto-download disabled (binDir empty)")
	}

	url, err := mkcertDownloadURL()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(m.binDir, 0o755); err != nil {
		return "", fmt.Errorf("create bin dir: %w", err)
	}

	name := "mkcert"
	if runtime.GOOS == "windows" {
		name = "mkcert.exe"
	}
	dst := filepath.Join(m.binDir, name)

	if err := downloadBinary(ctx, url, dst); err != nil {
		return "", err
	}
	m.invalidate()
	slog.Info("downloaded mkcert", "url", url, "dst", dst)
	return dst, nil
}

// installTimeout caps the mkcert -install run. The trust-store updates are
// fast under normal conditions; a longer hang almost always means an
// interactive prompt (sudo, polkit) we can't service from a GUI process.
// Killing the child process via context cancellation is preferable to
// freezing the banner.
const installTimeout = 90 * time.Second

// InstallCA runs `mkcert -install` to generate (if needed) and install the
// local CA into the OS / browser trust stores. Calls EnsureBinary first so
// the user doesn't need a separate setup step. mkcert prints non-fatal
// warnings (e.g. failed sudo for the system store on Linux) but still
// succeeds and writes the rootCA file — combined output is captured and
// surfaced verbatim on error so the UI can show what happened.
//
// On Linux the system trust store install needs sudo, which would hang a
// GUI process with no TTY; we set TRUST_STORES=nss so mkcert only writes
// to user-level NSS DBs (Firefox + Chromium-family). Users who want full
// curl/wget/java trust can re-run `mkcert -install` from a terminal.
func (m *Mkcert) InstallCA(ctx context.Context) error {
	bin, err := m.EnsureBinary(ctx)
	if err != nil {
		return err
	}

	ictx, cancel := context.WithTimeout(ctx, installTimeout)
	defer cancel()

	cmd := exec.CommandContext(ictx, bin, "-install")
	utils.HideConsole(cmd)
	if runtime.GOOS == "linux" {
		cmd.Env = append(os.Environ(), "TRUST_STORES=nss")
	}
	out, err := cmd.CombinedOutput()
	m.invalidate()
	if err != nil {
		return fmt.Errorf("mkcert -install: %w; output: %s", err, strings.TrimSpace(string(out)))
	}
	slog.Info("mkcert -install completed", "output", strings.TrimSpace(string(out)))
	return nil
}

// mkcertDownloadURL returns the canonical filippo.io redirect URL for the
// pinned mkcert version on the current platform. Unsupported combos return
// an error so the UI can show a precise message instead of a 404.
func mkcertDownloadURL() (string, error) {
	pair := runtime.GOOS + "/" + runtime.GOARCH
	switch pair {
	case "linux/amd64", "linux/arm64", "linux/arm",
		"darwin/amd64", "darwin/arm64",
		"windows/amd64", "windows/arm64",
		"freebsd/amd64", "freebsd/arm64", "freebsd/arm":
	default:
		return "", fmt.Errorf("no prebuilt mkcert binary for %s", pair)
	}
	return "https://dl.filippo.io/mkcert/" + MkcertVersion + "?for=" + pair, nil
}

func downloadBinary(ctx context.Context, url, dst string) error {
	dctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download mkcert: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download mkcert: HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".mkcert-dl-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { os.Remove(tmpPath) }

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close binary: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		cleanup()
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		cleanup()
		return fmt.Errorf("install binary: %w", err)
	}
	return nil
}
