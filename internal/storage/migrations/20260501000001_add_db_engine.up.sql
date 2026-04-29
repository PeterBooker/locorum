-- Multi-engine support: introduce dbEngine + dbVersion alongside the
-- legacy mysqlVersion. Existing rows default to "mysql" and copy
-- mysqlVersion → dbVersion so a fresh start picks up where they left off.
ALTER TABLE sites ADD COLUMN dbEngine TEXT NOT NULL DEFAULT 'mysql';
ALTER TABLE sites ADD COLUMN dbVersion TEXT NOT NULL DEFAULT '';
UPDATE sites SET dbVersion = COALESCE(NULLIF(dbVersion, ''), mysqlVersion);

-- Per-site host-port publish toggle for §6.3 of DATABASE.md. Default off
-- so existing sites are unchanged on the next start.
ALTER TABLE sites ADD COLUMN publishDBPort INTEGER NOT NULL DEFAULT 0;
