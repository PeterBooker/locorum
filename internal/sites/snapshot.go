package sites

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/types"
)

// shouldAutoSnapshot reports whether the auto-snapshot wrap is on.
// True is the safe default; users with their own backup discipline
// flip the snapshots.auto_before_destructive setting to disable.
//
// SiteManagers constructed in tests without a *config.Config (cfg ==
// nil) get the default behaviour: auto-snapshot ON.
func (sm *SiteManager) shouldAutoSnapshot() bool {
	if sm == nil || sm.cfg == nil {
		return true
	}
	return sm.cfg.AutoSnapshotBeforeDestructive()
}

// snapshotsRoot is the per-user directory holding all sites' snapshots.
// Lives outside the site's files tree so an accidental `rm -rf
// ~/locorum/sites/{slug}` does NOT take the snapshots with it — that's
// the entire point of an auto-snapshot.
const snapshotsRoot = "snapshots"

// snapshotLabel is a short identifier baked into the snapshot filename
// so the user can tell at a glance why each backup exists. The alphabet
// excludes `-` (we use `--` as a segment separator in filenames; allowing
// it inside labels would make parsing ambiguous) and any character that
// would cross path-separator or shell-metacharacter boundaries.
var snapshotLabelPat = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,31}$`)

// snapshotEnginePat / snapshotVersionPat sanitise engine + version before
// they're embedded into the filename. The same constraint as labels:
// alphanumerics + a few separators only.
var (
	snapshotEnginePat  = regexp.MustCompile(`[^a-z0-9]+`)
	snapshotVersionPat = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

// snapshotTSFormat is the canonical UTC timestamp baked into snapshot
// filenames. Lexicographic sort = chronological sort, no separator
// characters that need shell-escaping.
const snapshotTSFormat = "20060102T150405Z"

// snapshotSep is the segment separator used between {slug, label, ts,
// engineVersion}. Double-dash is unambiguous because:
//   - Slugs (from gosimple/slug) only ever contain single hyphens.
//   - Labels are validated against snapshotLabelPat which forbids `-`.
//   - The timestamp uses [0-9TZ] only.
//   - Engine+version are joined with a single `-`.
const snapshotSep = "--"

// Compression codec markers used in filenames.
const (
	codecGzip = "gzip"
	codecZstd = "zstd"

	extGzip = ".sql.gz"
	extZstd = ".sql.zst"
)

// DefaultSnapshotCodec is the compression codec used for new snapshots.
// zstd wins on speed and ratio for SQL dumps; gzip is still readable on
// the way back so existing snapshots keep working.
const DefaultSnapshotCodec = codecZstd

// SnapshotInfo describes a stored snapshot for the GUI list view.
type SnapshotInfo struct {
	Filename    string    `json:"filename"`
	HostPath    string    `json:"hostPath"`
	Slug        string    `json:"slug"`
	Label       string    `json:"label"`
	Engine      string    `json:"engine"`
	Version     string    `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
	SizeBytes   int64     `json:"sizeBytes"`
	Compression string    `json:"compression"`
	HasChecksum bool      `json:"hasChecksum"`
}

// snapshotsDir returns the on-disk root for snapshots. ~/.locorum/snapshots/.
// Created on demand; the caller can rely on the directory existing on
// return.
func (sm *SiteManager) snapshotsDir() (string, error) {
	dir := filepath.Join(sm.homeDir, ".locorum", snapshotsRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("ensure snapshots dir: %w", err)
	}
	return dir, nil
}

