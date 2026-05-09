  ,
);
);
);
);
  command TEXT NOT NULL,
  created_at TEXT NOT NULL,
  createdAt TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
CREATE INDEX idx_activity_events_site_time ON activity_events(
CREATE INDEX idx_site_hooks_site_event ON site_hooks(site_id, event);
CREATE INDEX idx_sites_parent ON sites(parentSiteID) WHERE parentSiteID != '';
CREATE TABLE activity_events(
CREATE TABLE settings(key TEXT PRIMARY KEY,
CREATE TABLE site_hooks(
CREATE TABLE sites(
CREATE TABLE sqlite_sequence(name,seq);
  dbEngine TEXT NOT NULL DEFAULT 'mysql',
  dbPassword TEXT NOT NULL DEFAULT 'password',
  dbVersion TEXT NOT NULL DEFAULT '',
  details TEXT NOT NULL DEFAULT ''
  domain TEXT NOT NULL,
  duration_ms INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  event TEXT NOT NULL,
  filesDir TEXT NOT NULL,
  gitBranch TEXT NOT NULL DEFAULT '',
  gitRemote TEXT NOT NULL DEFAULT '',
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  message TEXT NOT NULL,
  multisite TEXT NOT NULL DEFAULT '',
  mysqlVersion TEXT,
  name TEXT NOT NULL,
  parentSiteID TEXT NOT NULL DEFAULT ''
  phpVersion TEXT,
  plan TEXT NOT NULL,
  position INTEGER NOT NULL,
  publicDir TEXT NOT NULL,
  publishDBPort INTEGER NOT NULL DEFAULT 0,
  redisVersion TEXT,
  run_as_user TEXT NOT NULL DEFAULT '',
  salts TEXT NOT NULL DEFAULT '',
  service TEXT NOT NULL DEFAULT '',
  site_id,
  site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  site_id TEXT NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  slug TEXT NOT NULL,
  spxEnabled INTEGER NOT NULL DEFAULT 0,
  spxKey TEXT NOT NULL DEFAULT '',
  started BOOLEAN NOT NULL DEFAULT FALSE,
  status TEXT NOT NULL,
  task_type TEXT NOT NULL,
  time DESC
  time TEXT NOT NULL,
  UNIQUE(site_id, event, position)
  updated_at TEXT NOT NULL,
  updatedAt TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
value TEXT NOT NULL);
  webServer TEXT NOT NULL DEFAULT 'nginx',
  worktreePath TEXT NOT NULL DEFAULT '',
