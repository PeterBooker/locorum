package dbengine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/version"
)

// mariadbEngine is the MariaDB implementation. Same shape as MySQL —
// docker image differs, the conf mount target moves from
// /etc/mysql/conf.d/ to /etc/mysql/mariadb.conf.d/, and the import filter
// chain gains MariaDB's sandbox-mode header stripping.
type mariadbEngine struct{}

func (mariadbEngine) Kind() Kind             { return MariaDB }
func (mariadbEngine) DefaultPort() int       { return 3306 }
func (mariadbEngine) DataDir() string        { return "/var/lib/mysql" }
func (mariadbEngine) DefaultVersion() string { return "11.4" }
func (mariadbEngine) KnownVersions() []string {
	return []string{"11.4", "11.0", "10.11"}
}

func (mariadbEngine) Image(v string) string {
	return version.MariaDBImagePrefix + v
}

// ConfMountTarget is MariaDB's parallel-include directory. MariaDB
// reads /etc/mysql/mariadb.conf.d/*.cnf at startup; using the MariaDB
// path explicitly keeps the file out of MySQL's conf.d (where it would
// be re-read after an engine swap and conflict with the MySQL config).
func (mariadbEngine) ConfMountTarget() string {
	return "/etc/mysql/mariadb.conf.d/locorum.cnf"
}

func (e mariadbEngine) ContainerSpec(site *types.Site, homeDir string) docker.ContainerSpec {
	name := docker.SiteContainerName(site.Slug, "database")
	netName := docker.SiteNetworkName(site.Slug)
	dbConfPath := filepath.Join(homeDir, ".locorum", "config", "dbengine", "mariadb", "locorum.cnf")
	return docker.ContainerSpec{
		Name:  name,
		Image: e.Image(dbVersionFor(site)),
		Tty:   true,
		// MariaDB's startup script honours the same flags as MySQL.
		Cmd:    []string{"mariadbd", "--innodb-flush-method=fsync"},
		Labels: docker.PlatformLabels(docker.RoleDatabase, site.Slug, version.Version),
		Env: []string{
			"MARIADB_DATABASE=wordpress",
			"MARIADB_USER=wordpress",
			// MYSQL_* names are honoured by the mariadb image as
			// historical aliases; setting both keeps healthchecks /
			// snapshot helpers engine-agnostic.
			"MYSQL_DATABASE=wordpress",
			"MYSQL_USER=wordpress",
		},
		EnvSecrets: []docker.EnvSecret{
			{Key: "MARIADB_ROOT_PASSWORD", Value: site.DBPassword},
			{Key: "MARIADB_PASSWORD", Value: site.DBPassword},
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
			// `mariadb-admin` is the canonical name in MariaDB 11; the
			// `mysqladmin` symlink also works but emits a deprecation
			// warning that's hostile in healthcheck logs.
			Test:        []string{"CMD-SHELL", "MYSQL_PWD=\"$MARIADB_ROOT_PASSWORD\" mariadb-admin ping -h 127.0.0.1 -uroot --silent"},
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
			// 1 GiB: matches MySQL — both engines share the same WP-shaped
			// workload and the same default InnoDB buffer footprint.
			MemoryLimit: 1024 << 20,
		},
		Init:    true,
		Restart: docker.RestartNo,
	}
}

func (e mariadbEngine) Snapshot(ctx context.Context, ex Execer, site *types.Site, w io.Writer) (int64, error) {
	cn := docker.SiteContainerName(site.Slug, "database")
	cmd := []string{
		"sh", "-c",
		// `mariadb-dump` is canonical in MariaDB 11; it accepts the
		// same flag set as mysqldump.
		"MYSQL_PWD=\"$MARIADB_ROOT_PASSWORD\" mariadb-dump -uroot " +
			"--single-transaction --quick --default-character-set=utf8mb4 " +
			"--add-drop-table --routines --triggers --events --hex-blob " +
			"--no-tablespaces wordpress",
	}
	cw := &countWriter{w: w}
	exit, err := ex.ExecInContainerWriter(ctx, cn, docker.ExecOptions{Cmd: cmd}, cw, nil)
	if err != nil {
		return cw.n, fmt.Errorf("mariadb-dump exec: %w", err)
	}
	if exit != 0 {
		return cw.n, fmt.Errorf("mariadb-dump exited %d", exit)
	}
	if cw.n == 0 {
		return 0, errors.New("mariadb-dump produced no output")
	}
	return cw.n, nil
}

