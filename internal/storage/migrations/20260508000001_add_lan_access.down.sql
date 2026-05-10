-- SQLite's DROP COLUMN is unreliable across the supported version
-- range; recreate the table without lanEnabled. Mirrors the pattern
-- used by 20260507000001_add_worktree.down.sql.
CREATE TABLE sites_backup AS
  SELECT id, name, slug, domain, filesDir, publicDir, started,
         phpVersion, mysqlVersion, redisVersion, dbPassword,
         webServer, multisite, salts, dbEngine, dbVersion,
         publishDBPort, spxEnabled, spxKey,
         gitRemote, gitBranch, worktreePath, parentSiteID,
         createdAt, updatedAt
  FROM sites;
DROP TABLE sites;
ALTER TABLE sites_backup RENAME TO sites;
CREATE INDEX IF NOT EXISTS idx_sites_parent ON sites(parentSiteID) WHERE parentSiteID != '';