// Snapshot exports the site's database to ~/.locorum/snapshots/ as a
// compressed SQL dump with a SHA-256 sidecar for integrity verification.
// Returns the absolute host path of the snapshot.
//
// Design choices:
//   - The dump streams direct from the database container's mysqldump
//     stdout into the compressor on the host. No plaintext SQL ever
//     touches FilesDir or any other disk location — closing a small but
//     real exposure window where the dump was readable to anyone with
//     site-files access.
//   - Compression defaults to zstd; gzipped snapshots remain readable on
//     restore so older snapshots survive the transition.
//   - SHA-256 is computed in the same single-pass stream and persisted as
//     `<file>.sha256`. Restore verifies before importing.
//   - Engine + version are encoded in the filename so RestoreSnapshot can
//     reject mismatched restores unless explicitly overridden
//     (LEARNINGS.md F18, §4.6).
//   - Pre/post-snapshot hooks fire so users can wire in their own
//     pre-flight checks (e.g. flush caches) without modifying Locorum.
func (sm *SiteManager) Snapshot(ctx context.Context, siteID, label string) (string, error) {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return "", fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return "", fmt.Errorf("site %q not found", siteID)
	}
	if !site.Started {
		return "", fmt.Errorf("%w: cannot snapshot", ErrSiteNotRunning)
	}
	if !snapshotLabelPat.MatchString(label) {
		return "", fmt.Errorf("invalid snapshot label %q: must match %s", label, snapshotLabelPat)
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	return sm.snapshotLocked(ctx, site, label)
}

// snapshotLocked is the lock-free body of Snapshot, also reachable from
// internal callers (e.g. DeleteSite's auto-snapshot step) that already
// hold the site mutex.
func (sm *SiteManager) snapshotLocked(ctx context.Context, site *types.Site, label string) (string, error) {
	if err := sm.runHooks(ctx, hooks.PreSnapshot, site); err != nil {
		return "", err
	}

	dir, err := sm.snapshotsDir()
	if err != nil {
		return "", err
	}

	eng := dbengine.Resolve(site)
	finalName := buildSnapshotName(site.Slug, label, string(eng.Kind()), site.DBVersion, time.Now().UTC(), DefaultSnapshotCodec)
	finalPath := filepath.Join(dir, finalName)

	// Stream-and-hash: tee the engine's stdout through SHA-256 into the
	// compressor into a tmpfile beside the destination. Atomic rename on
	// success; partial files never appear in ListSnapshots.
	tmp, err := os.CreateTemp(dir, ".locorum-snap-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanupTmp := func() { _ = os.Remove(tmpName) }

	hasher := sha256.New()
	cw, err := newCompressWriter(DefaultSnapshotCodec, tmp)
	if err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return "", fmt.Errorf("compressor: %w", err)
	}
	hashedW := io.MultiWriter(cw, hasher)

	bytesWritten, snapErr := eng.Snapshot(ctx, sm.d, site, hashedW)
	if snapErr != nil {
		_ = cw.Close()
		_ = tmp.Close()
		cleanupTmp()
		return "", fmt.Errorf("engine snapshot: %w", snapErr)
	}
	if err := cw.Close(); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return "", fmt.Errorf("flush compressor: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return "", fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTmp()
		return "", fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		cleanupTmp()
		return "", fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		cleanupTmp()
		return "", fmt.Errorf("rename: %w", err)
	}

	// Write the SHA-256 sidecar. Failure to write the sidecar is
	// non-fatal — the snapshot itself is good; restore will warn that
	// integrity verification was skipped.
	checksum := hex.EncodeToString(hasher.Sum(nil))
	if err := writeChecksum(finalPath, checksum); err != nil {
		slog.Warn("snapshot: failed to write checksum sidecar", "path", finalPath, "err", err.Error())
	}

	slog.Info("snapshot: created",
		"site", site.Slug,
		"db_engine", eng.Kind(),
		"db_version", site.DBVersion,
		"path", finalPath,
		"plaintext_bytes", bytesWritten,
	)

	if err := sm.runHooks(ctx, hooks.PostSnapshot, site); err != nil {
		// Snapshot is already on disk — log the post-hook failure and
		// return success. The user has their backup; the post-hook is
		// observability-only by convention.
		slog.Warn("post-snapshot hook failed", "err", err.Error())
	}
	return finalPath, nil
}

