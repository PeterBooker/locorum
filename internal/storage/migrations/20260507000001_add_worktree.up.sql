-- Worktree-bound sites (P1, AGENTS-SUPPORT.md). A site row gains four
-- columns identifying its git provenance and parent relationship. All
-- four are NOT NULL with empty-string defaults so legacy rows keep
-- working without a backfill: an empty gitRemote means "this is a
-- conventional WordPress install, not bound to a git checkout."
--
--   gitRemote    — canonical remote URL, e.g. "git@github.com:foo/bar.git"
--   gitBranch    — branch name the worktree tracks (e.g. "feature/x")
--   worktreePath — host path of the git worktree (when bound)
--   parentSiteID — non-empty for worktree sites; FK to sites(id)
--
-- parentSiteID is unconstrained at the SQL level: enforcing it as a
-- foreign key would force the parent site to be deleted last, blocking
-- the existing "delete a site cascade-deletes its hooks" UX. Instead,
-- DeleteSite for a parent site purges its worktree-children first;
-- orphaned rows from a hand-edited DB are tolerated and treated as
-- regular sites.
ALTER TABLE sites ADD COLUMN gitRemote    TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN gitBranch    TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN worktreePath TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN parentSiteID TEXT NOT NULL DEFAULT '';

-- Index for the "find every worktree-child of this parent" query that
-- DeleteSite uses to cascade. parentSiteID is the empty string for
-- regular sites, so the index covers only the rows that need it.
CREATE INDEX IF NOT EXISTS idx_sites_parent ON sites(parentSiteID) WHERE parentSiteID != '';
