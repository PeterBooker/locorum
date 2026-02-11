package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

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

	db, err := sql.Open("sqlite", dbPath)
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

// GetSites returns all sites stored in SQLite.
func (s *Storage) GetSites() ([]types.Site, error) {
	rows, err := s.db.Query("SELECT id, name, slug, domain, filesDir, publicDir, started, phpVersion, mysqlVersion, redisVersion FROM sites")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []types.Site
	for rows.Next() {
		var site types.Site
		if err := rows.Scan(&site.ID, &site.Name, &site.Slug, &site.Domain, &site.FilesDir, &site.PublicDir, &site.Started, &site.PHPVersion, &site.MySQLVersion, &site.RedisVersion); err != nil {
			return nil, err
		}
		result = append(result, site)
	}

	return result, rows.Err()
}

// GetSite returns a single Site by ID.
func (s *Storage) GetSite(id string) (*types.Site, error) {
	row := s.db.QueryRow("SELECT id, name, slug, domain, filesDir, publicDir, started, phpVersion, mysqlVersion, redisVersion FROM sites WHERE id = ?", id)
	var site types.Site

	if err := row.Scan(&site.ID, &site.Name, &site.Slug, &site.Domain, &site.FilesDir, &site.PublicDir, &site.Started, &site.PHPVersion, &site.MySQLVersion, &site.RedisVersion); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}

		return nil, err
	}

	return &site, nil
}

// AddSite inserts a new Site into the database, generating an ID if none is set.
func (s *Storage) AddSite(site *types.Site) error {
	_, err := s.db.Exec(
		"INSERT INTO sites (id, name, slug, domain, filesDir, publicDir, started, phpVersion, mysqlVersion, redisVersion) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		site.ID, site.Name, site.Slug, site.Domain, site.FilesDir, site.PublicDir, site.Started, site.PHPVersion, site.MySQLVersion, site.RedisVersion,
	)
	if err != nil {
		return err
	}

	return nil
}

// UpdateSite updates an existing Site in the database.
func (s *Storage) UpdateSite(site *types.Site) (*types.Site, error) {
	_, err := s.db.Exec(
		"UPDATE sites SET name = ?, slug = ?, domain = ?, filesDir = ?, publicDir = ?, started = ?, phpVersion = ?, mysqlVersion = ?, redisVersion = ? WHERE id = ?",
		site.Name, site.Slug, site.Domain, site.FilesDir, site.PublicDir, site.Started, site.PHPVersion, site.MySQLVersion, site.RedisVersion, site.ID,
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
