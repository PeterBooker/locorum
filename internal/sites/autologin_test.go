package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeterBooker/locorum/internal/types"
)

// TestInstallAutoLoginPlugin_WritesIdempotentlyAndCarriesMarker locks
// the persistent mu-plugin contract: written under
// wp-content/mu-plugins/, mode 0600, contains the locorum-generated
// marker so genmark.WriteIfManaged can recognise it on later runs.
func TestInstallAutoLoginPlugin_WritesIdempotentlyAndCarriesMarker(t *testing.T) {
	dir := t.TempDir()
	site := &types.Site{FilesDir: dir}

	if err := installAutoLoginPlugin(site); err != nil {
		t.Fatalf("install: %v", err)
	}

	pluginPath := filepath.Join(dir, "wp-content", "mu-plugins", "locorum-autologin.php")
	body, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin: %v", err)
	}
	if !strings.Contains(string(body), "#locorum-generated") {
		t.Errorf("plugin body missing locorum-generated marker — genmark cannot detect Locorum ownership")
	}
	if !strings.Contains(string(body), "hash_equals") {
		t.Errorf("plugin body missing hash_equals — token comparison must be constant-time")
	}
	if !strings.Contains(string(body), "@unlink($tokenFile)") {
		t.Errorf("plugin body missing token unlink — one-time-use guarantee broken")
	}
	st, err := os.Stat(pluginPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("plugin mode = %v, want 0o600", mode)
	}

	// Idempotent: a second call must not error and must not change the
	// file contents.
	if err := installAutoLoginPlugin(site); err != nil {
		t.Fatalf("second install: %v", err)
	}
	body2, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if string(body) != string(body2) {
		t.Errorf("plugin body changed across idempotent install calls")
	}
}

// TestInstallAutoLoginPlugin_RespectsUserStrippedMarker asserts F9 /
// invariant 16: a user who removes the locorum-generated marker has
// opted out, and Locorum must not overwrite their version.
func TestInstallAutoLoginPlugin_RespectsUserStrippedMarker(t *testing.T) {
	dir := t.TempDir()
	site := &types.Site{FilesDir: dir}
	pluginsDir := filepath.Join(dir, "wp-content", "mu-plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(pluginsDir, "locorum-autologin.php")
	userBody := "<?php\n// user-edited, no marker\n"
	if err := os.WriteFile(pluginPath, []byte(userBody), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := installAutoLoginPlugin(site); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != userBody {
		t.Errorf("user-owned file was overwritten\n got: %q\nwant: %q", got, userBody)
	}
}

// TestWriteAutoLoginToken_AtomicAndOneTime locks the token file
// contract: written atomically (rename), mode 0600, content is one
// trimmable hex token. The plugin's hash_equals comparison relies on
// the token being plain hex — no surrounding bytes that would break
// length equality.
func TestWriteAutoLoginToken_AtomicAndOneTime(t *testing.T) {
	dir := t.TempDir()
	site := &types.Site{FilesDir: dir}

	token, err := writeAutoLoginToken(site)
	if err != nil {
		t.Fatalf("write token: %v", err)
	}
	if len(token) != 64 {
		t.Errorf("token len = %d, want 64 (32 random bytes hex-encoded)", len(token))
	}
	for _, c := range token {
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !ok {
			t.Errorf("token contains non-hex byte %q", c)
			break
		}
	}

	tokenPath := filepath.Join(dir, "wp-content", ".locorum", "login-token")
	body, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	if strings.TrimSpace(string(body)) != token {
		t.Errorf("file content %q does not match returned token %q", body, token)
	}
	st, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := st.Mode().Perm(); mode != 0o600 {
		t.Errorf("token mode = %v, want 0o600", mode)
	}

	// Repeated calls produce fresh tokens (no replay).
	tok2, err := writeAutoLoginToken(site)
	if err != nil {
		t.Fatal(err)
	}
	if tok2 == token {
		t.Errorf("two consecutive tokens collided — randomness pipeline broken")
	}
}

// TestAutoLoginAppRoot_HandlesPublicDir mirrors the WordPress public-
// dir convention used everywhere else in this package: empty / "/"
// means the docroot is the site files dir; anything else is a
// subdirectory underneath it.
func TestAutoLoginAppRoot_HandlesPublicDir(t *testing.T) {
	cases := []struct {
		filesDir, publicDir, want string
	}{
		{"/x", "", "/x"},
		{"/x", "/", "/x"},
		{"/x", "public", "/x/public"},
		{"/x", "/public/", "/x/public"},
	}
	for _, tc := range cases {
		got := autoLoginAppRoot(&types.Site{FilesDir: tc.filesDir, PublicDir: tc.publicDir})
		if got != tc.want {
			t.Errorf("autoLoginAppRoot(%q,%q) = %q, want %q", tc.filesDir, tc.publicDir, got, tc.want)
		}
	}
}
