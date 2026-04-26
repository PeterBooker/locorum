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
	"ApacheRoot": func(s *types.Site) string {
		return path.Clean("/var/www/html/" + s.PublicDir)
	},
}

var (
	siteTpl       *template.Template
	apacheSiteTpl *template.Template
)

// writeInPlace writes data to filename, truncating the file first.
func (sm *SiteManager) writeInPlace(filename string, data []byte) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

func (sm *SiteManager) generateSiteConfig(site *types.Site, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(dest), err)
	}
	var mbuf bytes.Buffer
	if err := siteTpl.Execute(&mbuf, site); err != nil {
		return fmt.Errorf("render site config: %w", err)
	}
	if err := sm.writeInPlace(dest, mbuf.Bytes()); err != nil {
		return fmt.Errorf("write site config: %w", err)
	}
	return nil
}

func (sm *SiteManager) generateApacheSiteConfig(site *types.Site, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("mkdir %q: %w", filepath.Dir(dest), err)
	}
	var mbuf bytes.Buffer
	if err := apacheSiteTpl.Execute(&mbuf, site); err != nil {
		return fmt.Errorf("render apache site config: %w", err)
	}
	if err := sm.writeInPlace(dest, mbuf.Bytes()); err != nil {
		return fmt.Errorf("write apache site config: %w", err)
	}
	return nil
}
