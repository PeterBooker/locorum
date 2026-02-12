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
func (sm *SiteManager) ensureWordPress(site *types.Site) error {
	targetDir := site.FilesDir
	if site.PublicDir != "" && site.PublicDir != "/" {
		targetDir = filepath.Join(site.FilesDir, site.PublicDir)
	}

	if err := utils.EnsureDir(targetDir); err != nil {
		return fmt.Errorf("ensure target dir: %w", err)
	}

	empty, err := isDirEmpty(targetDir)
	if err != nil {
		return fmt.Errorf("checking target dir: %w", err)
	}
	if !empty {
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
	defer gz.Close()

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

		target := filepath.Join(destDir, name)

		// Guard against path traversal.
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("mkdir %q: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("mkdir parent %q: %w", target, err)
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return fmt.Errorf("create %q: %w", target, err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("write %q: %w", target, err)
			}
			f.Close()
		}
	}

	return nil
}