// buildSnapshotName assembles a deterministic filename. Format:
//
//	{slug}--{label}--{tsUTC}--{engine}-{version}.sql.{gz|zst}
//
// All fields are sanitised so a hostile / unusual value can't escape the
// filename via path separators or shell metacharacters.
func buildSnapshotName(slug, label, engine, version string, ts time.Time, codec string) string {
	engine = strings.ToLower(snapshotEnginePat.ReplaceAllString(strings.ToLower(engine), ""))
	if engine == "" {
		engine = "mysql"
	}
	version = snapshotVersionPat.ReplaceAllString(version, "")
	if version == "" {
		version = "unknown"
	}
	stem := strings.Join([]string{
		slug,
		label,
		ts.Format(snapshotTSFormat),
		engine + "-" + version,
	}, snapshotSep)
	switch codec {
	case codecZstd:
		return stem + extZstd
	case codecGzip:
		return stem + extGzip
	default:
		return stem + extZstd
	}
}

// parseSnapshotName turns a filename back into a SnapshotInfo (without
// HostPath / SizeBytes — caller fills those after stat). Returns nil for
// unrecognised filenames; the caller silently ignores them so a stray
// file in the snapshots dir doesn't break listings.
func parseSnapshotName(name string) *SnapshotInfo {
	var codec, stem string
	switch {
	case strings.HasSuffix(name, extZstd):
		codec = codecZstd
		stem = strings.TrimSuffix(name, extZstd)
	case strings.HasSuffix(name, extGzip):
		codec = codecGzip
		stem = strings.TrimSuffix(name, extGzip)
	default:
		return nil
	}
	parts := strings.Split(stem, snapshotSep)
	if len(parts) != 4 {
		return nil
	}
	slug, label, tsRaw, ev := parts[0], parts[1], parts[2], parts[3]
	if slug == "" || label == "" {
		return nil
	}
	ts, err := time.Parse(snapshotTSFormat, tsRaw)
	if err != nil {
		return nil
	}
	engine, version := splitEngineVersion(ev)
	if engine == "" {
		return nil
	}
	return &SnapshotInfo{
		Filename:    name,
		Slug:        slug,
		Label:       label,
		CreatedAt:   ts,
		Engine:      engine,
		Version:     version,
		Compression: codec,
	}
}

// splitEngineVersion separates "mysql-8.0" → ("mysql", "8.0"). Returns
// ("", "") if the input doesn't match the engine[-version] shape.
func splitEngineVersion(s string) (engine, version string) {
	idx := strings.IndexByte(s, '-')
	if idx <= 0 {
		// Pure-letters engine with no version (e.g. "mysql"); accept.
		if s != "" && isAllLowerAlpha(s) {
			return s, ""
		}
		return "", ""
	}
	engine = s[:idx]
	version = s[idx+1:]
	if !isAllLowerAlpha(engine) {
		return "", ""
	}
	return engine, version
}

func isAllLowerAlpha(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// ListSnapshots returns every snapshot for slug, newest first. Pass an
// empty slug to list snapshots for every site.
func (sm *SiteManager) ListSnapshots(slug string) ([]SnapshotInfo, error) {
	dir, err := sm.snapshotsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read snapshots: %w", err)
	}
	var out []SnapshotInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info := parseSnapshotName(e.Name())
		if info == nil {
			continue
		}
		if slug != "" && info.Slug != slug {
			continue
		}
		fi, err := e.Info()
		if err == nil {
			info.SizeBytes = fi.Size()
		}
		info.HostPath = filepath.Join(dir, e.Name())
		if _, err := os.Stat(info.HostPath + ".sha256"); err == nil {
			info.HasChecksum = true
		}
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// DeleteSnapshot removes a snapshot file (and its sidecar) by path. The
// path is validated to live inside the snapshots dir so a hostile caller
// cannot point us at /etc/passwd.
func (sm *SiteManager) DeleteSnapshot(snapshotPath string) error {
	dir, err := sm.snapshotsDir()
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(snapshotPath)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}
	if !strings.HasPrefix(abs, dir+string(os.PathSeparator)) {
		return fmt.Errorf("snapshot path %q is outside the snapshots dir", abs)
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove snapshot: %w", err)
	}
	if err := os.Remove(abs + ".sha256"); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("snapshot: remove sidecar", "path", abs+".sha256", "err", err.Error())
	}
	slog.Info("snapshot: deleted", "path", abs)
	return nil
}

