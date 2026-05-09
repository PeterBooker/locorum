package dbengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/version"
)

// mysqlEngine is the MySQL implementation. The methods here are the
// canonical reference for adding a new engine — mariadb.go re-uses most
// of this and overrides only what differs.
type mysqlEngine struct{}

func (mysqlEngine) Kind() Kind             { return MySQL }
func (mysqlEngine) DefaultPort() int       { return 3306 }
func (mysqlEngine) DataDir() string        { return "/var/lib/mysql" }
func (mysqlEngine) DefaultVersion() string { return "8.4" }
func (mysqlEngine) KnownVersions() []string {
	return []string{"8.4", "8.0"}
}

func (mysqlEngine) Image(v string) string {
	return version.MySQLImagePrefix + v
}

// ConfMountTarget is where the dev-tuned config file lands inside the
// container. MySQL reads /etc/mysql/conf.d/*.cnf at startup.
func (mysqlEngine) ConfMountTarget() string {
	return "/etc/mysql/conf.d/locorum.cnf"
}

// ContainerSpec mirrors the historical docker.DatabaseSpec output for
// MySQL — byte-identical so existing volumes and config hashes remain
// stable on upgrade.
//
// Security defaults match the rest of internal/docker: hardened caps,
// no-new-privileges, log size capped at 10m × 3.
func (e mysqlEngine) ContainerSpec(site *types.Site, homeDir string) docker.ContainerSpec {
	name := docker.SiteContainerName(site.Slug, "database")
	netName := docker.SiteNetworkName(site.Slug)
	dbConfPath := filepath.Join(homeDir, ".locorum", "config", "dbengine", "mysql", "locorum.cnf")
	return docker.ContainerSpec{
		Name:   name,
		Image:  e.Image(dbVersionFor(site)),
		Tty:    true,
		Cmd:    []string{"mysqld", "--innodb-flush-method=fsync"},
		Labels: docker.PlatformLabels(docker.RoleDatabase, site.Slug, version.Version),
		Env: []string{
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
		},
		EnvSecrets: []docker.EnvSecret{
			{Key: "MYSQL_ROOT_PASSWORD", Value: site.DBPassword},
			{Key: "MYSQL_PASSWORD", Value: site.DBPassword},
		},
		Mounts: []docker.Mount{
			{Volume: &docker.VolumeMount{Name: docker.SiteVolumeName(site.Slug), Target: e.DataDir()}},
			{Bind: &docker.BindMount{Source: dbConfPath, Target: e.ConfMountTarget(), ReadOnly: true}},
		},
		Networks: []docker.NetworkAttachment{
			{Network: netName, Aliases: []string{"database"}},
			{Network: docker.GlobalNetwork},
		},
		Ports: publishedPorts(site, e.DefaultPort()),
		Healthcheck: &docker.Healthcheck{
			// mysqladmin ping respects MYSQL_PWD so the password never
			// hits the command line. Both mysql and mariadb images ship
			// mysqladmin.
			Test:        []string{"CMD-SHELL", "MYSQL_PWD=\"$MYSQL_ROOT_PASSWORD\" mysqladmin ping -h 127.0.0.1 -uroot --silent"},
			Interval:    2 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     60,
			StartPeriod: 5 * time.Second,
		},
		Security: docker.SecurityOptions{
			CapDrop:         []string{"ALL"},
			CapAdd:          []string{"CHOWN", "SETGID", "SETUID", "DAC_OVERRIDE", "FOWNER", "FSETID"},
			NoNewPrivileges: true,
		},
		Resources: docker.Resources{
			LogMaxSize:  "10m",
			LogMaxFiles: 3,
			PidsLimit:   1024,
		},
		Init:    true,
		Restart: docker.RestartNo,
	}
}

// Snapshot streams `mysqldump` directly into w. The dump is logical SQL
// (not binary) so it round-trips across MySQL minor versions and into
// MariaDB. Both engines ship `mysqldump` (MariaDB via a symlink to
// `mariadb-dump`) so the same code path works for both.
//
// Flags chosen for fidelity over speed:
//   - --single-transaction: consistent snapshot of InnoDB tables without
//     long table locks.
//   - --quick: stream rows one at a time instead of buffering.
//   - --default-character-set=utf8mb4: WP standard.
//   - --add-drop-table: restore is idempotent.
//   - --routines --triggers --events: capture stored objects, not just
//     row data.
//   - --hex-blob: blob columns survive the round-trip even on weird
//     terminals / locales.
//   - --no-tablespaces: avoids requiring PROCESS privilege which the
//     wordpress user does not hold.
func (e mysqlEngine) Snapshot(ctx context.Context, ex Execer, site *types.Site, w io.Writer) (int64, error) {
	cn := docker.SiteContainerName(site.Slug, "database")
	cmd := []string{
		"sh", "-c",
		// MYSQL_PWD is read by mysqldump from the env, keeping the
		// password out of the process arg list (which docker top / ps
		// would otherwise expose).
		"MYSQL_PWD=\"$MYSQL_ROOT_PASSWORD\" mysqldump -uroot " +
			"--single-transaction --quick --default-character-set=utf8mb4 " +
			"--add-drop-table --routines --triggers --events --hex-blob " +
			"--no-tablespaces wordpress",
	}
	cw := &countWriter{w: w}
	exit, err := ex.ExecInContainerWriter(ctx, cn, docker.ExecOptions{Cmd: cmd}, cw, nil)
	if err != nil {
		return cw.n, fmt.Errorf("mysqldump exec: %w", err)
	}
	if exit != 0 {
		return cw.n, fmt.Errorf("mysqldump exited %d", exit)
	}
	if cw.n == 0 {
		return 0, errors.New("mysqldump produced no output — database empty or unreachable")
	}
	return cw.n, nil
}

