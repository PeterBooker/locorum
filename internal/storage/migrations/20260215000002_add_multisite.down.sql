-- SQLite doesn't support DROP COLUMN in older versions; recreate the table.
CREATE TABLE sites_backup AS SELECT id, name, slug, domain, filesDir, publicDir, started, phpVersion, mysqlVersion, redisVersion, dbPassword, webServer, createdAt, updatedAt FROM sites;
DROP TABLE sites;
ALTER TABLE sites_backup RENAME TO sites;