// RestoreSnapshotOptions controls the restore flow.
type RestoreSnapshotOptions struct {
	// AllowEngineMismatch lets a snapshot taken on a different engine /
	// version restore into the current site. Used by the engine-migrate
	// flow which deliberately swaps engines.
	AllowEngineMismatch bool

	// SkipChecksum disables SHA-256 verification before restore. Default
	// false. Skip if the user is restoring a snapshot they trust without
	// a sidecar.
	SkipChecksum bool

	// SkipAutoSnapshot disables the pre-restore safety snapshot that
	// RestoreSnapshot otherwise takes (P4 in AGENTS-SUPPORT). Set true
	// when the caller has already established a recovery point (e.g.
	// the worktree clone-DB flow that snapshots the parent before
	// touching the child) or when restoring into a known-empty DB.
	SkipAutoSnapshot bool
}

// RestoreSnapshot reconstructs the database from a snapshot. The site
// must be running. Engine compatibility is checked against the filename
// metadata: a snapshot tagged `mysql8.0` will refuse to restore into a
// `mysql5.7` site (LEARNINGS.md F18). Override with AllowEngineMismatch.
//
// Auto-snapshot wrap (P4): unless opts.SkipAutoSnapshot is true, a
// "pre_restore" snapshot is taken before the destructive restore, so a
// botched restore can be rolled back with one command. The wrap is
// gated by snapshots.auto_before_destructive (default true) so power
// users with their own backup discipline can disable.
func (sm *SiteManager) RestoreSnapshot(ctx context.Context, siteID, snapshotPath string, opts RestoreSnapshotOptions) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if !site.Started {
		return fmt.Errorf("%w: cannot restore snapshot", ErrSiteNotRunning)
	}
	info := parseSnapshotName(filepath.Base(snapshotPath))
	if info != nil && !opts.AllowEngineMismatch {
		if info.Engine != "" && info.Engine != site.DBEngine {
			return fmt.Errorf("snapshot engine %s does not match site engine %s — pass AllowEngineMismatch to override", info.Engine, site.DBEngine)
		}
		if info.Version != "" && site.DBVersion != "" && info.Version != site.DBVersion {
			return fmt.Errorf("snapshot version %s does not match site version %s — pass AllowEngineMismatch to override", info.Version, site.DBVersion)
		}
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	// Pre-restore safety snapshot. A failure here logs and continues:
	// refusing to restore because of a snapshot failure would strand
	// users whose DB volume is the very thing they're trying to
	// repair. The snapshot label `pre_restore` makes the recovery
	// point easy to find in ListSnapshots.
	if !opts.SkipAutoSnapshot && sm.shouldAutoSnapshot() {
		if path, serr := sm.snapshotLocked(ctx, site, "pre_restore"); serr != nil {
			slog.Warn("pre-restore auto-snapshot failed", "site", site.Slug, "err", serr.Error())
		} else {
			slog.Info("pre-restore auto-snapshot saved", "path", path)
		}
	}

	// Verify checksum first if a sidecar exists. We do this before
	// touching the live database so a corrupt snapshot is rejected
	// without leaving the DB in a half-restored state.
	if !opts.SkipChecksum {
		if err := verifyChecksum(snapshotPath); err != nil {
			if !errors.Is(err, errNoChecksumSidecar) {
				return fmt.Errorf("checksum verify: %w", err)
			}
			slog.Warn("snapshot: no checksum sidecar — restoring without verification",
				"path", snapshotPath)
		}
	}

	f, err := os.Open(snapshotPath)
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer f.Close()

	dec, err := newDecompressReader(snapshotPath, f)
	if err != nil {
		return fmt.Errorf("decompress: %w", err)
	}
	if c, ok := dec.(io.Closer); ok && c != f {
		defer c.Close()
	}

	eng := dbengine.Resolve(site)
	if err := eng.Restore(ctx, sm.d, site, dec); err != nil {
		return fmt.Errorf("engine restore: %w", err)
	}
	slog.Info("restore: complete",
		"site", site.Slug,
		"db_engine", eng.Kind(),
		"from", snapshotPath,
	)
	return nil
}

