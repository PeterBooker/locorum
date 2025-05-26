CREATE TABLE sites (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    domain TEXT NOT NULL,
    started BOOLEAN NOT NULL DEFAULT FALSE,
    php_version TEXT,
    mysql_version TEXT,
    redis_version TEXT
);