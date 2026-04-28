package sites

import (
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/orch"
	"github.com/PeterBooker/locorum/internal/sites/sitesteps"
	"github.com/PeterBooker/locorum/internal/types"
)

// ImportDBOptions controls how ImportDB rewrites URLs after the dump
// lands. The defaults are chosen to "do the right thing" for a typical
// production-to-local restore — the user can opt out via DisableAuto.
type ImportDBOptions struct {
	// SearchReplace runs after the import and after the auto pairs.
	// Use this to add explicit overrides — e.g. when the production
	// dump has both naked and www variants of the host.
	SearchReplace []SearchReplacePair

	// DisableAuto skips the automatic siteurl/home detection. The
	// import then runs unchanged; useful for "I just want the data,
	// I'll re-link manually" workflows.
	DisableAuto bool

	// KeepDump retains the in-container preprocessed dump file after
	// import. Default: false. Set true for debugging only — a 5 GB
	// retained dump can fill the user's filesystem fast.
	KeepDump bool
}

// SearchReplacePair is a single from→to URL substitution applied via
// `wp search-replace`. Both fields are required; an empty value is
// rejected.
type SearchReplacePair struct {
	From, To string
}

// ImportDB streams a host-side SQL dump into the site's database with
// preprocessing (drop CREATE DATABASE / USE, normalise collations, strip
// DEFINER, etc.) and an optional auto-search-replace from the imported
// site URL to the local one.
//
// Supported source formats: .sql, .sql.gz, .sql.bz2, and .zip with a
// single .sql entry inside. .sql.xz is not supported in v1 because the
// stdlib has no xz reader; users can decompress to .sql first.
//
// The site MUST be running. The per-site mutex is held for the duration —
// concurrent Start/Stop/Delete on the same site will queue.
//
// The PreImportDB / PostImportDB hooks fire around the whole flow; the
// caller-supplied SearchReplace and the auto-detected pairs run between
// them so a post-import-db hook sees the local URLs already in place.
func (sm *SiteManager) ImportDB(ctx context.Context, siteID, hostPath string, opts ImportDBOptions) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if !site.Started {
		return errors.New("site must be running to import a database")
	}
	if hostPath == "" {
		return errors.New("import path is empty")
	}
	for _, p := range opts.SearchReplace {
		if p.From == "" || p.To == "" {
			return errors.New("search-replace pairs require both From and To")
		}
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	if err := sm.runHooks(ctx, hooks.PreImportDB, site); err != nil {
		return err
	}

	// importToken keeps the on-disk dump filename unique even if two
	// imports race in different sites (FilesDir is per-site so this is
	// belt-and-braces).
	token, err := importToken()
	if err != nil {
		return fmt.Errorf("import token: %w", err)
	}
	dumpFilename := "locorum-import-" + token + ".sql"
	hostDumpPath := filepath.Join(site.FilesDir, dumpFilename)
	containerDumpPath := "/var/www/html/" + dumpFilename

	cleanup := func() {
		if opts.KeepDump {
			slog.Info("import: retained preprocessed dump", "path", hostDumpPath)
			return
		}
		if err := os.Remove(hostDumpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("import: cleanup failed", "path", hostDumpPath, "err", err.Error())
		}
	}

	plan := orch.Plan{
		Name: "import-db:" + site.Slug,
		Steps: []orch.Step{
			&sitesteps.FuncStep{
				Label: "prepare-dump",
				Do: func(_ context.Context) error {
					return prepareDump(hostPath, hostDumpPath)
				},
				Undo: func(_ context.Context) error {
					cleanup()
					return nil
				},
			},
			&sitesteps.FuncStep{
				Label: "wp-db-import",
				Do: func(ctx context.Context) error {
					_, err := sm.wpDBImport(ctx, site, containerDumpPath)
					return err
				},
			},
			&sitesteps.FuncStep{
				Label: "auto-search-replace",
				Do: func(ctx context.Context) error {
					if opts.DisableAuto && len(opts.SearchReplace) == 0 {
						return nil
					}
					return sm.applySearchReplace(ctx, site, opts)
				},
			},
			&sitesteps.FuncStep{
				Label: "cleanup-dump",
				Do: func(_ context.Context) error {
					cleanup()
					return nil
				},
			},
		},
	}

	res := sm.runPlan(ctx, site.ID, plan)
	if res.FinalError != nil {
		return res.FinalError
	}

	if err := sm.runHooks(ctx, hooks.PostImportDB, site); err != nil {
		return err
	}
	sm.emitSitesUpdate()
	return nil
}

