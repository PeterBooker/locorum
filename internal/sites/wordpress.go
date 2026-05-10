package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1" //nolint:gosec // wordpress.org publishes SHA-1 sidecar; integrity-only check, not security-strength.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

const (
	wordpressDownloadURL    = "https://wordpress.org/latest.tar.gz"
	wordpressSHA1URL        = "https://wordpress.org/latest.tar.gz.sha1"
	wordpressDownloadMax    = 100 << 20 // 100 MiB cap on the tarball download.
	wordpressDownloadTO     = 5 * time.Minute
	wordpressMaxArchiveSize = 256 << 20 // 256 MiB cap on per-file decompressed size.
)

// httpClientWordPress is a dedicated HTTP client with a sane timeout. The
// stdlib default has none, so a hung connection would freeze the start-site
// step indefinitely.
var httpClientWordPress = &http.Client{Timeout: wordpressDownloadTO}

// ensureWordPress checks if the site's public directory contains WordPress.
// If the directory is empty, it downloads and extracts WordPress into it.
//
// Ambiguity rule (LEARNINGS.md §3.3): if wp-settings.php is found in more
// than one location under the docroot, abort with an explicit error rather
// than guessing which install to use. The user must remove the duplicate
// or set the site's PublicDir to disambiguate.
func (sm *SiteManager) ensureWordPress(site *types.Site) error {
	targetDir := site.FilesDir
	if site.PublicDir != "" && site.PublicDir != "/" {
		targetDir = filepath.Join(site.FilesDir, site.PublicDir)
	}

	if err := utils.EnsureDir(targetDir); err != nil {
		return fmt.Errorf("ensure target dir: %w", err)
	}

	matches, err := detectWordPress(site.FilesDir, site.PublicDir)
	if err != nil {
		return err
	}
	if len(matches) > 0 {
		// WordPress already present (at the docroot or one level
		// deeper). Respect what the user has and skip the download.
		return nil
	}

	empty, err := isEmptyForWordPress(targetDir)
	if err != nil {
		return fmt.Errorf("checking target dir: %w", err)
	}
	if !empty {
		// Directory has *user* content but no wp-settings.php. Don't
		// clobber it; they may be in the middle of setting up Bedrock
		// or restoring a partial backup. Locorum's own sentinel files
		// (.locorum/) are skipped by isEmptyForWordPress — AddSite
		// projects config.yaml there before StartSite runs, and a
		// freshly-created site's docroot is empty *modulo* that
		// sentinel.
		return nil
	}

	slog.Info(fmt.Sprintf("Downloading WordPress to %s", targetDir))

	// Download the SHA-1 sidecar first. wordpress.org publishes it next
	// to latest.tar.gz precisely so consumers can verify the body. A
	// missing sidecar is fatal — refusing to install code we can't
	// verify is the whole point.
	expectedSHA1, err := fetchWordPressSHA1()
	if err != nil {
		return fmt.Errorf("fetching WordPress sha1: %w", err)
	}

	resp, err := httpClientWordPress.Get(wordpressDownloadURL)
	if err != nil {
		return fmt.Errorf("downloading WordPress: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading WordPress: HTTP %d", resp.StatusCode)
	}

	// Tee through SHA-1 while buffering the body. WordPress is ~30 MiB;
	// holding it in RAM is acceptable and lets us verify before any byte
	// touches disk.
	var buf bytes.Buffer
	hasher := sha1.New() //nolint:gosec // sidecar publishes SHA-1; integrity-only check.
	if _, err := io.Copy(io.MultiWriter(&buf, hasher), io.LimitReader(resp.Body, wordpressDownloadMax+1)); err != nil {
		return fmt.Errorf("reading WordPress tarball: %w", err)
	}
	if int64(buf.Len()) > wordpressDownloadMax {
		return fmt.Errorf("WordPress tarball exceeds %d bytes — refusing to extract", wordpressDownloadMax)
	}
	gotSHA1 := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(gotSHA1, expectedSHA1) {
		return fmt.Errorf("WordPress integrity check failed: sha1 mismatch (expected %s, got %s)",
			expectedSHA1, gotSHA1)
	}

	if err := extractTarGz(&buf, targetDir); err != nil {
		return fmt.Errorf("extracting WordPress: %w", err)
	}

	slog.Info("WordPress installed and verified", "sha1", gotSHA1)
	return nil
}

