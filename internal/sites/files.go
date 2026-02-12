package sites

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path"
	"path/filepath"

	"github.com/PeterBooker/locorum/internal/types"
)

var funcMap = template.FuncMap{
	"Domain": func(s types.Site) string {
		return s.Domain
	},
	"DocumentRoot": func(s types.Site) string {
		return filepath.Join("var", "www", s.Slug)
	},
	"Upstream": func(s types.Site) string {
		return s.Slug + "_upstream"
	},
	"NginxRoot": func(s *types.Site) string {
		return path.Clean("/var/www/html/" + s.PublicDir)
	},
}

var (
	siteTpl *template.Template
	mapTpl  *template.Template
)

// writeAtomic writes data to filename via a temp file + rename
func (sm *SiteManager) writeAtomic(filename string, data []byte) error {
	dir := filepath.Dir(filename)
	tmp, err := os.CreateTemp(dir, "tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	return os.Rename(tmp.Name(), filename)
}

// writeInPlace writes data to filename, truncating the file first
func (sm *SiteManager) writeInPlace(filename string, data []byte) error {
	f, err := os.OpenFile(filename,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return err
	}

	return f.Sync()
}

// generateSiteConfig generates the nginx config for a single site.
func (sm *SiteManager) generateSiteConfig(site *types.Site, dest string) error {
	var mbuf bytes.Buffer

	if err := siteTpl.Execute(&mbuf, site); err != nil {
		return fmt.Errorf("render site config: %w", err)
	}

	if err := sm.writeInPlace(dest, mbuf.Bytes()); err != nil {
		return fmt.Errorf("write site config: %w", err)
	}

	return nil
}

// generateMapConfig generates the nginx map config for all sites.
func (sm *SiteManager) generateMapConfig(sites []types.Site, dest string, testConfig bool) error {
	var mbuf bytes.Buffer

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(dest), err)
	}

	if err := mapTpl.Execute(&mbuf, sites); err != nil {
		return fmt.Errorf("render map config: %w", err)
	}

	if err := sm.writeInPlace(dest, mbuf.Bytes()); err != nil {
		return fmt.Errorf("write map config: %w", err)
	}

	if testConfig {
		if err := sm.d.TestGlobalNginxConfig(); err != nil {
			return fmt.Errorf("test nginx config: %w", err)
		}

		if err := sm.d.ReloadGlobalNginx(); err != nil {
			return fmt.Errorf("reload nginx config: %w", err)
		}
	}

	return nil
}
