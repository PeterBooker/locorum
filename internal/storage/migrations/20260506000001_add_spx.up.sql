-- Per-site SPX (php-spx) profiler toggle + persistent key. The key is
-- generated lazily the first time SPX is enabled and reused on later
-- toggles so bookmarked profile URLs keep working.
ALTER TABLE sites ADD COLUMN spxEnabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN spxKey TEXT NOT NULL DEFAULT '';