// Restore pipes r into `mysql wordpress`. The dump is expected to have
// already been preprocessed via FilterImportStream(e.Filters(), ...) by
// the caller — engine-internal restores apply no extra filtering.
func (e mysqlEngine) Restore(ctx context.Context, ex Execer, site *types.Site, r io.Reader) error {
	cn := docker.SiteContainerName(site.Slug, "database")
	cmd := []string{
		"sh", "-c",
		"MYSQL_PWD=\"$MYSQL_ROOT_PASSWORD\" mysql -uroot --default-character-set=utf8mb4 wordpress",
	}
	exit, err := ex.ExecInContainerWriterStdin(ctx, cn, docker.ExecOptions{Cmd: cmd}, r, nil, nil)
	if err != nil {
		return fmt.Errorf("mysql restore: %w", err)
	}
	if exit != 0 {
		return fmt.Errorf("mysql restore exited %d", exit)
	}
	return nil
}

// Filters is the canonical MySQL filter chain. MariaDB inherits this set
// and adds uca1400 collation rewrites + sandbox-mode header stripping —
// the latter being a MariaDB-only producer-side artefact.
func (mysqlEngine) Filters() []ImportFilter { return mysqlBaseFilters }

// UpgradeAllowed permits same-major upgrades (8.0 → 8.4 yes, 8.x → 5.7
// no). Cross-major upgrades land in the migrate-via-snapshot flow.
func (mysqlEngine) UpgradeAllowed(from, to string) bool {
	return sameMajor(from, to)
}

func (e mysqlEngine) ConnectionURL(host, port string, site *types.Site) string {
	if port == "" {
		port = strconv.Itoa(e.DefaultPort())
	}
	return fmt.Sprintf("mysql://wordpress:%s@%s:%s/wordpress", site.DBPassword, host, port)
}

// ──────────────────────────────────────────────────────────────────────
// Filters
//
// These regexes used to live in internal/sites/import_filters.go. They
// move here because each engine's chain is the engine's responsibility;
// MariaDB extends the same set.
// ──────────────────────────────────────────────────────────────────────

var (
	importUCA1400Pat = regexp.MustCompile(`utf8mb4_uca1400_[A-Za-z0-9_]+`)
	importDefinerPat = regexp.MustCompile("DEFINER=[^ ]+@[^ ]+ ")
)

// mysqlBaseFilters is the chain shared by MySQL and MariaDB. MariaDB
// extends it via mariadbExtraFilters in mariadb.go.
var mysqlBaseFilters = []ImportFilter{
	{
		Name: "drop CREATE DATABASE",
		Pat:  regexp.MustCompile(`(?i)^\s*CREATE\s+DATABASE\b`),
		Fn:   func(_ []byte) []byte { return nil },
	},
	{
		Name: "drop USE database",
		Pat:  regexp.MustCompile(`(?i)^\s*USE\s+`),
		Fn:   func(_ []byte) []byte { return nil },
	},
	{
		Name: "rewrite uca1400 collations",
		Pat:  regexp.MustCompile(`utf8mb4_uca1400_[A-Za-z0-9_]+`),
		Fn: func(line []byte) []byte {
			return importUCA1400Pat.ReplaceAll(line, []byte("utf8mb4_unicode_ci"))
		},
	},
	{
		Name: "strip DEFINER clause",
		Pat:  regexp.MustCompile("DEFINER=[^ ]+@[^ ]+ "),
		Fn: func(line []byte) []byte {
			return importDefinerPat.ReplaceAll(line, []byte(""))
		},
	},
	{
		Name: "drop SQL_LOG_BIN session toggles",
		Pat:  regexp.MustCompile(`(?i)^\s*SET\s+@@SESSION\.SQL_LOG_BIN`),
		Fn:   func(_ []byte) []byte { return nil },
	},
}

// ──────────────────────────────────────────────────────────────────────
// Helpers shared with mariadb.go
// ──────────────────────────────────────────────────────────────────────

// dbVersionFor returns the engine version string for the site, falling
// back to MySQLVersion for sites that pre-date the multi-engine schema.
// New code should set Site.DBVersion exclusively; this helper keeps the
// transition silent.
func dbVersionFor(site *types.Site) string {
	if v := strings.TrimSpace(site.DBVersion); v != "" {
		return v
	}
	return site.MySQLVersion //nolint:staticcheck // SA1019: legacy mirror, kept for back-compat with rows written before the DBVersion+DBEngine split
}

// publishedPorts returns the spec.Ports slice for the engine. PublishDBPort
// is a per-site setting (see internal/sites — db.publish_port). When
// disabled, returns an empty slice so the port stays internal-only.
func publishedPorts(site *types.Site, containerPort int) []docker.PortMap {
	if !site.PublishDBPort {
		return nil
	}
	return []docker.PortMap{{
		HostIP:        "127.0.0.1",
		HostPort:      "0", // Docker assigns ephemeral
		ContainerPort: strconv.Itoa(containerPort),
		Proto:         "tcp",
	}}
}

// sameMajor reports whether two semver-like strings share their first
// dotted component. "8.0" and "8.4" → true; "8.0" and "5.7" → false.
// Empty inputs match anything (used as the upgrade-from-empty case for
// fresh sites).
func sameMajor(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	majA, _, _ := strings.Cut(a, ".")
	majB, _, _ := strings.Cut(b, ".")
	return majA == majB
}

// countWriter wraps an io.Writer, counting bytes for progress reporting.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
