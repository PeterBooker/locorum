package hooks

import (
	"runtime"
	"sort"

	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/version"
)

// EnvContext selects which network perspective the env vars should describe.
// Container-context vars resolve database addresses to the in-network alias
// (database:3306); host-context vars resolve them to 127.0.0.1 plus the
// published port.
type EnvContext int

const (
	// ContextContainer is for tasks that run inside one of the site's
	// containers (exec, wp-cli).
	ContextContainer EnvContext = iota

	// ContextHost is for tasks that run on the host shell (exec-host).
	ContextHost
)

// String returns a stable label suitable for diagnostics.
func (c EnvContext) String() string {
	switch c {
	case ContextHost:
		return "host"
	default:
		return "container"
	}
}

// HostDBPort is the host port published by the site database container in
// host-context tasks. Locorum currently does not publish per-site DB ports
// to the host; users typically connect via Adminer at db.localhost. We
// expose the in-container port (3306) here as a sensible default. If/when
// the platform starts publishing per-site DB ports, swap this for a lookup
// against the docker port mapping.
const HostDBPort = "3306"

// BuildEnv returns the complete LOCORUM_* environment variable list to
// inject into a hook task. The result is a fresh slice; the caller is free
// to append to it.
//
// Variables are sorted by name so the output is reproducible across calls
// (helpful for diffs in tests and reasoning about ordering precedence).
//
// The returned slice is "KEY=VALUE" formatted, ready to pass to either
// docker.ExecOptions.Env or utils.HostExecOptions.Env.
func BuildEnv(site *types.Site, ctx EnvContext) []string {
	if site == nil {
		return nil
	}

	domain := site.Domain
	if domain == "" && site.Slug != "" {
		domain = site.Slug + ".localhost"
	}

	webServer := site.WebServer
	if webServer == "" {
		webServer = "nginx" // matches AddSite default
	}

	dbHost := "database"
	dbPort := "3306"
	if ctx == ContextHost {
		dbHost = "127.0.0.1"
		dbPort = HostDBPort
	}

	gui := "1"
	if !runningUnderGUI() {
		gui = "0"
	}

	vars := map[string]string{
		"LOCORUM_VERSION":       version.Version,
		"LOCORUM_SITE_ID":       site.ID,
		"LOCORUM_SITE_NAME":     site.Name,
		"LOCORUM_SITE_SLUG":     site.Slug,
		"LOCORUM_DOMAIN":        domain,
		"LOCORUM_PRIMARY_URL":   primaryURL(domain),
		"LOCORUM_PHP_VERSION":   site.PHPVersion,
		"LOCORUM_MYSQL_VERSION": site.MySQLVersion, //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
		"LOCORUM_REDIS_VERSION": site.RedisVersion,
		"LOCORUM_WEBSERVER":     webServer,
		"LOCORUM_MULTISITE":     site.Multisite,
		"LOCORUM_FILES_DIR":     site.FilesDir,
		"LOCORUM_PUBLIC_DIR":    site.PublicDir,
		"LOCORUM_DB_HOST":       dbHost,
		"LOCORUM_DB_NAME":       "wordpress",
		"LOCORUM_DB_USER":       "wordpress",
		"LOCORUM_DB_PASSWORD":   site.DBPassword,
		"LOCORUM_DB_PORT":       dbPort,
		"LOCORUM_OS":            runtime.GOOS,
		"LOCORUM_GUI":           gui,
		"LOCORUM_CONTEXT":       ctx.String(),
	}

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+vars[k])
	}
	return out
}

func primaryURL(domain string) string {
	if domain == "" {
		return ""
	}
	return "https://" + domain
}

// runningUnderGUI is indirected through a variable so tests can override it.
// In production every code path through BuildEnv is reached from the Gio
// event loop, so this returns true.
var runningUnderGUI = func() bool { return true }