// applySearchReplace runs the auto-detected and user-supplied URL
// rewrites. Auto detection reads `home` and `siteurl` from the imported
// DB; for each value found we build https + http variants paired with
// the local site URL. User-supplied pairs are applied last so they can
// override or extend the auto set.
func (sm *SiteManager) applySearchReplace(ctx context.Context, site *types.Site, opts ImportDBOptions) error {
	pairs := make([]SearchReplacePair, 0, len(opts.SearchReplace)+4)

	if !opts.DisableAuto {
		auto, err := sm.autoSearchReplacePairs(ctx, site)
		if err != nil {
			// Don't fail the import on a soft error here — the import
			// itself succeeded; a missing auto-pair just means the user
			// will need to run search-replace by hand. Surface as warn.
			slog.Warn("import: auto-detect URLs failed", "err", err.Error())
		}
		pairs = append(pairs, auto...)
	}
	pairs = append(pairs, opts.SearchReplace...)

	if len(pairs) == 0 {
		return nil
	}

	for _, p := range dedupePairs(pairs) {
		if p.From == p.To {
			continue
		}
		if _, err := sm.wpSearchReplace(ctx, site, p.From, p.To); err != nil {
			return fmt.Errorf("search-replace %s → %s: %w", p.From, p.To, err)
		}
		slog.Info("import: search-replace applied", "from", p.From, "to", p.To)
	}
	return nil
}

// autoSearchReplacePairs reads siteurl/home from the freshly-imported DB
// and pairs each one with the local site URL. Returns http and https
// variants — production dumps frequently embed both, especially around
// older "force HTTPS" plugins.
func (sm *SiteManager) autoSearchReplacePairs(ctx context.Context, site *types.Site) ([]SearchReplacePair, error) {
	local := "https://" + site.Domain
	var pairs []SearchReplacePair

	for _, key := range []string{"siteurl", "home"} {
		v, err := sm.wpOptionGet(ctx, site, key)
		if err != nil {
			return pairs, fmt.Errorf("read %s: %w", key, err)
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// Only add the pair if the source actually differs from the
		// destination; otherwise the wp-cli call wastes work.
		if v == local {
			continue
		}
		pairs = append(pairs, SearchReplacePair{From: v, To: local})

		// Also add the protocol-flipped variant — production sites
		// often embed http:// strings inside post content even when
		// the canonical URL is https://.
		if flipped := flipScheme(v); flipped != "" && flipped != local {
			pairs = append(pairs, SearchReplacePair{From: flipped, To: local})
		}
	}
	return pairs, nil
}

// flipScheme swaps https↔http on a URL. Returns empty for unparseable
// or non-HTTP(S) inputs. Safe — only reformats; no other URL surgery.
func flipScheme(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "https"
	case "https":
		u.Scheme = "http"
	default:
		return ""
	}
	return u.String()
}

// dedupePairs removes exact duplicates while preserving order.
func dedupePairs(pairs []SearchReplacePair) []SearchReplacePair {
	seen := make(map[SearchReplacePair]struct{}, len(pairs))
	out := pairs[:0]
	for _, p := range pairs {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// prepareDump opens hostPath, transparently decompresses based on
// extension, runs the import-filter pipeline, and writes the cleaned SQL
// to dst. dst is created with 0640 (readable by the PHP UID after the
// chown step on next start; readable now by virtue of the bind-mount
// chmod at site creation). Atomic via tmpfile+rename — a partial dump
// is never visible to wp-cli.
func prepareDump(hostPath, dst string) error {
	src, err := os.Open(hostPath)
	if err != nil {
		return fmt.Errorf("open dump: %w", err)
	}
	defer src.Close()

	reader, err := decompressedReader(hostPath, src)
	if err != nil {
		return err
	}
	if rc, ok := reader.(io.Closer); ok && rc != src {
		defer rc.Close()
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".locorum-import-*.sql")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	cleanupTmp := func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}

	written, err := FilterImportStream(reader, tmp)
	if err != nil {
		cleanupTmp()
		return fmt.Errorf("filter: %w", err)
	}
	if written == 0 {
		cleanupTmp()
		return errors.New("dump produced no SQL after preprocessing — file is empty or not a SQL dump")
	}
	if err := tmp.Sync(); err != nil {
		cleanupTmp()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		closed = true
		_ = os.Remove(tmpName)
		return fmt.Errorf("close tmp: %w", err)
	}
	closed = true

	if err := os.Chmod(tmpName, 0o640); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename: %w", err)
	}
	slog.Info("import: dump preprocessed", "size_bytes", written, "dst", dst)
	return nil
}

