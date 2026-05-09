package sites

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/utils"
)

const wordpressDownloadURL = "https://wordpress.org/latest.tar.gz"

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

	empty, err := isDirEmpty(targetDir)
	if err != nil {
		return fmt.Errorf("checking target dir: %w", err)
	}
	if !empty {
		// Directory has *something* but no wp-settings.php. Don't
		// clobber the user's files; they may be in the middle of
		// setting up Bedrock or restoring a partial backup.
		return nil
	}

	slog.Info(fmt.Sprintf("Downloading WordPress to %s", targetDir))

	resp, err := http.Get(wordpressDownloadURL)
	if err != nil {
		return fmt.Errorf("downloading WordPress: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading WordPress: HTTP %d", resp.StatusCode)
	}

	if err := extractTarGz(resp.Body, targetDir); err != nil {
		return fmt.Errorf("extracting WordPress: %w", err)
	}

	// Ensure the target directory itself is writable by container processes.
	_ = os.Chmod(targetDir, 0o777)

	slog.Info("WordPress installed successfully")
	return nil
}

// isDirEmpty returns true if the directory exists and contains no entries.
func isDirEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// extractTarGz extracts a .tar.gz stream into destDir.
// The WordPress tarball has a top-level "wordpress/" directory —
// its contents are extracted directly into destDir.
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

		target := filepath.Join(destDir, name) //nolint:gosec // G305: traversal guarded by the Clean+HasPrefix check below

		// Guard against path traversal.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o777); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
			// Force permissions — MkdirAll is subject to umask.
			if err := os.Chmod(target, 0o777); err != nil {
				return fmt.Errorf("chmod dir %q: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
				return fmt.Errorf("mkdir parent %q: %w", target, err)
			}
			_ = os.Chmod(filepath.Dir(target), 0o777)
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o666)
			if err != nil {
				return fmt.Errorf("create %q: %w", target, err)
			}
			// Cap per-file decompression at 256 MiB. WordPress core
			// tarballs are ~30 MiB; a hostile gzip claiming to be
			// WordPress shouldn't get to fill the disk.
			const maxFileBytes = 256 << 20
			if _, err := io.Copy(f, io.LimitReader(tr, maxFileBytes)); err != nil {
				f.Close()
				return fmt.Errorf("write %q: %w", target, err)
			}
			f.Close()
		}
	}

	return nil
}

// ensureWritable makes all directories under root world-writable so that
// container processes (which may run as a different UID) can create files.
func ensureWritable(root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(path, 0o777)
		}
		return nil
	})
}
