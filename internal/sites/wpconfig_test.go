package sites

import (
	"bytes"
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

func TestHasLocorumSignature(t *testing.T) {
	if !hasLocorumSignature([]byte("// #locorum-generated\n<?php …")) {
		t.Error("signature on first line should match")
	}
	if hasLocorumSignature([]byte("<?php /* hand-written */")) {
		t.Error("plain file should not match")
	}
	// Signature buried far past peek window must not match — that's a
	// random comment, not an opt-in.
	deep := bytes.Repeat([]byte("// padding\n"), 600)
	deep = append(deep, []byte("// #locorum-generated trailing\n")...)
	if hasLocorumSignature(deep) {
		t.Error("signature beyond peek window should not match")
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

func TestWriteIfManagedAtomicAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wp-config.php")
	body := []byte("<?php // #locorum-generated\n$x = 1;\n")

	// 1st write: creates file.
	if err := writeIfManaged(p, body); err != nil {
		t.Fatalf("first write: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("first write content mismatch")
	}

	// 2nd write of identical content: should be a no-op (no rename).
	st1, _ := os.Stat(p)
	if err := writeIfManaged(p, body); err != nil {
		t.Fatalf("second write: %v", err)
	}
	st2, _ := os.Stat(p)
	if st2.ModTime() != st1.ModTime() {
		t.Errorf("identical content triggered a write (mtime changed)")
	}

	// Different content, signature still present: should overwrite.
	body2 := []byte("<?php // #locorum-generated\n$x = 2;\n")
	if err := writeIfManaged(p, body2); err != nil {
		t.Fatalf("third write: %v", err)
	}
	got, _ = os.ReadFile(p)
	if !bytes.Equal(got, body2) {
		t.Errorf("update did not apply")
	}
}

func TestWriteIfManagedRefusesUnmanagedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "wp-config.php")
	hand := []byte("<?php // user-written\n$x = 99;\n")
	if err := os.WriteFile(p, hand, 0o644); err != nil {
		t.Fatal(err)
	}

	managed := []byte("<?php // #locorum-generated\n$x = 1;\n")
	if err := writeIfManaged(p, managed); err != nil {
		t.Fatalf("writeIfManaged returned error: %v", err)
	}

	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, hand) {
		t.Errorf("user-managed file was overwritten — invariant violated")
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
		Salts:      salts,
		DBPassword: "p4ss'wd\\test",
		Domain:     "example.localhost",
		Multisite:  "subdomain",
		WPHome:     "https://example.localhost",
		WPSiteURL:  "https://example.localhost",
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
				"WP_HOME',    'https://example.localhost'",
				"WP_SITEURL', 'https://example.localhost'",
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
		if got != c.want {
			t.Errorf("wpDocrootDir(%q,%q) = %q, want %q", c.filesDir, c.publicDir, got, c.want)
		}
	}
}
