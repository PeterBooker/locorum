package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

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

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}

	// _pragma=foreign_keys(1) enables FK enforcement on every new connection
	// in modernc.org/sqlite's pool. Required for ON DELETE CASCADE.
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}

	if err := applyMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
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
const siteColumns = "id, name, slug, domain, filesDir, publicDir, started, phpVersion, mysqlVersion, redisVersion, dbPassword, webServer, multisite, salts, dbEngine, dbVersion, publishDBPort, createdAt, updatedAt"

// scanSite hydrates a Site from a row scanner. Centralised so GetSite and
// GetSites stay in lockstep with siteColumns; a missed field here means
// every caller is half-broken.
func scanSite(scan func(...any) error) (*types.Site, error) {
	var site types.Site
	if err := scan(
		&site.ID, &site.Name, &site.Slug, &site.Domain,
		&site.FilesDir, &site.PublicDir, &site.Started,
		&site.PHPVersion, &site.MySQLVersion, &site.RedisVersion, &site.DBPassword,
		&site.WebServer, &site.Multisite, &site.Salts,
		&site.DBEngine, &site.DBVersion, &site.PublishDBPort,
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
		site.DBVersion = site.MySQLVersion
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
		if err == sql.ErrNoRows {
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
	if site.MySQLVersion == "" && site.DBEngine == "mysql" {
		site.MySQLVersion = site.DBVersion
	}

	_, err := s.db.Exec(
		"INSERT INTO sites ("+siteColumns+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		site.ID, site.Name, site.Slug, site.Domain, site.FilesDir, site.PublicDir, site.Started,
		site.PHPVersion, site.MySQLVersion, site.RedisVersion, site.DBPassword,
		site.WebServer, site.Multisite, site.Salts,
		site.DBEngine, site.DBVersion, boolToInt(site.PublishDBPort),
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

	if site.MySQLVersion == "" && site.DBEngine == "mysql" {
		site.MySQLVersion = site.DBVersion
	}

	_, err := s.db.Exec(
		"UPDATE sites SET name = ?, slug = ?, domain = ?, filesDir = ?, publicDir = ?, started = ?, phpVersion = ?, mysqlVersion = ?, redisVersion = ?, dbPassword = ?, webServer = ?, multisite = ?, salts = ?, dbEngine = ?, dbVersion = ?, publishDBPort = ?, updatedAt = ? WHERE id = ?",
		site.Name, site.Slug, site.Domain, site.FilesDir, site.PublicDir, site.Started,
		site.PHPVersion, site.MySQLVersion, site.RedisVersion, site.DBPassword,
		site.WebServer, site.Multisite, site.Salts,
		site.DBEngine, site.DBVersion, boolToInt(site.PublishDBPort),
		site.UpdatedAt, site.ID,
	)
	if err != nil {
		return nil, err
	}

	return site, nil
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
