package storage

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/PeterBooker/locorum/internal/types"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := applyMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newStorage(t *testing.T) *Storage {
	t.Helper()
	return &Storage{db: setupTestDB(t)}
}

func TestAddAndGetSite(t *testing.T) {
	st := newStorage(t)

	site := &types.Site{
		ID:           "test-id-1",
		Name:         "My Site",
		Slug:         "my-site",
		Domain:       "my-site.localhost",
		FilesDir:     "/tmp/sites/my-site",
		PublicDir:    "/",
		Started:      false,
		PHPVersion:   "8.3",
		MySQLVersion: "8.0",
		RedisVersion: "7",
		DBPassword:   "secret123",
	}

	if err := st.AddSite(site); err != nil {
		t.Fatalf("AddSite() error = %v", err)
	}

	if site.CreatedAt == "" {
		t.Fatal("expected CreatedAt to be set")
	}
	if site.UpdatedAt == "" {
		t.Fatal("expected UpdatedAt to be set")
	}

	got, err := st.GetSite("test-id-1")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got == nil {
		t.Fatal("expected site, got nil")
	}
	if got.Name != "My Site" {
		t.Errorf("Name = %q, want %q", got.Name, "My Site")
	}
	if got.DBPassword != "secret123" {
		t.Errorf("DBPassword = %q, want %q", got.DBPassword, "secret123")
	}
}

func TestGetSites(t *testing.T) {
	st := newStorage(t)

	s1 := &types.Site{ID: "id-1", Name: "Site 1", Slug: "site-1", Domain: "site-1.localhost", FilesDir: "/tmp/1", PublicDir: "/", DBPassword: "pw1"}
	s2 := &types.Site{ID: "id-2", Name: "Site 2", Slug: "site-2", Domain: "site-2.localhost", FilesDir: "/tmp/2", PublicDir: "/", DBPassword: "pw2"}

	st.AddSite(s1)
	st.AddSite(s2)

	sites, err := st.GetSites()
	if err != nil {
		t.Fatalf("GetSites() error = %v", err)
	}
	if len(sites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(sites))
	}
}

func TestUpdateSite(t *testing.T) {
	st := newStorage(t)

	site := &types.Site{ID: "id-1", Name: "Original", Slug: "original", Domain: "original.localhost", FilesDir: "/tmp/o", PublicDir: "/", DBPassword: "pw"}
	st.AddSite(site)

	site.Name = "Updated"
	updated, err := st.UpdateSite(site)
	if err != nil {
		t.Fatalf("UpdateSite() error = %v", err)
	}
	if updated.Name != "Updated" {
		t.Errorf("Name = %q, want %q", updated.Name, "Updated")
	}

	got, _ := st.GetSite("id-1")
	if got.Name != "Updated" {
		t.Errorf("persisted Name = %q, want %q", got.Name, "Updated")
	}
}

func TestDeleteSite(t *testing.T) {
	st := newStorage(t)

	site := &types.Site{ID: "id-1", Name: "ToDelete", Slug: "todelete", Domain: "todelete.localhost", FilesDir: "/tmp/d", PublicDir: "/", DBPassword: "pw"}
	st.AddSite(site)

	if err := st.DeleteSite("id-1"); err != nil {
		t.Fatalf("DeleteSite() error = %v", err)
	}

	got, err := st.GetSite("id-1")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestGetSite_NotFound(t *testing.T) {
	st := newStorage(t)

	got, err := st.GetSite("nonexistent")
	if err != nil {
		t.Fatalf("GetSite() error = %v", err)
	}
	if got != nil {
		t.Fatal("expected nil for nonexistent site")
	}
}