// ──────────────────────────────────────────────────────────────────────
// Codec helpers
// ──────────────────────────────────────────────────────────────────────

// compressWriter is an io.WriteCloser that finalises the codec footer on
// Close. zstd's encoder needs explicit Close to flush; gzip the same.
type compressWriter struct {
	gz    *gzip.Writer
	zw    *zstd.Encoder
	codec string
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if c.gz != nil {
		return c.gz.Write(p)
	}
	return c.zw.Write(p)
}

func (c *compressWriter) Close() error {
	if c.gz != nil {
		return c.gz.Close()
	}
	return c.zw.Close()
}

func newCompressWriter(codec string, w io.Writer) (*compressWriter, error) {
	switch codec {
	case codecGzip:
		gz, err := gzip.NewWriterLevel(w, gzip.BestSpeed)
		if err != nil {
			return nil, err
		}
		return &compressWriter{gz: gz, codec: codec}, nil
	case codecZstd:
		// SpeedDefault tracks "level 3" — best speed/ratio tradeoff for
		// SQL dumps where we expect aggressive textual redundancy.
		zw, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.SpeedDefault))
		if err != nil {
			return nil, err
		}
		return &compressWriter{zw: zw, codec: codec}, nil
	}
	return nil, fmt.Errorf("unknown codec %q", codec)
}

// newDecompressReader returns an io.Reader over the snapshot file based on
// its extension. The caller is responsible for closing the returned
// reader if it implements io.Closer.
func newDecompressReader(path string, src io.Reader) (io.Reader, error) {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, extZstd):
		dec, err := zstd.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("zstd: %w", err)
		}
		// zstd.Decoder is io.ReadCloser; expose as such so the caller
		// can close it via type assertion.
		return decoderCloser{Decoder: dec}, nil
	case strings.HasSuffix(lower, extGzip):
		gr, err := gzip.NewReader(src)
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		return gr, nil
	}
	return nil, fmt.Errorf("unrecognised snapshot extension: %s", filepath.Ext(path))
}

// decoderCloser wraps zstd.Decoder so it satisfies io.Closer (Decoder.Close
// returns nothing, but io.Closer wants `error`).
type decoderCloser struct{ *zstd.Decoder }

func (d decoderCloser) Close() error {
	d.Decoder.Close()
	return nil
}

// ──────────────────────────────────────────────────────────────────────
// Checksum sidecar
// ──────────────────────────────────────────────────────────────────────

// errNoChecksumSidecar is returned when the .sha256 sidecar is absent.
// Callers treat this as a soft warning (older snapshots predate the
// sidecar feature).
var errNoChecksumSidecar = errors.New("no checksum sidecar")

