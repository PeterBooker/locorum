package sites

import (
	"compress/gzip"
	"context"
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

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/types"
)

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
// gzipped SQL file. Returns the absolute host path of the snapshot.
//
// Design choices:
//   - mysqldump runs via `wp db export` inside the PHP container — wp-cli
//     handles the credential discovery and is already a hard dependency
//     so we don't widen the attack surface.
//   - The dump lands first inside the bind-mounted FilesDir (so the
//     container has a valid write path), then is gzipped onto the host
//     and the plaintext copy is removed. This avoids a long-lived
//     plaintext SQL file readable by anyone with FilesDir access.
//   - Gzip (not zstd) keeps the dependency surface stdlib-only. Snapshot
//     speed for multi-GB DBs is the place to revisit later — see
//     LEARNINGS.md §4.1.
//   - Engine + version are encoded in the filename so RestoreSnapshot
//     can reject mismatched restores (LEARNINGS.md F18, §4.6).
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
		return "", errors.New("site must be running to snapshot")
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

	token, err := importToken()
	if err != nil {
		return "", err
	}
	stagingFilename := ".locorum-snapshot-" + token + ".sql"
	hostStagingPath := filepath.Join(site.FilesDir, stagingFilename)
	containerStagingPath := "/var/www/html/" + stagingFilename
	cleanupStaging := func() {
		if err := os.Remove(hostStagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("snapshot: staging cleanup failed", "path", hostStagingPath, "err", err.Error())
		}
	}
	defer cleanupStaging()

	if _, err := sm.wpcli(ctx, site,
		"db", "export", containerStagingPath,
		"--add-drop-table",
		"--default-character-set=utf8mb4",
	); err != nil {
		return "", fmt.Errorf("wp db export: %w", err)
	}

	finalName := buildSnapshotName(site.Slug, label, "mysql", site.MySQLVersion, time.Now().UTC())
	finalPath := filepath.Join(dir, finalName)

	if err := gzipMoveAtomic(hostStagingPath, finalPath); err != nil {
		return "", fmt.Errorf("compress snapshot: %w", err)
	}

	slog.Info("snapshot: created", "site", site.Slug, "path", finalPath)

	if err := sm.runHooks(ctx, hooks.PostSnapshot, site); err != nil {
		// Snapshot is already on disk — log the post-hook failure and
		// return success. The user has their backup; the post-hook is
		// observability-only by convention.
		slog.Warn("post-snapshot hook failed", "err", err.Error())
	}
	return finalPath, nil
}

// snapshotSep is the segment separator used between {slug, label, ts,
// engineVersion}. Double-dash is unambiguous because:
//   - Slugs (from gosimple/slug) only ever contain single hyphens.
//   - Labels are validated against snapshotLabelPat which forbids `-`.
//   - The timestamp uses [0-9TZ] only.
//   - Engine+version are joined with a single `-`.
const snapshotSep = "--"

// buildSnapshotName assembles a deterministic filename. Format:
//
//	{slug}--{label}--{tsUTC}--{engine}-{version}.sql.gz
//
// All fields are sanitised so a hostile / unusual value can't escape the
// filename via path separators or shell metacharacters.
func buildSnapshotName(slug, label, engine, version string, ts time.Time) string {
	engine = strings.ToLower(snapshotEnginePat.ReplaceAllString(strings.ToLower(engine), ""))
	if engine == "" {
		engine = "mysql"
	}
	version = snapshotVersionPat.ReplaceAllString(version, "")
	if version == "" {
		version = "unknown"
	}
	return strings.Join([]string{
		slug,
		label,
		ts.Format(snapshotTSFormat),
		engine + "-" + version,
	}, snapshotSep) + ".sql.gz"
}

