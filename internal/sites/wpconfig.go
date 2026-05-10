package sites

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/PeterBooker/locorum/internal/genmark"
	"github.com/PeterBooker/locorum/internal/types"
)

// templateReadFS is the minimal capability EnsureWPConfig needs from a
// filesystem-shaped value. embed.FS satisfies it; tests inject a thin
// adapter that reads from disk so the actual checked-in templates can
// be exercised without an embed directive (which is package-relative
// and can't see ../../config/).
type templateReadFS interface {
	ReadFile(string) ([]byte, error)
}

// templateReader resolves the right FS for wp-config rendering. Test
// overrides win; production falls back to the embedded FS that
// NewSiteManager captured.
func (sm *SiteManager) templateReader() templateReadFS {
	if sm == nil {
		return nil
	}
	if sm.tplReader != nil {
		return sm.tplReader
	}
	return sm.config
}

// SetTemplateReader is a test seam (mirroring SetLANDetector). Pass
// nil to revert to the embedded FS. Not safe to call concurrently
// with EnsureWPConfig.
func (sm *SiteManager) SetTemplateReader(r templateReadFS) {
	if sm == nil {
		return
	}
	sm.tplReader = r
}

// wpSaltKeys are the eight WordPress secret-key/salt constant names, in
// the canonical order used in the bundled wp-config-sample.php.
var wpSaltKeys = []string{
	"AUTH_KEY",
	"SECURE_AUTH_KEY",
	"LOGGED_IN_KEY",
	"NONCE_KEY",
	"AUTH_SALT",
	"SECURE_AUTH_SALT",
	"LOGGED_IN_SALT",
	"NONCE_SALT",
}

// wpConfigTplFuncs are the funcMap shared by wp-config.php and
// wp-config-locorum.php. phpEscape is the only one we need today.
var wpConfigTplFuncs = template.FuncMap{
	"phpEscape": phpSingleQuoteEscape,
}

// phpSingleQuoteEscape escapes a Go string for safe inclusion inside a
// PHP single-quoted literal. PHP's single-quote rules are minimal: only
// backslash and single quote need escaping (no \n, \t, etc. interpolation).
// We deliberately do NOT pass through arbitrary unicode rewrites — the
// caller controls the input set.
func phpSingleQuoteEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `'`, `\'`)
	return r.Replace(s)
}

// wpConfigData is the template input shared by both files. Multisite is
// the canonical "" / "subdirectory" / "subdomain" string from types.Site.
//
// WPHome and WPSiteURL are precomputed in Go and baked directly into the
// rendered template. Earlier versions read the URL from a LOCORUM_PRIMARY_URL
// env var via getenv() at PHP request time; PHP-FPM's default `clear_env=yes`
// strips that env var before scripts run, so the fallback ("http://localhost")
// silently won and every site landed at https://localhost/wp-admin/ once
// is_ssl() flipped the scheme. The file is regenerated on every site start
// anyway, so baking the URL in costs nothing.
//
// PrimaryHost, LANHostRegex, and DocrootSuffix exist to support
// multi-domain access (ACCESS.md): the rendered wp-config-locorum.php
// dynamically maps `$_SERVER['HTTP_HOST']` onto WP_HOME/WP_SITEURL at
// request time, instead of pinning a single canonical URL. Without
// this, WordPress emits links pointing at site.Domain regardless of
// which alias (primary vs. LAN sslip.io hostname) the visitor reached.
// The whitelist is conservative — exact match for the primary, regex
// match for the LAN suffix — so attacker-controlled Host headers can
// never poison emails, redirects, or oEmbed URLs.
type wpConfigData struct {
	Salts         map[string]string
	DBPassword    string
	Domain        string
	Multisite     string
	WPHome        string
	WPSiteURL     string
	PrimaryHost   string
	LANHostRegex  string // PHP regex incl. delimiters, "" when LAN access disabled / unsupported
	DocrootSuffix string // "" or "/web" — appended to WP_HOME to form WP_SITEURL
}

// computeWPURLs derives WP_HOME and WP_SITEURL from the site's domain and
// PublicDir. Mirrors the historical env-var logic so existing sites see no
// behavioural change beyond "URLs are now correct under PHP-FPM".
//
// WP_SITEURL = WP_HOME + "/" + docroot, where docroot is the PublicDir
// stripped of leading slashes. For typical sites (PublicDir="/") docroot is
// empty and WP_SITEURL == WP_HOME.
func computeWPURLs(site *types.Site) (home, siteurl string) {
	home = "https://" + site.Domain
	siteurl = home + docrootSuffix(site)
	return home, siteurl
}