// decompressedReader returns a reader over the (possibly compressed) src
// based on the path's extension. The returned reader may equal src for
// uncompressed inputs; if it differs and is an io.Closer, the caller is
// expected to close it.
//
// Supported:
//   - .sql, .dump, no-extension → plain
//   - .gz, .sql.gz → gzip
//   - .bz2, .sql.bz2 → bzip2 (read-only stdlib codec)
//   - .zip → archive/zip with exactly one .sql entry
//
// Unsupported (.xz, .7z, .tar) are rejected with a clear error; the user
// is told to decompress first.
func decompressedReader(path string, src *os.File) (io.Reader, error) {
	lower := strings.ToLower(path)

	switch {
	case strings.HasSuffix(lower, ".gz"):
		gz, err := gzip.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		return gz, nil

	case strings.HasSuffix(lower, ".bz2"):
		// bzip2.NewReader returns an io.Reader (not a Closer); the
		// underlying file is closed by the outer defer.
		return bzip2.NewReader(src), nil

	case strings.HasSuffix(lower, ".zip"):
		return zipSingleSQL(src)

	case strings.HasSuffix(lower, ".xz"),
		strings.HasSuffix(lower, ".7z"),
		strings.HasSuffix(lower, ".tar"),
		strings.HasSuffix(lower, ".tgz"),
		strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tar.bz2"):
		return nil, fmt.Errorf("unsupported dump format %q — decompress to .sql first", filepath.Ext(path))
	}

	// Sniff for a SQL header — protects against silently importing a
	// random binary file someone dragged into the picker.
	head, replay, err := quickSniff(src)
	if err != nil {
		return nil, fmt.Errorf("sniff: %w", err)
	}
	if !looksLikeSQL(head) {
		return nil, errors.New("file does not appear to be a SQL dump (no recognisable header in first 256 bytes)")
	}
	return replay, nil
}

// looksLikeSQL is a deliberately conservative heuristic: a SQL dump
// usually begins with a comment block (`-- MySQL dump …`), a DROP/SET
// statement, or a `/*!\d+ … */` conditional comment. We accept any of
// those + plain `INSERT`/`CREATE` for hand-rolled exports.
func looksLikeSQL(head []byte) bool {
	if len(head) == 0 {
		return false
	}
	prefixes := [][]byte{
		[]byte("--"),
		[]byte("/*"),
		[]byte("SET "),
		[]byte("set "),
		[]byte("DROP "),
		[]byte("CREATE "),
		[]byte("INSERT "),
		[]byte("USE "),
		[]byte("LOCK "),
	}
	trimmed := head
	// Skip optional UTF-8 BOM.
	if len(trimmed) >= 3 && trimmed[0] == 0xEF && trimmed[1] == 0xBB && trimmed[2] == 0xBF {
		trimmed = trimmed[3:]
	}
	for len(trimmed) > 0 && (trimmed[0] == ' ' || trimmed[0] == '\t' || trimmed[0] == '\r' || trimmed[0] == '\n') {
		trimmed = trimmed[1:]
	}
	for _, p := range prefixes {
		if len(trimmed) >= len(p) {
			match := true
			for i := range p {
				if trimmed[i] != p[i] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// zipSingleSQL opens src as a zip and returns a reader for the single
// .sql entry inside. Multi-file zips, no-sql zips, and zips containing
// a directory tree are all rejected with explicit errors — the import
// flow refuses to guess.
func zipSingleSQL(src *os.File) (io.Reader, error) {
	stat, err := src.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat zip: %w", err)
	}
	zr, err := zip.NewReader(src, stat.Size())
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	var sqlEntries []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ToLower(f.Name)
		if strings.HasSuffix(name, ".sql") {
			sqlEntries = append(sqlEntries, f)
		}
	}
	switch len(sqlEntries) {
	case 0:
		return nil, errors.New("zip contains no .sql file")
	case 1:
		return sqlEntries[0].Open()
	default:
		names := make([]string, 0, len(sqlEntries))
		for _, f := range sqlEntries {
			names = append(names, f.Name)
		}
		return nil, fmt.Errorf("zip contains multiple .sql files: %s — extract the one to import first", strings.Join(names, ", "))
	}
}

// importToken is a short hex string used to disambiguate concurrent
// import temp files. 8 bytes = 16 hex chars; collision probability is
// 2^-64, vanishing for any plausible workload.
func importToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
