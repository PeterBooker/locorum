---
name: add-migration
description: Add a new SQLite migration to Locorum — use when the user asks to add a column, alter the sites schema, add a new table, or create a migration file
user-invocable: true
---

# Add a Migration

Locorum uses `golang-migrate` with migrations embedded from `internal/storage/migrations/`. Each migration is a pair of `.up.sql` / `.down.sql` files ordered by a `YYYYMMDDHHMMSS_` prefix.

## Steps

### 1. Create the migration files

Filename pattern — both files must share the prefix and description:

```
internal/storage/migrations/YYYYMMDDHHMMSS_short_description.up.sql
internal/storage/migrations/YYYYMMDDHHMMSS_short_description.down.sql
```

Use the current UTC date-time as the prefix (run `date -u +%Y%m%d%H%M%S`). `golang-migrate` orders strictly lexicographically, so the prefix must sort *after* all existing migrations in `internal/storage/migrations/`.

Existing migrations to check before choosing a prefix:
```bash
ls internal/storage/migrations/
```

### 2. Write the SQL

For a simple column add:

```sql
-- up
ALTER TABLE sites ADD COLUMN new_field TEXT NOT NULL DEFAULT '';
```

For the down:

```sql
-- SQLite doesn't support DROP COLUMN in older versions; recreate the table.
CREATE TABLE sites_backup AS SELECT id, name, slug, domain, filesDir, publicDir, started, phpVersion, mysqlVersion, redisVersion, dbPassword, webServer, multisite, createdAt, updatedAt FROM sites;
DROP TABLE sites;
ALTER TABLE sites_backup RENAME TO sites;
```

(List the columns you want to keep — excluding the one being dropped.)

### 3. Update the Go model

`internal/types/types.go` — add the field to `Site` with a JSON tag:

```go
type Site struct {
    ...
    NewField string `json:"newField"`
    ...
}
```

### 4. Update all four SQL statements in storage

`internal/storage/storage.go` has four queries that reference every column. You must update all of them:

- `GetSites()` — SELECT column list + `rows.Scan(...)` destinations
- `GetSite(id)` — SELECT column list + `row.Scan(...)` destinations
- `AddSite(site)` — INSERT column list, VALUES placeholders (count must match), and `s.db.Exec(...)` args
- `UpdateSite(site)` — UPDATE `SET ... = ?` list and `s.db.Exec(...)` args (in same order)

The column count and the number of `?` placeholders must match the number of `Scan`/`Exec` args exactly.

### 5. Add test coverage

If the field has semantic meaning (not just a nullable string), add a test case in `internal/storage/storage_test.go`:

```go
site := &types.Site{
    ID: "id-1",
    ...
    NewField: "expected",
}
st.AddSite(site)
got, _ := st.GetSite("id-1")
if got.NewField != "expected" {
    t.Errorf("NewField = %q, want %q", got.NewField, "expected")
}
```

Tests run against in-memory SQLite, so migrations execute during the test setup — this verifies the migration itself.

### 6. Wire through the stack (if user-facing)

If this field is set/edited by the user:

- **Business logic:** `internal/sites/sites.go` — add setter on `SiteManager` (e.g. `UpdateXxx(siteID, val string) error`), follow existing patterns (`UpdatePublicDir`, `UpdateSiteVersions`).
- **UI state:** `internal/ui/state.go` if the field needs transient UI state (loading flag, modal state).
- **UI rendering:** a new component file in `internal/ui/` or a field added to `sitedetail.go`.
- **Docker:** if the field changes container config (image version, env var), update the relevant `addXxxContainer` in `internal/docker/docker.go` and ensure `UpdateSiteVersions`-style code removes the old container so it gets recreated with the new config.

### 7. Verify

```bash
go vet ./...
go test ./...
go run .      # Launches the app — migration applies on startup.
```

A migration failure on startup will print `migration up error: ...` via slog and the app will refuse to proceed.

## Gotchas

- **Migrations are immutable after merge.** If you need to change a shipped migration, write a *new* migration that fixes the state. Editing a shipped up.sql will desync users whose DB already ran the old version.
- **Default values are important.** If adding a `NOT NULL` column, provide a `DEFAULT` so existing rows are valid.
- **SQLite column types are loose.** `TEXT`, `INTEGER`, `BOOLEAN` all accept string values — stick to the types used in `20250526132642_create_sites_table.up.sql` for consistency.
- **Order matters in `Scan`/`Exec`.** The column list in the SQL and the arg list in Go must be in the same order. A mismatch compiles but silently corrupts data.
- **Down migrations are rarely run in practice** but `golang-migrate` requires them — write a working one anyway.
