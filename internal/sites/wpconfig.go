package sites

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/PeterBooker/locorum/internal/types"
)

// LocorumGeneratedSig is the marker that flags a Locorum-managed file. If
// the user removes this string from a generated file, Locorum will refuse
// to overwrite it on the next regenerate. See LEARNINGS.md §2.3.
const LocorumGeneratedSig = "#locorum-generated"

// signaturePeekBytes caps how far into a file we look for the signature.
// 4 KiB is more than enough — the signature always lives in the first
// comment block.
const signaturePeekBytes = 4096

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
type wpConfigData struct {
	Salts      map[string]string
	DBPassword string
	Domain     string
	Multisite  string
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

	data := wpConfigData{
		Salts:      salts,
		DBPassword: site.DBPassword,
		Domain:     site.Domain,
		Multisite:  site.Multisite,
	}

	dir := wpDocrootDir(site)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure docroot: %w", err)
	}

	// wp-config.php: respect a user-stripped signature, otherwise write.
	mainPath := filepath.Join(dir, "wp-config.php")
	mainBytes, err := renderTemplate(sm.config, "config/wordpress/wp-config.tmpl.php", data)
	if err != nil {
		return fmt.Errorf("render wp-config.php: %w", err)
	}
	if err := writeIfManaged(mainPath, mainBytes); err != nil {
		return fmt.Errorf("write wp-config.php: %w", err)
	}

	// wp-config-locorum.php: always managed; the signature is in the
	// rendered bytes so writeIfManaged still does the right thing.
	includePath := filepath.Join(dir, "wp-config-locorum.php")
	includeBytes, err := renderTemplate(sm.config, "config/wordpress/wp-config-locorum.tmpl.php", data)
	if err != nil {
		return fmt.Errorf("render wp-config-locorum.php: %w", err)
	}
	if err := writeIfManaged(includePath, includeBytes); err != nil {
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

// writeIfManaged writes content to path under three rules:
//   - File missing → write it.
//   - File exists, contents identical → skip (idempotent).
//   - File exists, has the Locorum signature, contents differ → atomic
//     overwrite via tmpfile+rename.
//   - File exists, NO Locorum signature → refuse to overwrite, log warn.
//
// Atomic write: rename(2) on the same filesystem is atomic on POSIX and
// (since 2016) on Windows ReFS/NTFS. The temp file is in the same
// directory so the rename never crosses a filesystem boundary.
func writeIfManaged(path string, content []byte) error {
	existing, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return atomicWrite(path, content)
	case err != nil:
		return fmt.Errorf("read existing: %w", err)
	}

	if bytes.Equal(existing, content) {
		return nil
	}

	if !hasLocorumSignature(existing) {
		slog.Warn("refusing to overwrite user-managed file (signature missing)",
			"path", path,
			"hint", "remove the file or add `// "+LocorumGeneratedSig+"` to the top to opt back into management")
		return nil
	}

	return atomicWrite(path, content)
}

// hasLocorumSignature returns true if content's first signaturePeekBytes
// bytes contain the marker. Looking only at the prefix means a stray
// occurrence deep inside the file (e.g. inside a docblock) doesn't
// accidentally re-enable management.
func hasLocorumSignature(content []byte) bool {
	head := content
	if len(head) > signaturePeekBytes {
		head = head[:signaturePeekBytes]
	}
	return bytes.Contains(head, []byte(LocorumGeneratedSig))
}

// atomicWrite writes content to path via tmpfile+rename. Permissions match
// the wp-content writability convention (0644 — readable by everyone, the
// PHP container reads it as its own UID after ChownStep).
func atomicWrite(path string, content []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".locorum-tmp-*")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
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