// fetchWordPressSHA1 retrieves the published .sha1 sidecar (a 40-char hex
// digest, optionally followed by whitespace + filename).
func fetchWordPressSHA1() (string, error) {
	resp, err := httpClientWordPress.Get(wordpressSHA1URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	if err != nil {
		return "", err
	}
	digest := strings.TrimSpace(string(body))
	// Sidecars sometimes include the filename: "abc123…  latest.tar.gz".
	if i := strings.IndexAny(digest, " \t"); i > 0 {
		digest = digest[:i]
	}
	if len(digest) != 40 {
		return "", fmt.Errorf("malformed sha1 sidecar (len=%d)", len(digest))
	}
	for _, c := range digest {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return "", errors.New("malformed sha1 sidecar (non-hex byte)")
		}
	}
	return digest, nil
}

// isEmptyForWordPress returns true if the directory contains nothing the
// WordPress download would clobber. Entries managed by Locorum itself
// (currently the `.locorum/` sentinel directory, where AddSite projects
// config.yaml) are not user data and must not gate the download — without
// this exemption, a freshly-created site whose docroot already holds
// `.locorum/config.yaml` would skip the WP install entirely and serve
// 403/404.
func isEmptyForWordPress(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() == ".locorum" {
			continue
		}
		return false, nil
	}
	return true, nil
}

// extractTarGz extracts a .tar.gz stream into destDir.
//
// Hardening:
//   - Top-level "wordpress/" prefix is stripped (cosmetic).
//   - Path-traversal: the cleaned target must remain within destDir.
//   - Only TypeReg / TypeRegA (regular files) and TypeDir are accepted —
//     symlinks, hardlinks, devices, fifos are rejected with an explicit
//     error. A malicious tarball cannot point a regular file at an
//     attacker-controlled path through a symlink in a parent dir because
//     OpenFile is invoked with O_NOFOLLOW where the platform supports it.
//   - 0o755 dirs / 0o644 files. The PHP container runs as the host UID
//     (PHPUserGroup), so owner read+write is enough. World-readable bits
//     keep the WP `nginx` worker happy without exposing writes — notably
//     blocking the "drop a webshell into the docroot" path that 0o777
//     enabled.
//   - Per-file decompression cap (wordpressMaxArchiveSize) caps a single
//     entry at 256 MiB to defeat decompression bombs.
func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		// Strip the top-level "wordpress/" prefix.
		name := hdr.Name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		if name == "" {
			continue
		}

		target := filepath.Join(destDir, name) //nolint:gosec // G305: traversal guarded by the Clean+HasPrefix check below.

		// Guard against path traversal.
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator),
			filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry %q escapes destination", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent %q: %w", target, err)
			}
			if err := writeRegularEntry(tr, target); err != nil {
				return err
			}
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			// WordPress core ships only regular files and directories.
			// Anything else in the tarball is either a corruption or a
			// supply-chain attack; refuse it loudly so the failure is
			// visible rather than silently dropped.
			return fmt.Errorf("tar entry %q has unsupported type %c — refusing to extract",
				hdr.Name, hdr.Typeflag)
		default:
			return fmt.Errorf("tar entry %q has unknown type %c", hdr.Name, hdr.Typeflag)
		}
	}

	return nil
}

// writeRegularEntry writes a tar regular-file body to target with mode
// 0o644. O_EXCL refuses to follow a pre-existing symlink at the path; on
// platforms supporting it we additionally pass O_NOFOLLOW so a malicious
// dangling symlink cannot redirect the write outside destDir.
func writeRegularEntry(tr *tar.Reader, target string) error {
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC | os.O_EXCL | extraOpenFlags()
	f, err := os.OpenFile(target, flags, 0o644)
	if err != nil {
		// O_EXCL fires if the file already exists; the freshly-created
		// empty docroot dir guarantees that's a tar duplicate (which
		// WordPress core never has) — fall back to a truncating open
		// only if the existing entry is itself a regular file we
		// already wrote during this run.
		if errors.Is(err, os.ErrExist) {
			info, statErr := os.Lstat(target)
			if statErr == nil && info.Mode().IsRegular() {
				flags = os.O_WRONLY | os.O_TRUNC | extraOpenFlags()
				f, err = os.OpenFile(target, flags, 0o644)
			}
		}
		if err != nil {
			return fmt.Errorf("create %q: %w", target, err)
		}
	}
	defer f.Close()

	// Cap per-file decompression at 256 MiB. WordPress core tarballs
	// are ~30 MiB; a hostile gzip claiming to be WordPress shouldn't
	// get to fill the disk.
	if _, err := io.Copy(f, io.LimitReader(tr, wordpressMaxArchiveSize)); err != nil {
		return fmt.Errorf("write %q: %w", target, err)
	}
	return nil
}
