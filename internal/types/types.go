package types

type Site struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Domain    string `json:"domain"`
	FilesDir  string `json:"filesDir"`
	PublicDir string `json:"publicDir"`
	Started   bool   `json:"started"`

	PHPVersion   string `json:"phpVersion"`
	RedisVersion string `json:"redisVersion"`
	DBPassword   string `json:"dbPassword"`
	WebServer    string `json:"webServer"` // "nginx" or "apache"
	Multisite    string `json:"multisite"` // "", "subdirectory", or "subdomain"

	// DBEngine is the database engine name ("mysql" or "mariadb"). Read
	// through dbengine.Resolve(site) which falls back to MySQL for legacy
	// rows.
	DBEngine string `json:"dbEngine"`

	// DBVersion is the engine-specific version tag (e.g. "8.4", "11.4").
	// The combination of (DBEngine, DBVersion) selects the Docker image.
	DBVersion string `json:"dbVersion"`

	// MySQLVersion is the pre-multi-engine field. Kept readable for one
	// minor release so legacy rows continue to start; new code writes
	// only DBVersion. Will be dropped after the next minor.
	//
	// Deprecated: use DBVersion + DBEngine.
	MySQLVersion string `json:"mysqlVersion,omitempty"`

	// PublishDBPort opts in to publishing the database container's port
	// to 127.0.0.1 on the host (random ephemeral). Surfaced in the DB
	// Credentials panel as a "Host port" row + connection URL.
	PublishDBPort bool `json:"publishDBPort"`

	// SPXEnabled opts the site in to the php-spx profiler. The flag is
	// applied at next start; mid-life toggling is rejected upstream.
	SPXEnabled bool `json:"spxEnabled"`

	// SPXKey is the per-site secret SPX requires on every profile or UI
	// request (URL query param + cookie value). Generated on first
	// enable and persisted; toggling SPX off keeps the key so a later
	// re-enable preserves bookmarked URLs. Treated as a credential —
	// never serialised over JSON, never written to YAML projection,
	// never logged.
	SPXKey string `json:"-"`

	// Salts is a JSON-encoded map[string]string of the eight WordPress
	// secret keys (AUTH_KEY, SECURE_AUTH_KEY, …, NONCE_SALT). Generated
	// once at site creation and persisted so wp-config.php regenerates
	// produce a byte-identical file (idempotent writes).
	Salts string `json:"-"`

	// GitRemote is the canonical remote URL of the upstream repository
	// for worktree-bound sites (P1, AGENTS-SUPPORT.md). Empty for
	// conventional sites; non-empty implies worktreePath / gitBranch
	// are also set and FilesDir points at a git worktree.
	GitRemote string `json:"gitRemote,omitempty"`

	// GitBranch is the branch the worktree tracks. Same shape rules
	// as GitRemote: empty for conventional sites, set for worktrees.
	GitBranch string `json:"gitBranch,omitempty"`

	// WorktreePath is the host path of the git worktree directory.
	// For worktree-bound sites this is also FilesDir; we keep both so
	// a future flavour B (plugin/theme mode) can have FilesDir point
	// at a scratch WP install while WorktreePath identifies the
	// underlying checkout to remove via `git worktree remove`.
	WorktreePath string `json:"worktreePath,omitempty"`

	// ParentSiteID identifies the parent (conventional) site that
	// owns the upstream checkout. Worktree sites point at this row so
	// parent-delete can cascade-clean its workers; conventional sites
	// leave it empty.
	ParentSiteID string `json:"parentSiteID,omitempty"`

	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