// docrootSuffix returns the trailing path component WP_SITEURL needs
// when the site uses a non-root document root (e.g. Bedrock's `web/`).
// Returns "" for the conventional case (PublicDir = "/" or "" or ".").
// The slash is included so callers can append unconditionally.
func docrootSuffix(site *types.Site) string {
	if site == nil {
		return ""
	}
	doc := strings.TrimLeft(site.PublicDir, "/")
	if doc == "" || doc == "." {
		return ""
	}
	return "/" + doc
}

// buildLANHostRegex returns the PHP regex (with `/.../` delimiters) that
// the rendered wp-config-locorum.php uses to whitelist incoming LAN
// hostnames. Pattern: `^<slug>\.\d{1,3}-\d{1,3}-\d{1,3}-\d{1,3}\.<domain>$`.
//
// Returns "" — explicit "no LAN regex" — when:
//   - LAN access is disabled for the site;
//   - the site is multisite-subdomain (DOMAIN_CURRENT_SITE pins the
//     canonical host; LAN can't be supported for subsites in v1);
//   - the slug or LAN domain is empty.
//
// The slug and domain are regex-escaped using Go's regexp.QuoteMeta;
// the resulting metacharacters are the same set PHP's PCRE engine
// honours, so a Go-quoted literal renders correctly inside the PHP
// preg_match call.
func buildLANHostRegex(site *types.Site, lanDomain string) string {
	if site == nil || !site.LanEnabled {
		return ""
	}
	if site.Multisite == "subdomain" {
		// LAN access for subdomain multisite would require rewriting
		// wp_blogs.domain for every subsite — out of scope for v1.
		// The UI surfaces this as a notice; this is the matching
		// defence-in-depth check.
		return ""
	}
	slug := strings.TrimSpace(site.Slug)
	domain := strings.TrimSpace(lanDomain)
	if slug == "" || domain == "" {
		return ""
	}
	return "/^" + regexp.QuoteMeta(slug) +
		"\\.\\d{1,3}-\\d{1,3}-\\d{1,3}-\\d{1,3}\\." +
		regexp.QuoteMeta(domain) + "$/"
}

// wpDocrootDir resolves the on-disk directory that should contain
// wp-config.php — the bind-mount root unless the site has an explicit
// PublicDir subdirectory (e.g. a Bedrock-style "web" docroot).
func wpDocrootDir(site *types.Site) string {
	if site.PublicDir == "" || site.PublicDir == "/" || site.PublicDir == "." {
		return site.FilesDir
	}
	return filepath.Join(site.FilesDir, site.PublicDir)
}

// EnsureWPConfig renders and writes wp-config.php and wp-config-locorum.php
// for the site. Idempotent — if the rendered bytes match the existing file
// no write happens.
//
// Safety contract:
//   - wp-config.php is written ONCE per site. If the file already exists
//     with the Locorum signature, we re-render in case the salts changed
//     (see ensureSalts) but skip the write when the bytes match.
//   - If wp-config.php exists WITHOUT the signature, we never overwrite —
//     the user has either imported their own file or stripped the
//     signature deliberately. We log a warning and continue.
//   - wp-config-locorum.php is rewritten on every call (idempotent on
//     content). It carries the signature so a user-stripped variant is
//     also respected.
//   - Writes are atomic: render to a tempfile in the same directory, then
//     os.Rename — no half-written file is ever readable.
func (sm *SiteManager) EnsureWPConfig(site *types.Site) error {
	if err := sm.ensureSalts(site); err != nil {
		return fmt.Errorf("ensure salts: %w", err)
	}

	salts, err := decodeSalts(site.Salts)
	if err != nil {
		return fmt.Errorf("decode salts: %w", err)
	}

	wpHome, wpSiteURL := computeWPURLs(site)
	lanDomain := ""
	if sm.cfg != nil {
		lanDomain = sm.cfg.LanDomain()
	}
	data := wpConfigData{
		Salts:         salts,
		DBPassword:    site.DBPassword,
		Domain:        site.Domain,
		Multisite:     site.Multisite,
		WPHome:        wpHome,
		WPSiteURL:     wpSiteURL,
		PrimaryHost:   strings.ToLower(site.Domain),
		LANHostRegex:  buildLANHostRegex(site, lanDomain),
		DocrootSuffix: docrootSuffix(site),
	}

	dir := wpDocrootDir(site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure docroot: %w", err)
	}

	// wp-config.php: respect a user-stripped signature, otherwise write.
	// 0o600: contains DB_PASSWORD and the eight WP auth/secure salts. The
	// PHP container runs as the host user's UID (PHPUserGroup) so owner-only
	// read is sufficient — and crucially, anyone else with a shell on the
	// host (other accounts, sandboxed apps, build agents) cannot recover
	// the salts and forge logged-in session cookies.
	mainPath := filepath.Join(dir, "wp-config.php")
	mainBytes, err := renderTemplate(sm.templateReader(), "config/wordpress/wp-config.tmpl.php", data)
	if err != nil {
		return fmt.Errorf("render wp-config.php: %w", err)
	}
	if err := genmark.WriteIfManaged(mainPath, mainBytes, 0o600); err != nil && !errors.Is(err, genmark.ErrUserOwned) {
		return fmt.Errorf("write wp-config.php: %w", err)
	}

	// wp-config-locorum.php: always managed; the signature is in the
	// rendered bytes so WriteIfManaged still respects a user-stripped
	// variant.  Preserves backward compatibility with sites whose users
	// already opted out before this rewrite.
	includePath := filepath.Join(dir, "wp-config-locorum.php")
	includeBytes, err := renderTemplate(sm.templateReader(), "config/wordpress/wp-config-locorum.tmpl.php", data)
	if err != nil {
		return fmt.Errorf("render wp-config-locorum.php: %w", err)
	}
	if err := genmark.WriteIfManaged(includePath, includeBytes, 0o600); err != nil && !errors.Is(err, genmark.ErrUserOwned) {
		return fmt.Errorf("write wp-config-locorum.php: %w", err)
	}

	return nil
}

