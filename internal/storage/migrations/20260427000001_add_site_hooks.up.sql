CREATE TABLE site_hooks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id     TEXT    NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    event       TEXT    NOT NULL,
    position    INTEGER NOT NULL,
    task_type   TEXT    NOT NULL,
    command     TEXT    NOT NULL,
    service     TEXT    NOT NULL DEFAULT '',
    run_as_user TEXT    NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL,
    UNIQUE(site_id, event, position)
);

CREATE INDEX idx_site_hooks_site_event ON site_hooks(site_id, event);
