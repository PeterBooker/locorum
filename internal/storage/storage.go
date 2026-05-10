package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// register sqlite driver
	_ "modernc.org/sqlite"

	"github.com/PeterBooker/locorum/internal/types"
)

type Storage struct {
	db  *sql.DB
	ctx context.Context
}

// NewSQLiteStorage opens (or creates) the SQLite DB located in ~/.locorum/,
// applies migrations, and returns a Storage instance.
func NewSQLiteStorage(ctx context.Context) (*Storage, error) {
	cwd, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	appDataDir := filepath.Join(cwd, ".locorum")
	dbPath := filepath.Join(appDataDir, "storage.db")

	// 0o700: ~/.locorum holds DB passwords, WP salts, mkcert keys, and the
	// MCP bearer token. Anyone with read access to this tree can pivot to
	// every site's wp-admin and the local DB. Tighten regardless of umask.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, err
	}
	// MkdirAll is a no-op if the dir already exists with looser bits, so
	// re-chmod explicitly. Errors are non-fatal on platforms where chmod is
	// a no-op (Windows) but logged for diagnostics.
	if err := os.Chmod(appDataDir, 0o700); err != nil && !os.IsNotExist(err) {
		// Soft-fail: continuing on chmod errors is safer than refusing to
		// open the DB at all on filesystems that don't honour Unix bits.
		_ = err
	}

	// SQLite DSN tuned for an embedded, write-heavy desktop workload where
	// concurrent goroutines (orchestrator, activity log, hook listings,
	// background tasks) hit the same DB file. Without these pragmas, the
	// default rollback journal serialises all access through one mutex and
	// every concurrent write returns SQLITE_BUSY (5) immediately.
	//
	//   foreign_keys(1)        — enable FK enforcement on every connection
	//                            (required for our ON DELETE CASCADE rules).
	//   journal_mode(WAL)      — write-ahead log: readers and a single writer
	//                            never block each other. WAL state is
	//                            persistent in the DB header, so applying it
	//                            on every connection is idempotent.
	//   busy_timeout(5000)     — when a writer collides with another writer,
	//                            wait up to 5s for the lock instead of
	//                            failing the call. 5s is well above any
	//                            transaction we issue and never user-visible.
	//   synchronous(NORMAL)    — safe with WAL (durability boundary moves
	//                            from per-commit to per-checkpoint). Cuts
	//                            fsync count by an order of magnitude.
	//   _txlock=immediate      — every BEGIN takes a write lock up front
	//                            instead of upgrading on first INSERT,
	//                            removing the read-then-write deadlock that
	//                            happens when two transactions both hold a
	//                            shared lock and try to upgrade.
	dsn := "file:" + dbPath +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// Belt-and-braces against SQLITE_BUSY: cap the writer pool at 1
	// connection so the application enforces single-writer semantics
	// regardless of WAL state. Reads inside transactions reuse the same
	// connection; cross-goroutine reads queue behind any in-flight write,
	// which is exactly the property the activity log + hook listing
	// callers were silently relying on. Locorum's working set is small
	// enough that the loss of read parallelism is invisible.
	db.SetMaxOpenConns(1)
	// Match idle to open: avoid the pool churning connections, which
	// in WAL mode means re-applying the journal_mode pragma on every
	// open, which itself takes a brief write lock.
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	// SQLite creates the file lazily on first write. Force the mode after
	// migrations have created it so the file isn't world-readable for the
	// (small) window between sql.Open and the first migration query.
	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil && !os.IsNotExist(err) {
		_ = err
	}

	return &Storage{db: db, ctx: ctx}, nil
}

