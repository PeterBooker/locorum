// Package config is a typed facade over Locorum's flat key/value
// `settings` table. It centralises the namespace of well-known global
// keys so a typo in `defaults.php_version` (singular) becomes a compile
// error rather than a silent unset.
//
// Concurrency: safe to read and write from multiple goroutines. Reads
// hit a small in-memory map; writes upsert through the Store interface
// and refresh the cache. Reload re-pulls every key from storage and is
// invoked once at startup.
//
// Defaults: every Get* accessor documents its zero-value. Storage rows
// use the literal value "" for unset; accessors translate that to the
// documented default. Boolean keys accept "true"/"false" (case-
// insensitive) — anything else is treated as the default.
//
// This package depends only on stdlib + storage; do not add UI or
// site-manager imports.
package config

// Reserved keys. Add new keys here, with a brief comment, before using
// them in code. Naming rule: dotted lowercase, with prefixes that group
// related settings — the new-site modal walks the `defaults.` prefix to
// seed its initial form state, so any setting that should appear there
// MUST live under `defaults.`.
const (
	// KeyThemeMode persists the user's theme preference
	// ("system", "dark", "light"). Pre-existing key — name kept for
	// backward compat with shipped sites.
	KeyThemeMode = "theme_mode"

	// New-site defaults. Read by internal/ui/newsite.go to pre-fill
	// the form. Empty values fall back to the documented zero
	// returned by the accessor.
	KeyDefaultPHPVersion    = "defaults.php_version"
	KeyDefaultDBEngine      = "defaults.db_engine"
	KeyDefaultDBVersion     = "defaults.db_version"
	KeyDefaultRedisVersion  = "defaults.redis_version"
	KeyDefaultWebServer     = "defaults.web_server"
	KeyDefaultPublishDBPort = "defaults.publish_db_port"

	// Router. Reserved for use by internal/router/traefik when the
	// 80/443 conflict-fallback pattern lands (LEARNINGS §2.5).
	KeyRouterHTTPPort  = "router.http_port"
	KeyRouterHTTPSPort = "router.https_port"

	// TLS / mkcert. Empty means "autodetect on PATH".
	KeyMkcertPath = "mkcert.path"

	// Performance mode. Reserved for the LEARNINGS §6.3 mutagen
	// integration. Values: "auto", "bind", "mutagen". Default "auto".
	KeyPerformanceMode = "performance.mode"

	// Update check. Reserved for LEARNINGS §7.4.
	KeyUpdateCheckEnabled = "update_check.enabled"
	KeyUpdateCheckChannel = "update_check.channel"

	// System Health (CROSS-PLATFORM.md). All optional — sensible
	// defaults apply when unset.
	KeyHealthEnabled            = "health.enabled"         // bool, default true
	KeyHealthCadenceMinutes     = "health.cadence_minutes" // int, default 5
	KeyHealthDiskCadenceMinutes = "health.disk_check_cadence_minutes"
	KeyHealthDiskWarnGB         = "health.disk_warn_gb"    // int, default 5
	KeyHealthDiskBlockerGB      = "health.disk_blocker_gb" // int, default 1
	KeyHealthLastSeen           = "health.last_seen"       // json, internal

	// Auto-snapshot wraps for destructive ops (P4 in
	// AGENTS-SUPPORT.md). When true (the default), Locorum captures a
	// safety snapshot before each of: snapshot restore, wp db import,
	// wp search-replace, agent-driven `exec` as root in the database
	// container. Power users with their own backup discipline can
	// disable.
	KeyAutoSnapshotBeforeDestructive = "snapshots.auto_before_destructive"

	// KeyDebugLogging is the Settings → Diagnostics "Debug Mode" toggle.
	// When true, the applog handler emits Debug-level records too (UI
	// only — the runner cadence is unaffected). Default false.
	KeyDebugLogging = "diagnostics.debug_logging"

	// KeyUpdateDismissedVersion records the latest available version the
	// user dismissed via the "Dismiss this version" button on the
	// Diagnostics card. The banner is suppressed while
	// semver(latest) <= semver(this).
	KeyUpdateDismissedVersion = "update_check.dismissed_version"

	// KeyUpdateLastAvailable records the most recent latest-available
	// version surfaced by the update check, so the Settings card has
	// something to render on a fresh launch before the next fetch.
	KeyUpdateLastAvailable = "update_check.last_available"

	// LAN access (ACCESS.md). KeyLanDefault is reserved for a future
	// "auto-enable on every new site" workflow; the per-site toggle in
	// the Site row is the source of truth today. KeyLanDomain is an
	// escape hatch for self-hosted nip.io drop-ins. KeyLanIPOverride
	// pins the LAN IP when auto-detection picks the wrong interface.
	KeyLanDefault    = "lan.default_enabled" // bool, default false
	KeyLanDomain     = "lan.domain"          // default "sslip.io"
	KeyLanIPOverride = "lan.ip_override"     // optional manual IPv4
)

// Documented default values for every accessor. Centralising these
// makes them easy to audit and lets tests assert "we did not regress
// the default by accident".
const (
	DefaultPHPVersion    = "8.3"
	DefaultDBEngine      = "mysql"
	DefaultRedisVersion  = "7"
	DefaultWebServer     = "nginx"
	DefaultRouterHTTP    = 80
	DefaultRouterHTTPS   = 443
	DefaultPerformance   = "auto"
	DefaultUpdateChannel = "stable"

	DefaultHealthEnabled            = true
	DefaultHealthCadenceMinutes     = 5
	DefaultHealthDiskCadenceMinutes = 15
	DefaultHealthDiskWarnGB         = 5
	DefaultHealthDiskBlockerGB      = 1

	// DefaultLanDomain is the public wildcard-DNS service used to map
	// `<anything>.<ipv4>.<domain>` back to the host's LAN IP. sslip.io
	// is free, requires no setup, and is widely cached by ISP resolvers.
	DefaultLanDomain = "sslip.io"
)

// Allowed enum values. Used by Set* validation.
var (
	allowedDBEngines      = []string{"mysql", "mariadb"}
	allowedWebServers     = []string{"nginx", "apache"}
	allowedThemeModes     = []string{"system", "dark", "light"}
	allowedPerformance    = []string{"auto", "bind", "mutagen"}
	allowedUpdateChannels = []string{"stable", "beta"}
)
