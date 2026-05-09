package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "modernc.org/sqlite"
)

// At every version V: up→down→up must produce a byte-stable schema.
func TestMigrations_ForwardRoundTrip(t *testing.T) {
	versions := migrationVersions(t)
	if len(versions) == 0 {
		t.Skip("no migrations embedded")
	}

	for _, v := range versions {
		t.Run(fmt.Sprintf("v=%d", v), func(t *testing.T) {
			t.Parallel()
			db := openTempDB(t)
			defer db.Close()

			m := newMigrator(t, db)
			if err := m.Migrate(v); err != nil {
				t.Fatalf("migrate up to %d: %v", v, err)
			}
			schemaAtUp := dumpSchema(t, db)

			if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				t.Fatalf("migrate down: %v", err)
			}
			if err := m.Migrate(v); err != nil {
				t.Fatalf("migrate up to %d (second pass): %v", v, err)
			}
			schemaAfterRoundtrip := dumpSchema(t, db)

			if schemaAtUp != schemaAfterRoundtrip {
				t.Fatalf("schema not stable across down+up roundtrip:\nfirst:\n%s\nsecond:\n%s",
					schemaAtUp, schemaAfterRoundtrip)
			}
		})
	}
}

// Catches the "down migration references a column an earlier migration
// removed" footgun.
func TestMigrations_FullUpThenFullDown(t *testing.T) {
	db := openTempDB(t)
	defer db.Close()

	m := newMigrator(t, db)
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("Up: %v", err)
	}
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("Down: %v", err)
	}
	// And one more forward pass — proves the chain is idempotent across
	// up→down→up.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("Up after Down: %v", err)
	}
}

// migrationVersions returns the numeric prefix of every up.sql in the
// embedded migrations dir, sorted ascending. golang-migrate uses the
// prefix directly as the version number.
func migrationVersions(t *testing.T) []uint {
	t.Helper()
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	var versions []uint
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		i := strings.IndexByte(name, '_')
		if i <= 0 {
			t.Fatalf("malformed migration filename %q", name)
		}
		var v uint
		if _, err := fmt.Sscanf(name[:i], "%d", &v); err != nil {
			t.Fatalf("parse version from %q: %v", name, err)
		}
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions
}

// On-disk rather than :memory: because golang-migrate's sqlite3 driver
// branches on path, and :memory: pool connections diverge.
func openTempDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "file:" + path.Join(t.TempDir(), "mig.db") + "?_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func newMigrator(t *testing.T, db *sql.DB) *migrate.Migrate {
	t.Helper()
	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		t.Fatalf("driver: %v", err)
	}
	src, err := iofs.New(migrations, "migrations")
	if err != nil {
		t.Fatalf("iofs source: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		t.Fatalf("migrator: %v", err)
	}
	t.Cleanup(func() {
		_, _ = m.Close()
	})
	return m
}

func dumpSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, sql FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' AND name NOT LIKE 'schema_migrations%' ORDER BY type, name`)
	if err != nil {
		t.Fatalf("dump schema: %v", err)
	}
	defer rows.Close()

	var lines []string
	for rows.Next() {
		var typ, name, ddl sql.NullString
		if err := rows.Scan(&typ, &name, &ddl); err != nil {
			t.Fatalf("scan: %v", err)
		}
		lines = append(lines, fmt.Sprintf("%s %s\n%s", typ.String, name.String, normaliseDDL(ddl.String)))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n---\n")
}

// Normalises DDL whitespace so a roundtrip that reflows blanks doesn't
// trip the equality check.
func normaliseDDL(s string) string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		out = append(out, strings.TrimRight(line, " \t"))
	}
	joined := strings.Join(out, "\n")
	for strings.Contains(joined, "\n\n\n") {
		joined = strings.ReplaceAll(joined, "\n\n\n", "\n\n")
	}
	return joined
}
