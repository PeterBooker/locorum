package sites

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/PeterBooker/locorum/internal/types"
)

func TestComputeWPURLs(t *testing.T) {
	cases := []struct {
		domain, publicDir string
		wantHome          string
		wantSiteURL       string
	}{
		{"peter.localhost", "/", "https://peter.localhost", "https://peter.localhost"},
		{"peter.localhost", "", "https://peter.localhost", "https://peter.localhost"},
		{"peter.localhost", ".", "https://peter.localhost", "https://peter.localhost"},
		{"site.localhost", "/web", "https://site.localhost", "https://site.localhost/web"},
		{"site.localhost", "web", "https://site.localhost", "https://site.localhost/web"},
	}
	for _, c := range cases {
		s := &types.Site{Domain: c.domain, PublicDir: c.publicDir}
		gotHome, gotSiteURL := computeWPURLs(s)
		if gotHome != c.wantHome {
			t.Errorf("computeWPURLs(%q,%q) home = %q, want %q", c.domain, c.publicDir, gotHome, c.wantHome)
		}
		if gotSiteURL != c.wantSiteURL {
			t.Errorf("computeWPURLs(%q,%q) siteurl = %q, want %q", c.domain, c.publicDir, gotSiteURL, c.wantSiteURL)
		}
	}
}

func TestPhpSingleQuoteEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{``, ``},
		{`hello`, `hello`},
		{`it's`, `it\'s`},
		{`back\slash`, `back\\slash`},
		{`mix'\of`, `mix\'\\of`},
		{`'\'`, `\'\\\'`},
	}
	for _, c := range cases {
		if got := phpSingleQuoteEscape(c.in); got != c.want {
			t.Errorf("phpSingleQuoteEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGenerateSaltsContainsAllKeys(t *testing.T) {
	salts, err := generateSalts()
	if err != nil {
		t.Fatalf("generateSalts: %v", err)
	}
	if len(salts) != len(wpSaltKeys) {
		t.Fatalf("got %d salts, want %d", len(salts), len(wpSaltKeys))
	}
	for _, k := range wpSaltKeys {
		v, ok := salts[k]
		if !ok {
			t.Errorf("missing salt %q", k)
			continue
		}
		if len(v) != saltLength {
			t.Errorf("salt %q has length %d, want %d", k, len(v), saltLength)
		}
		if strings.ContainsAny(v, `'\`) {
			t.Errorf("salt %q contains PHP-unsafe characters: %q", k, v)
		}
		for _, ch := range v {
			if !strings.ContainsRune(saltAlphabet, ch) {
				t.Errorf("salt %q contains out-of-alphabet rune %q", k, ch)
			}
		}
	}
}

func TestGenerateSaltsAreUnique(t *testing.T) {
	a, err := generateSalts()
	if err != nil {
		t.Fatal(err)
	}
	b, err := generateSalts()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range wpSaltKeys {
		if a[k] == b[k] {
			t.Errorf("salt %q identical across two runs (RNG broken?)", k)
		}
	}
}

func TestDecodeSalts(t *testing.T) {
	good, _ := generateSalts()
	encoded, _ := json.Marshal(good)
	decoded, err := decodeSalts(string(encoded))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range wpSaltKeys {
		if decoded[k] != good[k] {
			t.Errorf("round-trip mismatch %q", k)
		}
	}

	if _, err := decodeSalts(""); err == nil {
		t.Error("empty string should error")
	}
	if _, err := decodeSalts(`{"AUTH_KEY":"x"}`); err == nil {
		t.Error("incomplete map should error")
	}
	if _, err := decodeSalts(`not json`); err == nil {
		t.Error("invalid JSON should error")
	}
}

func TestRenderTemplateUsesPhpEscape(t *testing.T) {
	efs := fstest.MapFS{
		"t.tmpl": &fstest.MapFile{
			Data: []byte(`<?php define('X', '{{ phpEscape .V }}');`),
		},
	}
	out, err := renderTemplate(efs, "t.tmpl", struct{ V string }{V: `it's \weird\`})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := `<?php define('X', 'it\'s \\weird\\');`
	if string(out) != want {
		t.Errorf("got %q, want %q", string(out), want)
	}
}

// fileFS is a minimal io/fs.FS-style adapter that satisfies the
// `ReadFile(path string) ([]byte, error)` interface renderTemplate uses,
// reading from the host filesystem rooted at `root`. Used to exercise the
// actual checked-in templates without pulling in the main embed.FS.
type fileFS struct{ root string }

func (f fileFS) ReadFile(p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(f.root, p))
}

func TestEmbeddedWPConfigTemplatesParseAndRender(t *testing.T) {
	// Walk up from this test's package dir to the repo root (where the
	// `config/` directory lives). The package sits at internal/sites, so
	// two levels up.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := filepath.Clean(filepath.Join(wd, "..", ".."))
	efs := fileFS{root: repoRoot}

	salts, err := generateSalts()
	if err != nil {
		t.Fatal(err)
	}
	data := wpConfigData{
		Salts:         salts,
		DBPassword:    "p4ss'wd\\test",
		Domain:        "example.localhost",
		Multisite:     "subdomain",
		WPHome:        "https://example.localhost",
		WPSiteURL:     "https://example.localhost",
		PrimaryHost:   "example.localhost",
		LANHostRegex:  "",
		DocrootSuffix: "",
	}

	mainOut, err := renderTemplate(efs, "config/wordpress/wp-config.tmpl.php", data)
	if err != nil {
		t.Fatalf("render wp-config: %v", err)
	}
	includeOut, err := renderTemplate(efs, "config/wordpress/wp-config-locorum.tmpl.php", data)
	if err != nil {
		t.Fatalf("render wp-config-locorum: %v", err)
	}

	for _, c := range []struct {
		name string
		body []byte
		need []string
	}{
		{
			name: "wp-config.php",
			body: mainOut,
			need: []string{
				"#locorum-generated",
				"require_once __DIR__ . '/wp-config-locorum.php'",
				"define( 'AUTH_KEY'",
				"$table_prefix = 'wp_';",
				"require_once ABSPATH . 'wp-settings.php';",
			},
		},
		{
			name: "wp-config-locorum.php",
			body: includeOut,
			need: []string{
				"#locorum-generated",
				"HTTP_X_FORWARDED_PROTO",
				// Dynamic WP_HOME resolver pieces.
				"$locorum_primary_host = 'example.localhost'",
				"$locorum_lan_regex    = ''",
				"$locorum_home = 'https://example.localhost'",
				"define( 'WP_HOME',    $locorum_home )",
				"define( 'WP_SITEURL', $locorum_home . '' )",
				"WP_DEBUG",
				"WP_ALLOW_MULTISITE",
				"SUBDOMAIN_INSTALL',     true",
				"DOMAIN_CURRENT_SITE',   'example.localhost'",
				`getenv( 'MYSQL_PASSWORD' ) ?: 'p4ss\'wd\\test'`,
			},
		},
	} {
		for _, want := range c.need {
			if !strings.Contains(string(c.body), want) {
				t.Errorf("%s: missing %q\n--- output ---\n%s", c.name, want, c.body)
			}
		}
	}
}

func TestBuildLANHostRegex(t *testing.T) {
	cases := []struct {
		name      string
		site      *types.Site
		lanDomain string
		want      string
	}{
		{
			name:      "lan disabled",
			site:      &types.Site{Slug: "myblog", LanEnabled: false},
			lanDomain: "sslip.io",
			want:      "",
		},
		{
			name:      "subdomain multisite skips lan",
			site:      &types.Site{Slug: "myblog", LanEnabled: true, Multisite: "subdomain"},
			lanDomain: "sslip.io",
			want:      "",
		},
		{
			name:      "single site enabled",
			site:      &types.Site{Slug: "myblog", LanEnabled: true},
			lanDomain: "sslip.io",
			want:      `/^myblog\.\d{1,3}-\d{1,3}-\d{1,3}-\d{1,3}\.sslip\.io$/`,
		},
		{
			name:      "subdirectory multisite enabled",
			site:      &types.Site{Slug: "my-store", LanEnabled: true, Multisite: "subdirectory"},
			lanDomain: "sslip.io",
			want:      `/^my-store\.\d{1,3}-\d{1,3}-\d{1,3}-\d{1,3}\.sslip\.io$/`,
		},
		{
			name:      "alt lan domain",
			site:      &types.Site{Slug: "shop", LanEnabled: true},
			lanDomain: "nip.io",
			want:      `/^shop\.\d{1,3}-\d{1,3}-\d{1,3}-\d{1,3}\.nip\.io$/`,
		},
		{
			name:      "empty domain",
			site:      &types.Site{Slug: "shop", LanEnabled: true},
			lanDomain: "",
			want:      "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := buildLANHostRegex(c.site, c.lanDomain); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestDocrootSuffix(t *testing.T) {
	cases := []struct {
		publicDir string
		want      string
	}{
		{"", ""},
		{"/", ""},
		{".", ""},
		{"web", "/web"},
		{"/web", "/web"},
		{"public/wp", "/public/wp"},
	}
	for _, c := range cases {
		if got := docrootSuffix(&types.Site{PublicDir: c.publicDir}); got != c.want {
			t.Errorf("docrootSuffix(%q) = %q, want %q", c.publicDir, got, c.want)
		}
	}
}

// TestEmbeddedWPConfigLANResolver asserts the LAN-host pattern is baked
// into the rendered file when the site has LAN access enabled. Combined
// with TestBuildLANHostRegex (which exercises Go-side construction),
// this gives end-to-end coverage of the resolver.
func TestEmbeddedWPConfigLANResolver(t *testing.T) {
	wd, _ := os.Getwd()
	efs := fileFS{root: filepath.Clean(filepath.Join(wd, "..", ".."))}
	salts, _ := generateSalts()

	t.Run("primary + lan regex", func(t *testing.T) {
		data := wpConfigData{
			Salts:         salts,
			DBPassword:    "x",
			Domain:        "myblog.localhost",
			PrimaryHost:   "myblog.localhost",
			LANHostRegex:  `/^myblog\.\d{1,3}-\d{1,3}-\d{1,3}-\d{1,3}\.sslip\.io$/`,
			WPHome:        "https://myblog.localhost",
			WPSiteURL:     "https://myblog.localhost",
			DocrootSuffix: "",
		}
		out, err := renderTemplate(efs, "config/wordpress/wp-config-locorum.tmpl.php", data)
		if err != nil {
			t.Fatal(err)
		}
		body := string(out)
		mustContain := []string{
			`$locorum_primary_host = 'myblog.localhost'`,
			// phpEscape doubles every backslash so the rendered file
			// has `\\.` where the Go-side regex used `\.`. PHP parses
			// that back to `\.` before PCRE compiles it.
			`$locorum_lan_regex    = '/^myblog\\.\\d{1,3}-\\d{1,3}-\\d{1,3}-\\d{1,3}\\.sslip\\.io$/'`,
			`preg_match( $locorum_lan_regex, $locorum_request_host )`,
			`$locorum_home = 'https://myblog.localhost'`, // fallback path
			`define( 'WP_HOME',    $locorum_home )`,
		}
		for _, m := range mustContain {
			if !strings.Contains(body, m) {
				t.Errorf("missing %q\n--- output ---\n%s", m, body)
			}
		}
	})

	t.Run("docroot suffix flows through", func(t *testing.T) {
		data := wpConfigData{
			Salts:         salts,
			DBPassword:    "x",
			Domain:        "bedrock.localhost",
			PrimaryHost:   "bedrock.localhost",
			DocrootSuffix: "/web",
			WPHome:        "https://bedrock.localhost",
			WPSiteURL:     "https://bedrock.localhost/web",
		}
		out, err := renderTemplate(efs, "config/wordpress/wp-config-locorum.tmpl.php", data)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out), `define( 'WP_SITEURL', $locorum_home . '/web' )`) {
			t.Errorf("docroot suffix not baked into WP_SITEURL\n%s", out)
		}
	})
}

func TestEmbeddedWPConfigOmitsMultisiteWhenDisabled(t *testing.T) {
	wd, _ := os.Getwd()
	efs := fileFS{root: filepath.Clean(filepath.Join(wd, "..", ".."))}
	salts, _ := generateSalts()
	data := wpConfigData{Salts: salts, DBPassword: "x", Domain: "x.localhost", Multisite: ""}

	out, err := renderTemplate(efs, "config/wordpress/wp-config-locorum.tmpl.php", data)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "WP_ALLOW_MULTISITE") {
		t.Errorf("non-multisite render should not include MULTISITE block")
	}
}

func TestWpDocrootDir(t *testing.T) {
	cases := []struct {
		filesDir, publicDir string
		want                string
	}{
		{"/site", "", "/site"},
		{"/site", "/", "/site"},
		{"/site", ".", "/site"},
		{"/site", "wordpress", "/site/wordpress"},
		{"/site", "web", "/site/web"},
	}
	for _, c := range cases {
		got := wpDocrootDir(&types.Site{FilesDir: c.filesDir, PublicDir: c.publicDir})
		// wpDocrootDir returns a host-side path: filepath.Join produces
		// backslashes on Windows. Normalise both sides for the
		// equality check so the test exercises join semantics, not
		// the OS separator.
		if filepath.ToSlash(got) != c.want {
			t.Errorf("wpDocrootDir(%q,%q) = %q, want %q", c.filesDir, c.publicDir, got, c.want)
		}
	}
}
