package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"

	"github.com/PeterBooker/locorum/internal/types"
)

type Storage struct {
	db  *sql.DB
	ctx context.Context
}

// NewSQLiteStorage opens (or creates) the SQLite DB located in the Wails AppData folder,
// ensures the sites table exists, and returns a Storage instance.
func NewSQLiteStorage(ctx context.Context) (*Storage, error) {
	cwd, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	appDataDir := filepath.Join(cwd, ".locorum")
	dbPath := filepath.Join(appDataDir, "storage.db")

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Create table if it doesn't exist
	const schema = `
	CREATE TABLE IF NOT EXISTS sites (
		id   TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		slug  TEXT NOT NULL,
		domain  TEXT NOT NULL
	);`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	return &Storage{db: db, ctx: ctx}, nil
}

// Close the database when shutting down
func (s *Storage) Close() error {
	return s.db.Close()
}

// GetSites returns all sites stored in SQLite.
func (s *Storage) GetSites() ([]types.Site, error) {
	rows, err := s.db.Query("SELECT id, name, slug, domain FROM sites")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []types.Site
	for rows.Next() {
		var site types.Site
		if err := rows.Scan(&site.ID, &site.Name, &site.Slug, &site.Domain); err != nil {
			return nil, err
		}
		result = append(result, site)
	}
	return result, rows.Err()
}

// GetSite returns a single Site by ID.
func (s *Storage) GetSite(id string) (*types.Site, error) {
	row := s.db.QueryRow("SELECT id, name, slug, domain FROM sites WHERE id = ?", id)
	var site types.Site

	if err := row.Scan(&site.ID, &site.Name, &site.Slug, &site.Domain); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}

		return nil, err
	}

	return &site, nil
}

// AddSite inserts a new Site into the database, generating an ID if none is set.
func (s *Storage) AddSite(site *types.Site) error {
	if site.ID == "" {
		site.ID = uuid.NewString()
	}
	_, err := s.db.Exec(
		"INSERT INTO sites (id, name, slug, domain) VALUES (?, ?, ?, ?)",
		site.ID, site.Name, site.Slug, site.Domain,
	)
	return err
}

// DeleteSite removes the Site with the given ID from the database.
func (s *Storage) DeleteSite(id string) error {
	_, err := s.db.Exec("DELETE FROM sites WHERE id = ?", id)
	return err
}
