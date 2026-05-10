// Package wpcli ensures the pinned wp-cli phar (see internal/version
// for the version + SHA-512 pin) is present at ~/.locorum/bin/wp so
// every PHP container can bind-mount it as /usr/local/bin/wp. The
// wodby/php image we use does not bundle wp-cli; every WordPress-
// touching lifecycle step (StartSite's `wp core install`, DB-import
// search-replace, multisite convert) depends on this binary being
// available inside the container.
//
// Why a host-side phar bind-mount and not a custom Docker image:
//
//   - One asset works across every supported PHP version. We don't
//     have to rebuild and republish a Locorum-curated image on each
//     wp-cli release, and switching to a baked-in image later is just
//     dropping the bind mount.
//   - Content-addressed: the phar is verified against the SHA-512
//     pinned in internal/version on every download. A tampered or
//     corrupted file is rejected before it overwrites the existing
//     phar — the on-disk binary is always either pristine or absent.
//   - Read-only mount: containers cannot mutate the phar.
package wpcli

import (
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PeterBooker/locorum/internal/version"
)

// pharRelativePath is the location, relative to homeDir, where the
// verified phar is persisted. Lives under ~/.locorum/bin/ so it sits
// next to other Locorum-managed binaries (mkcert) and inherits the
// 0700 permission Locorum applies to ~/.locorum.
var pharRelativePath = filepath.Join(".locorum", "bin", "wp")

// downloadTimeout caps the GET. The phar is ~7 MiB; a hung connection
// would otherwise stall app startup indefinitely.
const downloadTimeout = 2 * time.Minute

// downloadSizeLimit caps the body we'll buffer in memory. WP-CLI 2.12
// is ~7 MiB; doubled (15 MiB) gives generous headroom for a few
// future releases without ever letting a hostile mirror exhaust RAM.
const downloadSizeLimit = 15 << 20

// httpClient is shared so connection reuse + the timeout are
// consistent. The stdlib default has no timeout.
var httpClient = &http.Client{Timeout: downloadTimeout}

// PharPath returns the absolute path to the on-disk phar for homeDir.
// Pure function — used by callers (PHPSpec) that need the path even
// when EnsurePhar hasn't been called yet (e.g. tests, hash assembly).
func PharPath(homeDir string) string {
	return filepath.Join(homeDir, pharRelativePath)
}

// EnsurePhar guarantees the pinned wp-cli phar is present at
// PharPath(homeDir) with the version-package SHA-512.
//
// Sequence:
//
//  1. If the phar already exists and its SHA-512 matches the pin,
//     return immediately. The common case — every app start after the
//     first one — does no network IO.
//  2. Otherwise download from version.WPCliDownloadURL(), buffer in
//     memory, hash, and refuse to write if the hash mismatches.
//  3. Atomic write: temp file in the same directory, fsync, rename.
//     The visible path is never a half-written phar.
//
// Errors are wrapped with the precise step that failed so a
// connectivity issue, a bad upstream, and a permissions problem are
// distinguishable in logs.
func EnsurePhar(homeDir string) (string, error) {
	if homeDir == "" {
		return "", errors.New("wpcli: empty homeDir")
	}
	target := PharPath(homeDir)

	if ok, err := matchesPin(target); err != nil {
		return "", fmt.Errorf("wpcli: verifying existing phar: %w", err)
	} else if ok {
		return target, nil
	}

	body, err := download(version.WPCliDownloadURL())
	if err != nil {
		return "", fmt.Errorf("wpcli: download: %w", err)
	}
	if got := hexSHA512(body); !strings.EqualFold(got, version.WPCliSHA512) {
		return "", fmt.Errorf(
			"wpcli: integrity check failed: sha512 mismatch (expected %s, got %s) — refusing to install",
			version.WPCliSHA512, got,
		)
	}

	if err := writeAtomic(target, body, 0o755); err != nil {
		return "", fmt.Errorf("wpcli: write: %w", err)
	}
	return target, nil
}

// matchesPin returns true iff a file exists at path and its SHA-512
// equals version.WPCliSHA512. A missing file is not an error — it's
// the expected first-run state — so it returns (false, nil).
func matchesPin(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()

	h := sha512.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	return strings.EqualFold(hex.EncodeToString(h.Sum(nil)), version.WPCliSHA512), nil
}

// download fetches url, capped at downloadSizeLimit + 1 byte (so an
// over-cap response is detectable). The body is fully buffered so
// hash verification can complete before any bytes touch the
// filesystem — see EnsurePhar's threat-model commentary.
func download(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, downloadSizeLimit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > downloadSizeLimit {
		return nil, fmt.Errorf("phar exceeds %d bytes — refusing", downloadSizeLimit)
	}
	return body, nil
}

// hexSHA512 returns the lowercase-hex SHA-512 of body.
func hexSHA512(body []byte) string {
	sum := sha512.Sum512(body)
	return hex.EncodeToString(sum[:])
}

// writeAtomic writes data to path via a temp file in the same
// directory, fsyncs, then renames over the target. Same-directory
// guarantees the rename is atomic on every supported filesystem;
// fsync guarantees the bytes survive a crash before rename. Mode
// 0o755 because the file is executed (#!/usr/bin/env php shebang)
// and the bind-mount must keep the executable bit.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".wp-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// On any failure after CreateTemp, remove the dangling file so a
	// retry isn't blocked by leftover state.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	committed = true
	return nil
}
