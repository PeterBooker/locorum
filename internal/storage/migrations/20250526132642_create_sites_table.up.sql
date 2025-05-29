CREATE TABLE sites (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    domain TEXT NOT NULL,
    filesDir TEXT NOT NULL,
    publicDir TEXT NOT NULL,
    started BOOLEAN NOT NULL DEFAULT FALSE,
    phpVersion TEXT,
    mysqlVersion TEXT,
    redisVersion TEXT,
    createdAt TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updatedAt TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);