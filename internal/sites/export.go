package sites

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type exportMeta struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Domain       string `json:"domain"`
	PHPVersion   string `json:"phpVersion"`
	MySQLVersion string `json:"mysqlVersion"`
	RedisVersion string `json:"redisVersion"`
	PublicDir    string `json:"publicDir"`
	ExportedAt   string `json:"exportedAt"`
}

// ExportSite creates a .tar.gz archive containing the site's database dump,
// files directory, and metadata.
func (sm *SiteManager) ExportSite(id, destPath string) error {
	site, err := sm.st.GetSite(id)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", id)
	}

	// Dump the database via mysqldump in the database container.
	containerName := "locorum-" + site.Slug + "-database"
	sqlDump, err := sm.d.ExecInContainer(containerName, []string{
		"mysqldump", "-u", "wordpress", "-p" + site.DBPassword, "wordpress",
	})
	if err != nil {
		return fmt.Errorf("database dump: %w", err)
	}

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
	return nil
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

