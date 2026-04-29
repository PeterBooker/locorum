package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/hooks"
)

type exportMeta struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Domain       string `json:"domain"`
	PHPVersion   string `json:"phpVersion"`
	DBEngine     string `json:"dbEngine"`
	DBVersion    string `json:"dbVersion"`
	MySQLVersion string `json:"mysqlVersion,omitempty"` // legacy mirror
	RedisVersion string `json:"redisVersion"`
	PublicDir    string `json:"publicDir"`
	ExportedAt   string `json:"exportedAt"`
}

// ExportSite creates a .tar.gz archive containing the site's database dump,
// files directory, and metadata.
func (sm *SiteManager) ExportSite(ctx context.Context, id, destPath string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", id)
	}

	mu := sm.siteMutex(id)
	mu.Lock()
	defer mu.Unlock()

	if err := sm.runHooks(ctx, hooks.PreExport, site); err != nil {
		return err
	}

	// Dump the database via the engine's Snapshot method. Streams direct
	// from the container into an in-memory buffer; the export tar stays
	// the final destination so the bytes never touch a plaintext SQL
	// file on disk.
	eng := dbengine.Resolve(site)
	var dumpBuf bytes.Buffer
	if _, err := eng.Snapshot(ctx, sm.d, site, &dumpBuf); err != nil {
		return fmt.Errorf("database dump: %w", err)
	}
	sqlDump := dumpBuf.String()

	// Create the tar.gz file.
	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("creating export file: %w", err)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	// Write metadata.json
	meta := exportMeta{
		Name:         site.Name,
		Slug:         site.Slug,
		Domain:       site.Domain,
		PHPVersion:   site.PHPVersion,
		DBEngine:     site.DBEngine,
		DBVersion:    site.DBVersion,
		MySQLVersion: site.MySQLVersion,
		RedisVersion: site.RedisVersion,
		PublicDir:    site.PublicDir,
		ExportedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	if err := addToTar(tw, "metadata.json", metaJSON); err != nil {
		return fmt.Errorf("writing metadata: %w", err)
	}

	// Write database.sql
	if err := addToTar(tw, "database.sql", []byte(sqlDump)); err != nil {
		return fmt.Errorf("writing database dump: %w", err)
	}

	// Walk the site files directory and add to tar under "files/".
	filesDir := site.FilesDir
	err = filepath.WalkDir(filesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(filesDir, path)
		if err != nil {
			return err
		}

		tarPath := "files/" + rel

		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:     tarPath + "/",
				Typeflag: tar.TypeDir,
				Mode:     0755,
			})
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		hdr := &tar.Header{
			Name:    tarPath,
			Size:    info.Size(),
			Mode:    int64(info.Mode()),
			ModTime: info.ModTime(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("archiving files: %w", err)
	}

	slog.Info(fmt.Sprintf("Site %q exported to %s", site.Name, destPath))
	return sm.runHooks(ctx, hooks.PostExport, site)
}

// addToTar writes a single file entry to the tar writer.
func addToTar(tw *tar.Writer, name string, data []byte) error {
	hdr := &tar.Header{
		Name: name,
		Size: int64(len(data)),
		Mode: 0644,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
