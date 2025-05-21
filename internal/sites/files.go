package sites

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
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
}

var (
	siteTpl *template.Template
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

func (sm *SiteManager) regenerateSiteConfig(site types.Site, dest string) error {
	var mbuf bytes.Buffer

	if err := siteTpl.Execute(&mbuf, site); err != nil {
		return fmt.Errorf("render site config: %w", err)
	}

	if err := sm.writeAtomic(dest, mbuf.Bytes()); err != nil {
		return fmt.Errorf("write site config: %w", err)
	}

	if err := sm.d.TestGlobalNginxConfig(); err != nil {
		return fmt.Errorf("test nginx config: %w", err)
	}

	if err := sm.d.ReloadGlobalNginx(); err != nil {
		return fmt.Errorf("reload nginx config: %w", err)
	}

	return nil
}
