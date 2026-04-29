-- SQLite doesn't support DROP COLUMN reliably across the supported
-- versions; recreate the table without the new columns. Mirrors the
-- pattern in 20260215000002_add_multisite.down.sql.
CREATE TABLE sites_backup AS
  SELECT id, name, slug, domain, filesDir, publicDir, started,
         phpVersion, mysqlVersion, redisVersion, dbPassword,
         webServer, multisite, salts, createdAt, updatedAt
  FROM sites;
DROP TABLE sites;
ALTER TABLE sites_backup RENAME TO sites;