// renderTemplate executes a single-file template against the embedded FS.
// Returns the rendered bytes; the caller decides whether to write them.
func renderTemplate(efs interface {
	ReadFile(string) ([]byte, error)
}, path string, data any) ([]byte, error) {
	src, err := efs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template: %w", err)
	}
	tpl, err := template.New(filepath.Base(path)).Funcs(wpConfigTplFuncs).Parse(string(src))
	if err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}
	return buf.Bytes(), nil
}

// ensureSalts populates site.Salts (and persists the row) if it's empty.
// Salts must be stable across regenerates — if they changed every start
// every existing user session would be invalidated and wp-config.php
// would never reach byte-equal idempotency.
func (sm *SiteManager) ensureSalts(site *types.Site) error {
	if site.Salts != "" {
		// Validate that what we have on disk parses; corrupt JSON
		// (older incomplete write, manual edit) means regenerate.
		if _, err := decodeSalts(site.Salts); err == nil {
			return nil
		}
		slog.Warn("site salts JSON is invalid, regenerating", "site", site.Slug)
	}

	salts, err := generateSalts()
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(salts)
	if err != nil {
		return fmt.Errorf("encode salts: %w", err)
	}
	site.Salts = string(encoded)
	if _, err := sm.st.UpdateSite(site); err != nil {
		return fmt.Errorf("persist salts: %w", err)
	}
	return nil
}

// decodeSalts unmarshals the JSON blob from Site.Salts and validates that
// every required key is present. Missing keys → error; the caller treats
// that as "regenerate".
func decodeSalts(encoded string) (map[string]string, error) {
	if encoded == "" {
		return nil, errors.New("salts not set")
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(encoded), &m); err != nil {
		return nil, err
	}
	for _, k := range wpSaltKeys {
		if v, ok := m[k]; !ok || v == "" {
			return nil, fmt.Errorf("missing salt %q", k)
		}
	}
	return m, nil
}

// saltAlphabet is a 64-character set safe inside a PHP single-quoted
// literal (no `'` or `\`). 64 entries keeps per-character entropy at
// exactly 6 bits and the alphabet table at a power of two.
const saltAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@"

// saltLength matches WordPress's own (64 characters per salt). 64 × 6 bits
// = 384 bits per salt, ~3 KB total entropy for the eight values.
const saltLength = 64

// generateSalts produces the eight salts using crypto/rand. We do not call
// out to wordpress.org/secret-key — the network dependency would make
// site-creation fail-on-first-launch in offline scenarios, and our local
// generator has equivalent entropy.
func generateSalts() (map[string]string, error) {
	out := make(map[string]string, len(wpSaltKeys))
	alphaLen := big.NewInt(int64(len(saltAlphabet)))
	for _, key := range wpSaltKeys {
		buf := make([]byte, saltLength)
		for i := range buf {
			n, err := rand.Int(rand.Reader, alphaLen)
			if err != nil {
				return nil, fmt.Errorf("random: %w", err)
			}
			buf[i] = saltAlphabet[n.Int64()]
		}
		out[key] = string(buf)
	}
	return out, nil
}
