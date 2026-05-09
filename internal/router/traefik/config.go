package traefik

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/PeterBooker/locorum/internal/router"
	tlspkg "github.com/PeterBooker/locorum/internal/tls"
)

// Renderer parses the YAML templates once and renders Traefik static and
// dynamic configs from typed data. Safe for concurrent use after construction.
type Renderer struct {
	staticTpl  *template.Template
	siteTpl    *template.Template
	serviceTpl *template.Template
	apiTpl     *template.Template
}

// NewRenderer parses the templates from the embedded config FS. Returns an
// error if any template fails to parse — refuses to start with a
// half-loaded config.
func NewRenderer(filesystem fs.FS) (*Renderer, error) {
	static, err := template.New("static.yaml.tmpl").ParseFS(filesystem, "config/router/static.yaml.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse static template: %w", err)
	}
	site, err := template.New("site.yaml.tmpl").ParseFS(filesystem, "config/router/site.yaml.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse site template: %w", err)
	}
	service, err := template.New("service.yaml.tmpl").ParseFS(filesystem, "config/router/service.yaml.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse service template: %w", err)
	}
	api, err := template.New("api.yaml.tmpl").ParseFS(filesystem, "config/router/api.yaml.tmpl")
	if err != nil {
		return nil, fmt.Errorf("parse api template: %w", err)
	}
	return &Renderer{staticTpl: static, siteTpl: site, serviceTpl: service, apiTpl: api}, nil
}

// API renders the dynamic config that exposes Traefik's admin API on the
// internal entrypoint behind a basic-auth middleware. hash must be a
// bcrypt/htpasswd-compatible password hash for username.
func (r *Renderer) API(username, hash string) ([]byte, error) {
	if username == "" || hash == "" {
		return nil, errors.New("api auth credentials required")
	}
	var buf bytes.Buffer
	if err := r.apiTpl.Execute(&buf, struct {
		Username string
		Hash     string
	}{Username: username, Hash: hash}); err != nil {
		return nil, fmt.Errorf("render api config: %w", err)
	}
	return buf.Bytes(), nil
}

// Static renders the Traefik static config (entrypoints, file provider,
// API). logLevel defaults to "INFO" if blank.
func (r *Renderer) Static(logLevel string) ([]byte, error) {
	if logLevel == "" {
		logLevel = "INFO"
	}
	var buf bytes.Buffer
	if err := r.staticTpl.Execute(&buf, struct{ LogLevel string }{LogLevel: logLevel}); err != nil {
		return nil, fmt.Errorf("render static config: %w", err)
	}
	return buf.Bytes(), nil
}

// Site renders the dynamic config for a single site. cert paths must already
// be translated to container paths (see Router.containerCertPath). If cert
// is zero, the site is rendered without TLS — useful for HTTP-only fallback
// when mkcert is unavailable.
func (r *Renderer) Site(route router.SiteRoute, cert tlspkg.CertPath) ([]byte, error) {
	data := struct {
		Slug     string
		Rule     string
		Backend  string
		CertFile string
		KeyFile  string
	}{
		Slug:     route.Slug,
		Rule:     BuildSiteRule(route),
		Backend:  route.Backend,
		CertFile: cert.CertFile,
		KeyFile:  cert.KeyFile,
	}
	var buf bytes.Buffer
	if err := r.siteTpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render site config: %w", err)
	}
	return buf.Bytes(), nil
}

// Service renders the dynamic config for a global service (mail, adminer).
func (r *Renderer) Service(route router.ServiceRoute, cert tlspkg.CertPath) ([]byte, error) {
	if len(route.Hostnames) == 0 {
		return nil, fmt.Errorf("service %q: at least one hostname required", route.Name)
	}
	data := struct {
		Name     string
		Rule     string
		Backend  string
		CertFile string
		KeyFile  string
	}{
		Name:     route.Name,
		Rule:     BuildServiceRule(route),
		Backend:  route.Backend,
		CertFile: cert.CertFile,
		KeyFile:  cert.KeyFile,
	}
	var buf bytes.Buffer
	if err := r.serviceTpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render service config: %w", err)
	}
	return buf.Bytes(), nil
}

// BuildSiteRule constructs the Traefik v3 rule string for a SiteRoute.
//
// Output examples:
//   - PrimaryHost only:   Host(`myslug.localhost`)
//   - With extras:        Host(`myslug.localhost`) || Host(`alias.localhost`)
//   - With wildcard:      Host(`myslug.localhost`) || HostRegexp(`^[^.]+\.myslug\.localhost$`)
//
// Wildcard SANs match exactly one DNS label, mirroring TLS verification
// rules (so `a.myslug.localhost` matches but `a.b.myslug.localhost` does not).
func BuildSiteRule(route router.SiteRoute) string {
	parts := []string{fmt.Sprintf("Host(`%s`)", route.PrimaryHost)}
	for _, h := range route.ExtraHosts {
		parts = append(parts, fmt.Sprintf("Host(`%s`)", h))
	}
	if route.WildcardHost != "" {
		suffix := strings.TrimPrefix(route.WildcardHost, "*.")
		pattern := "^[^.]+\\." + regexp.QuoteMeta(suffix) + "$"
		parts = append(parts, fmt.Sprintf("HostRegexp(`%s`)", pattern))
	}
	return strings.Join(parts, " || ")
}

// BuildServiceRule constructs the rule for a global service.
func BuildServiceRule(route router.ServiceRoute) string {
	parts := make([]string, len(route.Hostnames))
	for i, h := range route.Hostnames {
		parts[i] = fmt.Sprintf("Host(`%s`)", h)
	}
	return strings.Join(parts, " || ")
}

// containerCertPath translates a host-side cert path to its container-side
// counterpart given the bind-mount layout. Paths outside hostRoot are passed
// through unchanged (defensive — should never happen in practice).
func containerCertPath(hostPath, hostRoot, containerRoot string) string {
	if hostPath == "" {
		return ""
	}
	rel, err := filepath.Rel(hostRoot, hostPath)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, "..") {
		return hostPath
	}
	return path.Join(containerRoot, filepath.ToSlash(rel))
}