// parseSnapshotName turns a filename back into a SnapshotInfo (without
// HostPath / SizeBytes — caller fills those after stat). Returns nil for
// unrecognised filenames; the caller silently ignores them so a stray
// file in the snapshots dir doesn't break listings.
func parseSnapshotName(name string) *SnapshotInfo {
	if !strings.HasSuffix(name, ".sql.gz") {
		return nil
	}
	stem := strings.TrimSuffix(name, ".sql.gz")
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
	// Engine-version split on the LAST dash before any digit, matching
	// the build-side format `{engine}-{version}` where engine is
	// alphabetic and version starts with a digit.
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
		Compression: "gzip",
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
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// RestoreSnapshot reconstructs the database from a snapshot. The site
// must be running. Engine compatibility is checked against the filename
// metadata: a snapshot tagged `mysql8.0` will refuse to restore into a
// `mysql5.7` site (LEARNINGS.md F18). The user can opt out with
// AllowEngineMismatch=true if they know what they're doing.
type RestoreSnapshotOptions struct {
	AllowEngineMismatch bool
}

func (sm *SiteManager) RestoreSnapshot(ctx context.Context, siteID, snapshotPath string, opts RestoreSnapshotOptions) error {
	site, err := sm.st.GetSite(siteID)
	if err != nil {
		return fmt.Errorf("fetching site: %w", err)
	}
	if site == nil {
		return fmt.Errorf("site %q not found", siteID)
	}
	if !site.Started {
		return errors.New("site must be running to restore")
	}
	info := parseSnapshotName(filepath.Base(snapshotPath))
	if info != nil && !opts.AllowEngineMismatch {
		if info.Version != "" && site.MySQLVersion != "" && info.Version != site.MySQLVersion {
			return fmt.Errorf("snapshot engine %s%s does not match site engine mysql%s — pass AllowEngineMismatch to override", info.Engine, info.Version, site.MySQLVersion)
		}
	}

	mu := sm.siteMutex(siteID)
	mu.Lock()
	defer mu.Unlock()

	token, err := importToken()
	if err != nil {
		return err
	}
	stagingFilename := ".locorum-restore-" + token + ".sql"
	hostStagingPath := filepath.Join(site.FilesDir, stagingFilename)
	containerStagingPath := "/var/www/html/" + stagingFilename
	defer func() {
		if err := os.Remove(hostStagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("restore: staging cleanup failed", "err", err.Error())
		}
	}()

	if err := gunzipTo(snapshotPath, hostStagingPath); err != nil {
		return fmt.Errorf("decompress: %w", err)
	}

	if _, err := sm.wpDBImport(ctx, site, containerStagingPath); err != nil {
		return fmt.Errorf("wp db import: %w", err)
	}
	slog.Info("restore: complete", "site", site.Slug, "from", snapshotPath)
	return nil
}

// gzipMoveAtomic compresses src to dst+".tmp.gz" and renames into place
// after fsync. Removes src on success. Atomic: a partial dst is never
// visible to ListSnapshots.
func gzipMoveAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".locorum-snap-*.gz")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanupTmp := func() { _ = os.Remove(tmpName) }

	gw, err := gzip.NewWriterLevel(tmp, gzip.BestSpeed)
	if err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("gzip writer: %w", err)
	}
	if _, err := io.Copy(gw, in); err != nil {
		_ = gw.Close()
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("compress: %w", err)
	}
	if err := gw.Close(); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("close gzip: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTmp()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		cleanupTmp()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		cleanupTmp()
		return fmt.Errorf("rename: %w", err)
	}
	if err := os.Remove(src); err != nil {
		// Failure to remove src is non-fatal — the snapshot is on disk;
		// the staging file just lingers until the next snapshot or a
		// manual cleanup. Log and continue.
		slog.Warn("snapshot: failed to remove staging file", "path", src, "err", err.Error())
	}
	return nil
}

// gunzipTo decompresses src.gz to dst atomically.
func gunzipTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()
	gr, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".locorum-restore-*.sql")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpName := tmp.Name()
	cleanupTmp := func() { _ = os.Remove(tmpName) }
	if _, err := io.Copy(tmp, gr); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("decompress: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanupTmp()
		return fmt.Errorf("sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanupTmp()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		cleanupTmp()
		return fmt.Errorf("chmod: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		cleanupTmp()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
