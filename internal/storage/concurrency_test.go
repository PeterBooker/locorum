package storage

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	// register sqlite driver
	_ "modernc.org/sqlite"

	"github.com/PeterBooker/locorum/internal/hooks"
	"github.com/PeterBooker/locorum/internal/types"
)

// openOnDiskStorage opens a real on-disk SQLite DB with the production
// DSN — the same pragmas NewSQLiteStorage applies. Used to guard against
// SQLITE_BUSY in the same conditions the user hit during StartSite.
func openOnDiskStorage(t testing.TB) *Storage {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "storage.db")
	dsn := "file:" + dbPath +
		"?_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Storage{db: db}
}

// TestConcurrentWritesDoNotReturnBusy reproduces the user-facing failure
// mode: multiple goroutines writing to the DB during StartSite (site
// state update, activity-log append, hook reads) collided with
// SQLITE_BUSY because the previous DSN had no WAL and no busy_timeout.
//
// With the production DSN + MaxOpenConns(1) + WAL + busy_timeout, all
// writes must succeed regardless of contention.
func TestConcurrentWritesDoNotReturnBusy(t *testing.T) {
	st := openOnDiskStorage(t)

	site := &types.Site{
		ID:         "00000000-0000-0000-0000-000000000001",
		Name:       "x",
		Slug:       "x",
		Domain:     "x.localhost",
		FilesDir:   "/tmp/x",
		PHPVersion: "8.4",
		DBEngine:   "mysql",
		DBVersion:  "8.4",
		WebServer:  "nginx",
	}
	if err := st.AddSite(site); err != nil {
		t.Fatalf("seed AddSite: %v", err)
	}

	siteID := site.ID
	const goroutines = 16
	const opsPerG = 32

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines*opsPerG)

	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < opsPerG; i++ {
				// Mix of writes and reads — the same pattern StartSite
				// produces (UpdateSite + AppendActivity + ListHooks).
				// Each goroutine builds its own structs so the
				// concurrency we're stressing is at the DB layer, not
				// in shared in-memory state.
				switch i % 3 {
				case 0:
					localSite := *site
					if _, err := st.UpdateSite(&localSite); err != nil {
						errs <- err
					}
				case 1:
					ev := &ActivityEvent{
						SiteID: siteID,
						Time:   time.Now().UTC(),
						Plan:   "concurrency-test",
						Kind:   ActivityKindStart,
						Status: ActivityStatusSucceeded,
					}
					if err := st.AppendActivity(ev); err != nil {
						errs <- err
					}
				case 2:
					if _, err := st.ListHooksByEvent(siteID, hooks.PostStart); err != nil {
						errs <- err
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent op failed (this likely means WAL/busy_timeout/MaxOpenConns regressed): %v", err)
	}
}
