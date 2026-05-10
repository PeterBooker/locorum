package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Store is the minimal Storage subset Config needs. *storage.Storage
// satisfies it; tests can pass a map-backed fake.
type Store interface {
	GetSetting(key string) (string, error)
	SetSetting(key, value string) error
}

// Config is a typed read/write view over Store. Construct once at
// startup, share the pointer across goroutines.
//
// Reads are served from an in-memory cache populated by Reload. Writes
// go through Store and update the cache atomically. The cache is small
// (~tens of keys) so the lock is a sync.RWMutex held only briefly.
type Config struct {
	st Store

	mu     sync.RWMutex
	cached map[string]string
}

// New constructs a Config and immediately calls Reload. A storage error
// is propagated so callers can refuse to start with a corrupt settings
// row rather than silently using defaults.
func New(st Store) (*Config, error) {
	if st == nil {
		return nil, errors.New("config: nil store")
	}
	c := &Config{st: st, cached: map[string]string{}}
	if err := c.Reload(); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload re-fetches every reserved key from storage. Cheap (O(keys),
// each a single SELECT). Call this when an external mutator has
// touched the settings table — the GUI does not need to since Set
// already invalidates the cache.
func (c *Config) Reload() error {
	keys := allKeys()
	fresh := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := c.st.GetSetting(k)
		if err != nil {
			return fmt.Errorf("config: reload %q: %w", k, err)
		}
		fresh[k] = v
	}
	c.mu.Lock()
	c.cached = fresh
	c.mu.Unlock()
	return nil
}

// allKeys is the canonical list of well-known keys Reload must
// populate. Updated whenever a new Key* constant is added.
func allKeys() []string {
	return []string{
		KeyThemeMode,
		KeyDefaultPHPVersion,
		KeyDefaultDBEngine,
		KeyDefaultDBVersion,
		KeyDefaultRedisVersion,
		KeyDefaultWebServer,
		KeyDefaultPublishDBPort,
		KeyRouterHTTPPort,
		KeyRouterHTTPSPort,
		KeyMkcertPath,
		KeyPerformanceMode,
		KeyUpdateCheckEnabled,
		KeyUpdateCheckChannel,
		KeyHealthEnabled,
		KeyHealthCadenceMinutes,
		KeyHealthDiskCadenceMinutes,
		KeyHealthDiskWarnGB,
		KeyHealthDiskBlockerGB,
		KeyHealthLastSeen,
		KeyAutoSnapshotBeforeDestructive,
		KeyDebugLogging,
		KeyUpdateDismissedVersion,
		KeyUpdateLastAvailable,
	}
}

// raw returns the stored value (empty string if unset). Used by
// accessors so they all share one cache lookup path.
func (c *Config) raw(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cached[key]
}

// Set persists a key/value pair through the store and refreshes the
// cache entry.  Validation is the caller's responsibility for arbitrary
// keys; for well-known keys, prefer the typed Set* helpers below which
// reject invalid enum values up front.
func (c *Config) Set(key, value string) error {
	if err := c.st.SetSetting(key, value); err != nil {
		return fmt.Errorf("config: persist %q: %w", key, err)
	}
	c.mu.Lock()
	c.cached[key] = value
	c.mu.Unlock()
	return nil
}

// ── String accessors ────────────────────────────────────────────────

// ThemeMode returns the persisted theme preference; default "system".
func (c *Config) ThemeMode() string {
	if v := c.raw(KeyThemeMode); v != "" {
		return v
	}
	return "system"
}

// PHPVersionDefault is the version the new-site modal pre-selects.
func (c *Config) PHPVersionDefault() string {
	if v := c.raw(KeyDefaultPHPVersion); v != "" {
		return v
	}
	return DefaultPHPVersion
}

// DBEngineDefault is "mysql" or "mariadb".
func (c *Config) DBEngineDefault() string {
	v := c.raw(KeyDefaultDBEngine)
	if validEnum(v, allowedDBEngines) {
		return v
	}
	return DefaultDBEngine
}

// DBVersionDefault returns the user's last-saved default DB version,
// or empty if unset. Callers must fall back to engine-specific defaults
// (dbengine.MustFor(kind).DefaultVersion()) when this is "" — keeping
// the engine fallback at the call site lets us avoid importing the
// dbengine package here.
func (c *Config) DBVersionDefault() string {
	return c.raw(KeyDefaultDBVersion)
}

// RedisVersionDefault returns the redis tag default.
func (c *Config) RedisVersionDefault() string {
	if v := c.raw(KeyDefaultRedisVersion); v != "" {
		return v
	}
	return DefaultRedisVersion
}

// WebServerDefault returns "nginx" or "apache".
func (c *Config) WebServerDefault() string {
	v := c.raw(KeyDefaultWebServer)
	if validEnum(v, allowedWebServers) {
		return v
	}
	return DefaultWebServer
}

// MkcertPath returns the user-overridden mkcert binary path, or "" if
// the caller should auto-detect on PATH.
func (c *Config) MkcertPath() string {
	return c.raw(KeyMkcertPath)
}

// PerformanceMode is "auto", "bind", or "mutagen".
func (c *Config) PerformanceMode() string {
	v := c.raw(KeyPerformanceMode)
	if validEnum(v, allowedPerformance) {
		return v
	}
	return DefaultPerformance
}

// UpdateCheckChannel is "stable" or "beta".
func (c *Config) UpdateCheckChannel() string {
	v := c.raw(KeyUpdateCheckChannel)
	if validEnum(v, allowedUpdateChannels) {
		return v
	}
	return DefaultUpdateChannel
}

// ── Bool accessors ──────────────────────────────────────────────────

// PublishDBPortDefault returns whether the new-site modal should
// pre-tick the "publish DB host port" switch. Default false.
func (c *Config) PublishDBPortDefault() bool {
	return parseBool(c.raw(KeyDefaultPublishDBPort), false)
}

// UpdateCheckEnabled reports whether to perform background update
// checks. Default true — opt-out semantics.
func (c *Config) UpdateCheckEnabled() bool {
	return parseBool(c.raw(KeyUpdateCheckEnabled), true)
}

// AutoSnapshotBeforeDestructive reports whether SiteManager should
// take a safety snapshot before each destructive op (db restore, db
// import, search-replace, agent-driven root exec). Default true; the
// setter accepts the explicit "false" string to disable.
func (c *Config) AutoSnapshotBeforeDestructive() bool {
	return parseBool(c.raw(KeyAutoSnapshotBeforeDestructive), true)
}

// SetAutoSnapshotBeforeDestructive persists the toggle.
func (c *Config) SetAutoSnapshotBeforeDestructive(on bool) error {
	v := "false"
	if on {
		v = "true"
	}
	return c.Set(KeyAutoSnapshotBeforeDestructive, v)
}

// ── Int accessors ───────────────────────────────────────────────────

// RouterHTTPPort returns the host port the global router binds on for
// plain HTTP. Default 80.
func (c *Config) RouterHTTPPort() int {
	return parseInt(c.raw(KeyRouterHTTPPort), DefaultRouterHTTP)
}

// RouterHTTPSPort returns the host port the global router binds on for
// HTTPS. Default 443.
func (c *Config) RouterHTTPSPort() int {
	return parseInt(c.raw(KeyRouterHTTPSPort), DefaultRouterHTTPS)
}

// ── Typed setters ───────────────────────────────────────────────────

// SetThemeMode validates and persists the theme preference.
func (c *Config) SetThemeMode(v string) error {
	if !validEnum(v, allowedThemeModes) {
		return fmt.Errorf("config: invalid theme mode %q (allowed: %s)", v, strings.Join(allowedThemeModes, ", "))
	}
	return c.Set(KeyThemeMode, v)
}

// SetPHPVersionDefault accepts any non-empty version string. The
// dbengine package validates the actual image tag at site start.
func (c *Config) SetPHPVersionDefault(v string) error {
	if v == "" {
		return errors.New("config: php version cannot be empty")
	}
	return c.Set(KeyDefaultPHPVersion, v)
}

// SetDBEngineDefault validates the engine value.
func (c *Config) SetDBEngineDefault(v string) error {
	if !validEnum(v, allowedDBEngines) {
		return fmt.Errorf("config: invalid db engine %q (allowed: %s)", v, strings.Join(allowedDBEngines, ", "))
	}
	return c.Set(KeyDefaultDBEngine, v)
}

// SetDBVersionDefault accepts any non-empty version string.
func (c *Config) SetDBVersionDefault(v string) error {
	if v == "" {
		return errors.New("config: db version cannot be empty")
	}
	return c.Set(KeyDefaultDBVersion, v)
}

// SetRedisVersionDefault accepts any non-empty version string.
func (c *Config) SetRedisVersionDefault(v string) error {
	if v == "" {
		return errors.New("config: redis version cannot be empty")
	}
	return c.Set(KeyDefaultRedisVersion, v)
}

// SetWebServerDefault validates the web server value.
func (c *Config) SetWebServerDefault(v string) error {
	if !validEnum(v, allowedWebServers) {
		return fmt.Errorf("config: invalid web server %q (allowed: %s)", v, strings.Join(allowedWebServers, ", "))
	}
	return c.Set(KeyDefaultWebServer, v)
}

// SetPublishDBPortDefault persists the boolean as "true"/"false".
func (c *Config) SetPublishDBPortDefault(v bool) error {
	return c.Set(KeyDefaultPublishDBPort, formatBool(v))
}

// SetMkcertPath sets the override path. An empty value clears the
// override (back to autodetect).
func (c *Config) SetMkcertPath(v string) error {
	return c.Set(KeyMkcertPath, v)
}

// SetRouterHTTPPort validates and persists the HTTP host port. The
// caller is responsible for actually binding it — this just records
// user intent.
func (c *Config) SetRouterHTTPPort(port int) error {
	if err := validatePort(port); err != nil {
		return err
	}
	return c.Set(KeyRouterHTTPPort, strconv.Itoa(port))
}

// SetRouterHTTPSPort is the HTTPS twin of SetRouterHTTPPort.
func (c *Config) SetRouterHTTPSPort(port int) error {
	if err := validatePort(port); err != nil {
		return err
	}
	return c.Set(KeyRouterHTTPSPort, strconv.Itoa(port))
}

// SetUpdateCheckEnabled toggles the background update check.
func (c *Config) SetUpdateCheckEnabled(v bool) error {
	return c.Set(KeyUpdateCheckEnabled, formatBool(v))
}

// SetUpdateCheckChannel validates and persists the release channel.
func (c *Config) SetUpdateCheckChannel(v string) error {
	if !validEnum(v, allowedUpdateChannels) {
		return fmt.Errorf("config: invalid update channel %q (allowed: %s)", v, strings.Join(allowedUpdateChannels, ", "))
	}
	return c.Set(KeyUpdateCheckChannel, v)
}

// SetPerformanceMode validates and persists the perf mode.
func (c *Config) SetPerformanceMode(v string) error {
	if !validEnum(v, allowedPerformance) {
		return fmt.Errorf("config: invalid performance mode %q (allowed: %s)", v, strings.Join(allowedPerformance, ", "))
	}
	return c.Set(KeyPerformanceMode, v)
}

// ── System Health ───────────────────────────────────────────────────

// HealthEnabled reports whether the System Health runner should publish
// snapshots. Default true. When false, the UI hides the badge, modal,
// toasts, and panel findings.
func (c *Config) HealthEnabled() bool {
	return parseBool(c.raw(KeyHealthEnabled), DefaultHealthEnabled)
}

// SetHealthEnabled persists the on/off switch.
func (c *Config) SetHealthEnabled(v bool) error {
	return c.Set(KeyHealthEnabled, formatBool(v))
}

// HealthCadenceMinutes is the global health-check cadence in minutes.
// Default 5. Returns the default for invalid stored values.
func (c *Config) HealthCadenceMinutes() int {
	return parseInt(c.raw(KeyHealthCadenceMinutes), DefaultHealthCadenceMinutes)
}

// SetHealthCadenceMinutes persists the cadence; rejects ≤0.
func (c *Config) SetHealthCadenceMinutes(v int) error {
	if v <= 0 {
		return fmt.Errorf("config: cadence must be positive (got %d)", v)
	}
	return c.Set(KeyHealthCadenceMinutes, strconv.Itoa(v))
}

// HealthDiskCadenceMinutes is the cadence for the expensive disk-usage
// check. Default 15.
func (c *Config) HealthDiskCadenceMinutes() int {
	return parseInt(c.raw(KeyHealthDiskCadenceMinutes), DefaultHealthDiskCadenceMinutes)
}

// SetHealthDiskCadenceMinutes persists the disk-check cadence; rejects ≤0.
func (c *Config) SetHealthDiskCadenceMinutes(v int) error {
	if v <= 0 {
		return fmt.Errorf("config: disk cadence must be positive (got %d)", v)
	}
	return c.Set(KeyHealthDiskCadenceMinutes, strconv.Itoa(v))
}

// HealthDiskWarnGB is the threshold below which we surface a warning
// finding. Default 5.
func (c *Config) HealthDiskWarnGB() int {
	return parseInt(c.raw(KeyHealthDiskWarnGB), DefaultHealthDiskWarnGB)
}

// SetHealthDiskWarnGB persists the warn threshold; rejects ≤0.
func (c *Config) SetHealthDiskWarnGB(v int) error {
	if v <= 0 {
		return fmt.Errorf("config: warn threshold must be positive (got %d)", v)
	}
	return c.Set(KeyHealthDiskWarnGB, strconv.Itoa(v))
}

// HealthDiskBlockerGB is the threshold below which we escalate to a
// blocker. Default 1.
func (c *Config) HealthDiskBlockerGB() int {
	return parseInt(c.raw(KeyHealthDiskBlockerGB), DefaultHealthDiskBlockerGB)
}

// SetHealthDiskBlockerGB persists the blocker threshold; rejects ≤0 and
// any value ≥ the warn threshold (since blocker must be tighter).
func (c *Config) SetHealthDiskBlockerGB(v int) error {
	if v <= 0 {
		return fmt.Errorf("config: blocker threshold must be positive (got %d)", v)
	}
	warn := c.HealthDiskWarnGB()
	if v >= warn {
		return fmt.Errorf("config: blocker threshold (%d GB) must be smaller than warn threshold (%d GB)", v, warn)
	}
	return c.Set(KeyHealthDiskBlockerGB, strconv.Itoa(v))
}

// ── Diagnostics ─────────────────────────────────────────────────────

// DebugLogging reports whether the applog handler should emit Debug
// records. Default false.
func (c *Config) DebugLogging() bool {
	return parseBool(c.raw(KeyDebugLogging), false)
}

// SetDebugLogging persists the toggle. The caller is responsible for
// applying the new level via applog.SetDebug.
func (c *Config) SetDebugLogging(on bool) error {
	return c.Set(KeyDebugLogging, formatBool(on))
}

// ── Update-check banner state ───────────────────────────────────────

// UpdateDismissedVersion returns the version string the user last clicked
// "Dismiss this version" on, or "" if nothing has been dismissed.
func (c *Config) UpdateDismissedVersion() string {
	return c.raw(KeyUpdateDismissedVersion)
}

// SetUpdateDismissedVersion persists the dismissed version.
func (c *Config) SetUpdateDismissedVersion(v string) error {
	return c.Set(KeyUpdateDismissedVersion, v)
}

// UpdateLastAvailable returns the most recent latest-available version
// surfaced by the periodic update check. "" until the first check
// completes.
func (c *Config) UpdateLastAvailable() string {
	return c.raw(KeyUpdateLastAvailable)
}

// SetUpdateLastAvailable persists the snapshot.
func (c *Config) SetUpdateLastAvailable(v string) error {
	return c.Set(KeyUpdateLastAvailable, v)
}

// HealthLastSeen returns the persisted last-seen-finding-keys JSON blob.
// Empty string on first run. The value is opaque to the config package;
// the UI's toast handler parses it.
func (c *Config) HealthLastSeen() string {
	return c.raw(KeyHealthLastSeen)
}

// SetHealthLastSeen persists the JSON blob.
func (c *Config) SetHealthLastSeen(v string) error {
	return c.Set(KeyHealthLastSeen, v)
}

// ── Helpers ──────────────────────────────────────────────────────────

func validEnum(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func parseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func formatBool(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

// parseInt returns def if v is empty, not a base-10 integer, or negative.
// Used by accessors that document a positive default — silently falling
// back is preferable to crashing on a bad row.
func parseInt(v string, def int) int {
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// validatePort is the standard "0 < port ≤ 65535" check used by the
// router-port setters. Reserved ports < 1024 are allowed because
// Locorum is the binding agent for the router on 80/443.
func validatePort(port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("config: port %d out of range (1..65535)", port)
	}
	return nil
}