func (e mariadbEngine) Restore(ctx context.Context, ex Execer, site *types.Site, r io.Reader) error {
	cn := docker.SiteContainerName(site.Slug, "database")
	cmd := []string{
		"sh", "-c",
		"MYSQL_PWD=\"$MARIADB_ROOT_PASSWORD\" mariadb -uroot --default-character-set=utf8mb4 wordpress",
	}
	exit, err := ex.ExecInContainerWriterStdin(ctx, cn, docker.ExecOptions{Cmd: cmd}, r, nil, nil)
	if err != nil {
		return fmt.Errorf("mariadb restore: %w", err)
	}
	if exit != 0 {
		return fmt.Errorf("mariadb restore exited %d", exit)
	}
	return nil
}

// Filters extends the MySQL chain with MariaDB-specific quirks.
func (mariadbEngine) Filters() []ImportFilter {
	out := make([]ImportFilter, 0, len(mysqlBaseFilters)+len(mariadbExtraFilters))
	out = append(out, mysqlBaseFilters...)
	out = append(out, mariadbExtraFilters...)
	return out
}

// UpgradeAllowed: MariaDB allows minor-version-forward (10.11 → 11.0 →
// 11.4) but in-place downgrades aren't supported. The migrate flow
// covers the unsupported cases.
func (mariadbEngine) UpgradeAllowed(from, to string) bool {
	if from == to || from == "" {
		return true
	}
	// Forward-only across the 10.x → 11.x boundary (MariaDB 11 reads 10.x
	// data files; the reverse is not supported).
	if isVersionAtLeast(to, from) {
		return true
	}
	return false
}

func (e mariadbEngine) ConnectionURL(host, port string, site *types.Site) string {
	if port == "" {
		port = strconv.Itoa(e.DefaultPort())
	}
	// "mysql://" is the conventional URL scheme for MariaDB clients too.
	return fmt.Sprintf("mysql://wordpress:%s@%s:%s/wordpress", site.DBPassword, host, port)
}

// mariadbExtraFilters are MariaDB-specific producer artefacts the import
// must strip / rewrite.
var mariadbExtraFilters = []ImportFilter{
	{
		// MariaDB 10.6+ prefixes mysqldump output with a conditional
		// comment. No MySQL server interprets it; stripping is safe.
		Name: "drop MariaDB sandbox comment",
		Pat:  regexp.MustCompile(`/\*!9{6}\\?- enable the sandbox mode \*/`),
		Fn:   func(_ []byte) []byte { return nil },
	},
}

// isVersionAtLeast reports whether `cand` >= `minVal` in dotted-decimal
// ordering. Non-numeric components compare lexicographically (rare
// enough not to matter in practice).
func isVersionAtLeast(cand, minVal string) bool {
	ca := splitVersion(cand)
	mi := splitVersion(minVal)
	for i := 0; i < len(ca) || i < len(mi); i++ {
		var c, m int
		if i < len(ca) {
			c = ca[i]
		}
		if i < len(mi) {
			m = mi[i]
		}
		if c != m {
			return c >= m
		}
	}
	return true
}

func splitVersion(s string) []int {
	out := []int{}
	cur := 0
	hasDigit := false
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			if hasDigit {
				out = append(out, cur)
			}
			cur = 0
			hasDigit = false
			continue
		}
		ch := s[i]
		if ch < '0' || ch > '9' {
			// Treat trailing pre-release tags ("11.4-rc1") as the
			// numeric portion seen so far. Adequate for the small set of
			// MariaDB versions we support.
			if hasDigit {
				out = append(out, cur)
			}
			return out
		}
		cur = cur*10 + int(ch-'0')
		hasDigit = true
	}
	return out
}
