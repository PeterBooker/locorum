CREATE TABLE activity_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id     TEXT    NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    time        TEXT    NOT NULL,
    plan        TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    duration_ms INTEGER NOT NULL,
    message     TEXT    NOT NULL,
    details     TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX idx_activity_events_site_time ON activity_events(site_id, time DESC);
