package storage

import (
	"database/sql"
	"testing"

	// register sqlite driver
	_ "modernc.org/sqlite"
)

// NewTestStorage returns a Storage backed by a fresh in-memory SQLite
// database with all migrations applied. Each call returns an isolated DB —
// suitable for tests in any package that needs to exercise the storage
// layer without touching disk.
//
// MaxOpenConns is pinned to 1 because :memory: is per-connection: any new
// connection from the pool would see an empty database. The cleanup hook
// is registered against t so the DB is closed when the test finishes.
func NewTestStorage(t testing.TB) *Storage {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open in-memory storage: %v", err)
	}
	db.SetMaxOpenConns(1)
	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		t.Fatalf("apply migrations: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Storage{db: db}
}
