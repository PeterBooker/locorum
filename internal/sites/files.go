package sites

import (
	"bytes"
	"fmt"
	"html/template"
	"path"
	"path/filepath"

	"github.com/PeterBooker/locorum/internal/genmark"
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

// renderSiteConfig executes tpl against site and prepends the canonical
// genmark header. Returns rendered bytes; caller writes them.
//
// The header uses StyleHash because both nginx and Apache treat `#`
// lines as comments.  Centralised so both web-server paths share one
// "generated header + body" assembly and so the prepend stays in one
// place if the marker format ever changes.
func renderSiteConfig(tpl *template.Template, site *types.Site) ([]byte, error) {
	var body bytes.Buffer
	if err := tpl.Execute(&body, site); err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(genmark.Header(genmark.StyleHash))+body.Len())
	out = append(out, genmark.Header(genmark.StyleHash)...)
	out = append(out, body.Bytes()...)
	return out, nil
}

// generateSiteConfig renders the per-site nginx config and writes it
// via WriteAtomic — the file is bind-mounted into the global router and
// has no documented user-edit surface, so we always own it.
func (sm *SiteManager) generateSiteConfig(site *types.Site, dest string) error {
	payload, err := renderSiteConfig(siteTpl, site)
	if err != nil {
		return fmt.Errorf("render site config: %w", err)
	}
	if err := genmark.WriteAtomic(dest, payload, 0o644); err != nil {
		return fmt.Errorf("write site config: %w", err)
	}
	return nil
}

// generateApacheSiteConfig is the Apache twin of generateSiteConfig.
func (sm *SiteManager) generateApacheSiteConfig(site *types.Site, dest string) error {
	payload, err := renderSiteConfig(apacheSiteTpl, site)
	if err != nil {
		return fmt.Errorf("render apache site config: %w", err)
	}
	if err := genmark.WriteAtomic(dest, payload, 0o644); err != nil {
		return fmt.Errorf("write apache site config: %w", err)
	}
	return nil
}