// Close the database when shutting down
func (s *Storage) Close() error {
	return s.db.Close()
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// siteColumns is the canonical column list for sites SELECT/INSERT/UPDATE.
// Keep ordering aligned with the Scan / Exec arg order below — adding a
// column means editing four call sites; the constant centralises the
// SELECT/INSERT lists so two of those four stay in lockstep.
const siteColumns = "id, name, slug, domain, filesDir, publicDir, started, phpVersion, mysqlVersion, redisVersion, dbPassword, webServer, multisite, salts, dbEngine, dbVersion, publishDBPort, spxEnabled, spxKey, gitRemote, gitBranch, worktreePath, parentSiteID, createdAt, updatedAt"

// scanSite hydrates a Site from a row scanner. Centralised so GetSite and
// GetSites stay in lockstep with siteColumns; a missed field here means
// every caller is half-broken.
func scanSite(scan func(...any) error) (*types.Site, error) {
	var site types.Site
	if err := scan(
		&site.ID, &site.Name, &site.Slug, &site.Domain,
		&site.FilesDir, &site.PublicDir, &site.Started,
		//nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
		&site.PHPVersion, &site.MySQLVersion, &site.RedisVersion, &site.DBPassword,
		&site.WebServer, &site.Multisite, &site.Salts,
		&site.DBEngine, &site.DBVersion, &site.PublishDBPort,
		&site.SPXEnabled, &site.SPXKey,
		&site.GitRemote, &site.GitBranch, &site.WorktreePath, &site.ParentSiteID,
		&site.CreatedAt, &site.UpdatedAt,
	); err != nil {
		return nil, err
	}
	hydrateLegacyDBFields(&site)
	return &site, nil
}

// hydrateLegacyDBFields fills DBEngine / DBVersion for rows written
// before the multi-engine migration. The migration's UPDATE handles
// existing rows on first apply; this helper covers the corner case where
// a manual SQL edit zeroed dbEngine, or a future migration runs while
// some rows still carry only mysqlVersion.
func hydrateLegacyDBFields(site *types.Site) {
	if site.DBEngine == "" {
		site.DBEngine = "mysql"
	}
	if site.DBVersion == "" {
		site.DBVersion = site.MySQLVersion //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
	}
}

// GetSites returns all sites stored in SQLite.
func (s *Storage) GetSites() ([]types.Site, error) {
	rows, err := s.db.Query("SELECT " + siteColumns + " FROM sites")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []types.Site
	for rows.Next() {
		site, err := scanSite(rows.Scan)
		if err != nil {
			return nil, err
		}
		result = append(result, *site)
	}

	return result, rows.Err()
}

// GetSite returns a single Site by ID.
func (s *Storage) GetSite(id string) (*types.Site, error) {
	row := s.db.QueryRow("SELECT "+siteColumns+" FROM sites WHERE id = ?", id)
	site, err := scanSite(row.Scan)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return site, nil
}

// AddSite inserts a new Site into the database, generating an ID if none is set.
func (s *Storage) AddSite(site *types.Site) error {
	ts := now()
	site.CreatedAt = ts
	site.UpdatedAt = ts

	// Mirror the legacy column for one minor release so external
	// tooling that reads mysqlVersion directly keeps working.
	if site.MySQLVersion == "" && site.DBEngine == "mysql" { //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
		site.MySQLVersion = site.DBVersion //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
	}

	_, err := s.db.Exec(
		"INSERT INTO sites ("+siteColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		site.ID, site.Name, site.Slug, site.Domain, site.FilesDir, site.PublicDir, site.Started,
		//nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
		site.PHPVersion, site.MySQLVersion, site.RedisVersion, site.DBPassword,
		site.WebServer, site.Multisite, site.Salts,
		site.DBEngine, site.DBVersion, boolToInt(site.PublishDBPort),
		boolToInt(site.SPXEnabled), site.SPXKey,
		site.GitRemote, site.GitBranch, site.WorktreePath, site.ParentSiteID,
		site.CreatedAt, site.UpdatedAt,
	)
	if err != nil {
		return err
	}

	return nil
}

// UpdateSite updates an existing Site in the database.
func (s *Storage) UpdateSite(site *types.Site) (*types.Site, error) {
	site.UpdatedAt = now()

	if site.MySQLVersion == "" && site.DBEngine == "mysql" { //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
		site.MySQLVersion = site.DBVersion //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
	}

	_, err := s.db.Exec(
		"UPDATE sites SET name = ?, slug = ?, domain = ?, filesDir = ?, publicDir = ?, started = ?, phpVersion = ?, mysqlVersion = ?, redisVersion = ?, dbPassword = ?, webServer = ?, multisite = ?, salts = ?, dbEngine = ?, dbVersion = ?, publishDBPort = ?, spxEnabled = ?, spxKey = ?, gitRemote = ?, gitBranch = ?, worktreePath = ?, parentSiteID = ?, updatedAt = ? WHERE id = ?",
		site.Name, site.Slug, site.Domain, site.FilesDir, site.PublicDir, site.Started,
		//nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
		site.PHPVersion, site.MySQLVersion, site.RedisVersion, site.DBPassword,
		site.WebServer, site.Multisite, site.Salts,
		site.DBEngine, site.DBVersion, boolToInt(site.PublishDBPort),
		boolToInt(site.SPXEnabled), site.SPXKey,
		site.GitRemote, site.GitBranch, site.WorktreePath, site.ParentSiteID,
		site.UpdatedAt, site.ID,
	)
	if err != nil {
		return nil, err
	}

	return site, nil
}

// SitesByParent returns every worktree-bound site whose ParentSiteID
// matches parentID. Used by DeleteSite to cascade-clean its workers
// before removing the parent row.
func (s *Storage) SitesByParent(parentID string) ([]types.Site, error) {
	if parentID == "" {
		return nil, nil
	}
	rows, err := s.db.Query("SELECT "+siteColumns+" FROM sites WHERE parentSiteID = ?", parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []types.Site
	for rows.Next() {
		site, err := scanSite(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *site)
	}
	return out, rows.Err()
}

// DeleteSite removes the Site with the given ID from the database.
func (s *Storage) DeleteSite(id string) error {
	_, err := s.db.Exec("DELETE FROM sites WHERE id = ?", id)
	if err != nil {
		return err
	}

	return nil
}

// GetSetting returns the setting value for a key, or "" if not set.
func (s *Storage) GetSetting(key string) (string, error) {
	row := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key)
	var v string
	if err := row.Scan(&v); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// SetSetting upserts a key/value pair into the settings table.
func (s *Storage) SetSetting(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	)
	return err
}