func writeChecksum(snapshotPath, hexDigest string) error {
	body := hexDigest + "  " + filepath.Base(snapshotPath) + "\n"
	tmp, err := os.CreateTemp(filepath.Dir(snapshotPath), ".locorum-cs-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, snapshotPath+".sha256")
}

func verifyChecksum(snapshotPath string) error {
	expected, err := os.ReadFile(snapshotPath + ".sha256")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errNoChecksumSidecar
		}
		return fmt.Errorf("read sidecar: %w", err)
	}
	expectedHex := strings.TrimSpace(strings.SplitN(string(expected), " ", 2)[0])
	if len(expectedHex) != 64 {
		return fmt.Errorf("malformed checksum sidecar")
	}
	f, err := os.Open(snapshotPath)
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expectedHex {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, actual)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────
// Retention sweep
// ──────────────────────────────────────────────────────────────────────

// SnapshotRetentionPolicy bounds the on-disk snapshot count + age. Sweep
// removes anything that exceeds either limit.
type SnapshotRetentionPolicy struct {
	// MaxPerSite caps the number of snapshots kept per site. The
	// newest-N are kept; the older ones drop. 0 → unlimited.
	MaxPerSite int `json:"maxPerSite"`

	// MaxAge is the TTL after which a snapshot is removed regardless of
	// the per-site count. 0 → no age limit.
	MaxAge time.Duration `json:"maxAge"`
}

// DefaultRetentionPolicy is the in-tree fallback. ~/.locorum/snapshots/
// can override via .policy.json.
var DefaultRetentionPolicy = SnapshotRetentionPolicy{
	MaxPerSite: 20,
	MaxAge:     365 * 24 * time.Hour,
}

// LoadRetentionPolicy reads the on-disk policy, falling back to defaults
// for missing or malformed files. Soft failure mode: a bad JSON file
// should not stop Locorum from starting.
func (sm *SiteManager) LoadRetentionPolicy() SnapshotRetentionPolicy {
	dir, err := sm.snapshotsDir()
	if err != nil {
		return DefaultRetentionPolicy
	}
	body, err := os.ReadFile(filepath.Join(dir, ".policy.json"))
	if err != nil {
		return DefaultRetentionPolicy
	}
	var p SnapshotRetentionPolicy
	if err := json.Unmarshal(body, &p); err != nil {
		slog.Warn("snapshot: bad retention policy JSON, using defaults", "err", err.Error())
		return DefaultRetentionPolicy
	}
	if p.MaxPerSite < 0 || p.MaxAge < 0 {
		return DefaultRetentionPolicy
	}
	return p
}

// SaveRetentionPolicy persists the policy. Used by the settings UI.
func (sm *SiteManager) SaveRetentionPolicy(p SnapshotRetentionPolicy) error {
	dir, err := sm.snapshotsDir()
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".policy-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, ".policy.json"))
}

// SweepSnapshots applies the retention policy. Called at startup and
// after every successful snapshot. Logs each removal so a confused user
// can audit. Returns the count removed.
func (sm *SiteManager) SweepSnapshots(p SnapshotRetentionPolicy) (int, error) {
	if p.MaxPerSite == 0 && p.MaxAge == 0 {
		return 0, nil
	}
	all, err := sm.ListSnapshots("")
	if err != nil {
		return 0, err
	}
	bySite := map[string][]SnapshotInfo{}
	for _, s := range all {
		bySite[s.Slug] = append(bySite[s.Slug], s)
	}
	removed := 0
	now := time.Now()
	for _, group := range bySite {
		// ListSnapshots returns newest first; index >= MaxPerSite is the
		// "too many" tail.
		for i, snap := range group {
			drop := false
			if p.MaxPerSite > 0 && i >= p.MaxPerSite {
				drop = true
			}
			if !drop && p.MaxAge > 0 && now.Sub(snap.CreatedAt) > p.MaxAge {
				drop = true
			}
			if !drop {
				continue
			}
			if err := sm.DeleteSnapshot(snap.HostPath); err != nil {
				slog.Warn("snapshot: sweep delete failed", "path", snap.HostPath, "err", err.Error())
				continue
			}
			removed++
		}
	}
	if removed > 0 {
		slog.Info("snapshot: retention sweep complete", "removed", removed)
	}
	return removed, nil
}
