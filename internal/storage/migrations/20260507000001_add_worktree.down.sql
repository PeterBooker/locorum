-- SQLite's DROP COLUMN is unreliable across the supported version
-- range; recreate the table without the worktree columns. Mirrors the
-- pattern used by 20260506000001_add_spx.down.sql.
DROP INDEX IF EXISTS idx_sites_parent;
CREATE TABLE sites_backup AS
  SELECT id, name, slug, domain, filesDir, publicDir, started,
         phpVersion, mysqlVersion, redisVersion, dbPassword,
         webServer, multisite, salts, dbEngine, dbVersion,
         publishDBPort, spxEnabled, spxKey,
         createdAt, updatedAt
  FROM sites;
DROP TABLE sites;
ALTER TABLE sites_backup RENAME TO sites;
