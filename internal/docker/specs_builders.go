package docker

import (
	"os"
	"path/filepath"
	"time"

	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/version"
)

// SiteContainerName is the canonical helper for per-site container names.
// One place to change if we ever rename the prefix.
func SiteContainerName(slug, role string) string {
	return "locorum-" + slug + "-" + role
}

// SPXKeyINIPath is the canonical on-disk location of the per-site
// INI fragment that supplies SPX with its `spx.http_key` secret. The
// PHPSpec bind-mounts this file when SPX is enabled; the
// EnsureSPXStep generates it before the container starts.
//
// Centralising the path here means both producer (sitestep) and
// consumer (PHPSpec) reference one constant, so a renamed directory
// can never silently desync the two halves.
func SPXKeyINIPath(homeDir, slug string) string {
	return filepath.Join(homeDir, ".locorum", "config", "php", "spx-keys", slug+".ini")
}

// SiteNetworkName is the canonical name for a site's internal bridge network.
func SiteNetworkName(slug string) string {
	return "locorum-" + slug
}

// SiteVolumeName is the canonical name for a site's database data volume.
func SiteVolumeName(slug string) string {
	return "locorum-" + slug + "-dbdata"
}

// PHPUserGroup returns the uid:gid the PHP container should run as. On
// Windows os.Getuid()/Getgid() return -1; we fall back to 1000:1000 which
// matches the wodby image's default user.
func PHPUserGroup() (int, int) {
	uid, gid := os.Getuid(), os.Getgid()
	if uid < 0 || gid < 0 {
		return 1000, 1000
	}
	return uid, gid
}

// normaliseDocroot collapses the various forms of "WordPress is at the bind
// mount root" — empty string, "/", "." — into a canonical empty value, and
// strips a leading slash from explicit subdirectories so callers can
// concatenate without doubling slashes.
func normaliseDocroot(publicDir string) string {
	d := publicDir
	if d == "" || d == "/" || d == "." {
		return ""
	}
	for d != "" && d[0] == '/' {
		d = d[1:]
	}
	return d
}

// hardenedSecurity is the default security profile applied by every spec
// builder. CapDrop=ALL + NoNewPrivileges=true is the production-grade
// baseline; per-role caps are added back via WithCapAdd.
func hardenedSecurity(extraCapAdds ...string) SecurityOptions {
	return SecurityOptions{
		CapDrop:         []string{"ALL"},
		CapAdd:          extraCapAdds,
		NoNewPrivileges: true,
	}
}

// permissiveSecurity returns a security profile compatible with images
// whose entrypoint relies on `sudo` (the wodby/php image, in particular).
// `sudo` is a setuid binary; the kernel's no_new_privs flag explicitly
// blocks setuid-bit escalation, so we cannot keep NoNewPrivileges=true and
// still let sudo work. CapDrop is empty (not nil) so buildCaps does not
// apply the default ALL-drop — sudo additionally needs AUDIT_WRITE and
// SETPCAP, which are part of Docker's default cap set.
//
// The pragmatic tradeoff: Locorum pins the wodby image, treats it as
// trusted infrastructure, and accepts a slightly wider container surface
// for it than for the other roles.
func permissiveSecurity() SecurityOptions {
	return SecurityOptions{
		CapDrop:         []string{},
		CapAdd:          nil,
		NoNewPrivileges: false,
	}
}

// resourceDefaults returns the Resources struct every container gets unless
// the builder overrides it. Log size cap (10m × 3 = 30MB) prevents a single
// chatty container from filling /var/lib/docker.
//
// Memory limits are role-scoped: a runaway PHP plugin or aggressive WP-CRON
// can no longer OOM-kill the user's desktop. The numbers are conservative
// enough that real-world WordPress installs run comfortably without
// trip-wiring on a typical 100-page site, but tight enough that a fork-bomb
// in a hook or a hostile import is contained inside the container's cgroup.
//
// Defaults can be lifted on big-memory boxes via a future global setting
// (see internal/config); the current values are the safe-by-default cap.
func resourceDefaults() Resources {
	return roleResources(RoleDefault)
}

// roleResources returns per-role resource caps. New container builders
// should call this with their RoleX constant rather than resourceDefaults
// to pick up the right memory ceiling.
func roleResources(role Role) Resources {
	r := Resources{
		LogMaxSize:  "10m",
		LogMaxFiles: 3,
		PidsLimit:   1024,
	}
	switch role {
	case RolePHP:
		r.MemoryLimit = 512 << 20
	case RoleDatabase:
		r.MemoryLimit = 1024 << 20
	case RoleRedis:
		r.MemoryLimit = 256 << 20
	case RoleWeb, RoleMail, RoleAdminer, RoleRouter:
		r.MemoryLimit = 128 << 20
	}
	return r
}

// NginxWebSpec builds the per-site nginx web container spec.
func NginxWebSpec(site *types.Site, homeDir string) ContainerSpec {
	name := SiteContainerName(site.Slug, "web")
	netName := SiteNetworkName(site.Slug)
	configPath := filepath.Join(homeDir, ".locorum", "config", "nginx", "sites", site.Slug+".conf")
	return ContainerSpec{
		Name:   name,
		Image:  version.NginxImage,
		Tty:    true,
		Labels: PlatformLabels(RoleWeb, site.Slug, version.Version),
		Ports: []PortMap{
			{ContainerPort: "80", Proto: "tcp"},
		},
		Mounts: []Mount{
			{Bind: &BindMount{Source: configPath, Target: "/etc/nginx/nginx.conf", ReadOnly: true}},
			{Bind: &BindMount{Source: site.FilesDir, Target: "/var/www/html"}},
		},
		Networks: []NetworkAttachment{
			{Network: netName, Aliases: []string{"web"}},
			{Network: GlobalNetwork, Aliases: []string{name}},
		},
		Healthcheck: &Healthcheck{
			Test:        []string{"CMD-SHELL", "wget -qO- http://127.0.0.1/ >/dev/null 2>&1 || nginx -t"},
			Interval:    1 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     30,
			StartPeriod: 1 * time.Second,
		},
		Security:  hardenedSecurity("CHOWN", "SETGID", "SETUID", "NET_BIND_SERVICE", "DAC_OVERRIDE"),
		Resources: roleResources(RoleWeb),
		Init:      true,
		Restart:   RestartNo,
	}
}

// ApacheWebSpec builds the per-site Apache web container spec.
func ApacheWebSpec(site *types.Site, homeDir string) ContainerSpec {
	name := SiteContainerName(site.Slug, "web")
	netName := SiteNetworkName(site.Slug)
	configPath := filepath.Join(homeDir, ".locorum", "config", "apache", "sites", site.Slug+".conf")
	return ContainerSpec{
		Name:   name,
		Image:  version.ApacheImage,
		Tty:    true,
		Labels: PlatformLabels(RoleWeb, site.Slug, version.Version),
		Ports: []PortMap{
			{ContainerPort: "80", Proto: "tcp"},
		},
		Mounts: []Mount{
			{Bind: &BindMount{Source: configPath, Target: "/usr/local/apache2/conf/httpd.conf", ReadOnly: true}},
			{Bind: &BindMount{Source: site.FilesDir, Target: "/var/www/html"}},
		},
		Networks: []NetworkAttachment{
			{Network: netName, Aliases: []string{"web"}},
			{Network: GlobalNetwork, Aliases: []string{name}},
		},
		Healthcheck: &Healthcheck{
			Test:        []string{"CMD", "httpd", "-t"},
			Interval:    1 * time.Second,
			Timeout:     5 * time.Second,
			Retries:     30,
			StartPeriod: 1 * time.Second,
		},
		Security:  hardenedSecurity("CHOWN", "SETGID", "SETUID", "NET_BIND_SERVICE", "DAC_OVERRIDE"),
		Resources: roleResources(RoleWeb),
		Init:      true,
		Restart:   RestartNo,
	}
}

// WebSpec dispatches to the right builder based on site.WebServer.
func WebSpec(site *types.Site, homeDir string) ContainerSpec {
	if site.WebServer == "apache" {
		return ApacheWebSpec(site, homeDir)
	}
	return NginxWebSpec(site, homeDir)
}

// PHPSpec builds the per-site PHP-FPM container spec. Database credentials
// flow through EnvSecrets so the password is redacted from any error
// message Locorum itself emits.
//
// LOCORUM_* env vars are read at PHP request time by wp-config-locorum.php
// to resolve WP_HOME/WP_SITEURL. They participate in the container's
// config hash, so a domain or docroot change forces a recreate.
//
// SPX (php-spx) is gated by site.SPXEnabled. The spx.ini is bind-mounted
// unconditionally — it's inert until the extension loads and keeping the
// mount stable avoids hash churn when only the toggle flips. Activating
// SPX adds env vars + a per-site profile-data bind, both of which DO
// participate in the hash so the toggle correctly forces a recreate.
func PHPSpec(site *types.Site, homeDir string) ContainerSpec {
	name := SiteContainerName(site.Slug, "php")
	netName := SiteNetworkName(site.Slug)
	uid, gid := PHPUserGroup()
	phpIniPath := filepath.Join(homeDir, ".locorum", "config", "php", "php.ini")
	spxIniPath := filepath.Join(homeDir, ".locorum", "config", "php", "spx.ini")

	env := []string{
		"MYSQL_HOST=database",
		"MYSQL_DATABASE=wordpress",
		"MYSQL_USER=wordpress",
		"WP_CLI_ALLOW_ROOT=true",
		"LOCORUM_PRIMARY_URL=https://" + site.Domain,
		"LOCORUM_DOCROOT=" + normaliseDocroot(site.PublicDir),
		"LOCORUM_APPROOT=/var/www/html",
		"LOCORUM_SITE_SLUG=" + site.Slug,
		"LOCORUM_MULTISITE=" + site.Multisite,
		// wodby/php's FPM pool defaults to user/group www-data (UID 82),
		// but the container itself runs as the host user (UID 1000 →
		// wodby). With the default, FPM workers can't read 0600 files
		// owned by 1000:1000 — wp-config.php, wp-config-locorum.php,
		// import dumps, the auto-login plugin — and PHP fails with
		// "Permission denied" on require_once. Forcing the pool to
		// wodby/wodby restores the invariant the rest of the codebase
		// already assumes ("PHP runs as the host UID"); see the comments
		// in wpconfig.go, sites.go, wordpress.go, import.go.
		"PHP_FPM_USER=wodby",
		"PHP_FPM_GROUP=wodby",
	}
	secrets := []EnvSecret{
		{Key: "MYSQL_PASSWORD", Value: site.DBPassword},
	}
	// wodby/php does not ship wp-cli. The host-side phar is downloaded
	// + verified once at app start (internal/wpcli.EnsurePhar) and
	// bind-mounted read-only into every PHP container. Path must stay
	// in lockstep with wpcli.PharPath; see WPCliMountAgreement_test
	// for the assertion.
	wpcliPharHost := filepath.Join(homeDir, ".locorum", "bin", "wp")

	mounts := []Mount{
		{Bind: &BindMount{Source: phpIniPath, Target: "/usr/local/etc/php/conf.d/zzz-php.ini"}},
		{Bind: &BindMount{Source: spxIniPath, Target: "/usr/local/etc/php/conf.d/zzz-spx.ini"}},
		{Bind: &BindMount{Source: site.FilesDir, Target: "/var/www/html"}},
		{Bind: &BindMount{Source: wpcliPharHost, Target: "/usr/local/bin/wp", ReadOnly: true}},
	}

	if site.SPXEnabled {
		// PHP_EXTENSIONS_ENABLE flips the wodby image's default disable
		// list off for spx; the explicit DISABLE keeps xdebug/xhprof
		// off so a Locorum upgrade to an image that re-orders the
		// defaults can't accidentally bring them back.
		env = append(env,
			"PHP_EXTENSIONS_ENABLE=spx",
			"PHP_EXTENSIONS_DISABLE=xdebug,xhprof",
		)
		// Per-site INI override providing spx.http_key. Read-only mount
		// — the file is regenerated by the EnsureSPXStep before each
		// start so PHP-FPM never needs to write to it.
		mounts = append(mounts,
			Mount{Bind: &BindMount{
				Source:   SPXKeyINIPath(homeDir, site.Slug),
				Target:   "/usr/local/etc/php/conf.d/zzzz-spx-key.ini",
				ReadOnly: true,
			}},
			Mount{Bind: &BindMount{
				Source: filepath.Join(site.FilesDir, ".locorum", "spx"),
				Target: "/var/spx/data",
			}},
		)
	}

	return ContainerSpec{
		Name:       name,
		Image:      version.WodbyPHPImagePrefix + site.PHPVersion,
		User:       intPair(uid, gid),
		Tty:        true,
		WorkingDir: "/var/www/html",
		Labels:     PlatformLabels(RolePHP, site.Slug, version.Version),
		Env:        env,
		EnvSecrets: secrets,
		Mounts:     mounts,
		Networks: []NetworkAttachment{
			{Network: netName, Aliases: []string{"php"}},
			{Network: GlobalNetwork},
		},
		ExtraHosts: []string{site.Domain + ":host-gateway"},
		Healthcheck: &Healthcheck{
			// `pgrep php-fpm` ships in procps which the wodby base image
			// includes; CMD-SHELL keeps us shell-portable across alpine /
			// debian variants.
			Test:        []string{"CMD-SHELL", "pgrep php-fpm >/dev/null 2>&1 || pgrep -f php-fpm >/dev/null 2>&1"},
			Interval:    1 * time.Second,
			Timeout:     3 * time.Second,
			Retries:     60,
			StartPeriod: 2 * time.Second,
		},
		// wodby/php uses sudo in its entrypoint; see permissiveSecurity
		// for why we can't apply our hardened defaults here.
		Security:  permissiveSecurity(),
		Resources: roleResources(RolePHP),
		Init:      true,
		Restart:   RestartNo,
	}
}

// Database container specs live in the dbengine package — see
// internal/dbengine/{mysql,mariadb}.go. The site-spec assembler in
// internal/sites/sites.go:serviceSpecs routes through dbengine.Resolve(site).

// RedisSpec builds the per-site Redis container spec.
func RedisSpec(site *types.Site) ContainerSpec {
	name := SiteContainerName(site.Slug, "redis")
	netName := SiteNetworkName(site.Slug)
	return ContainerSpec{
		Name:   name,
		Image:  version.RedisImagePrefix + site.RedisVersion + version.RedisImageSuffix,
		Tty:    true,
		Cmd:    []string{"redis-server", "--appendonly", "yes"},
		Labels: PlatformLabels(RoleRedis, site.Slug, version.Version),
		Networks: []NetworkAttachment{
			{Network: netName, Aliases: []string{"redis"}},
		},
		Healthcheck: &Healthcheck{
			Test:        []string{"CMD-SHELL", "redis-cli ping | grep -q PONG"},
			Interval:    1 * time.Second,
			Timeout:     3 * time.Second,
			Retries:     20,
			StartPeriod: 1 * time.Second,
		},
		// redis-alpine's entrypoint starts as root, chowns /data, and gosu's
		// to the redis user. CHOWN + SETUID + SETGID are the minimum it
		// needs; DAC_OVERRIDE covers the post-chown read paths.
		Security:  hardenedSecurity("CHOWN", "SETGID", "SETUID", "DAC_OVERRIDE"),
		Resources: roleResources(RoleRedis),
		Init:      true,
		Restart:   RestartNo,
	}
}

// MailSpec builds the global mailhog container spec. Joined to the global
// network only — the router routes mail.localhost here.
func MailSpec() ContainerSpec {
	return ContainerSpec{
		Name:   "locorum-global-mail",
		Image:  version.MailhogImage,
		Tty:    true,
		Labels: PlatformLabels(RoleMail, "", version.Version),
		Ports: []PortMap{
			{ContainerPort: "1025", Proto: "tcp"},
			{ContainerPort: "8025", Proto: "tcp"},
		},
		Networks: []NetworkAttachment{
			{Network: GlobalNetwork, Aliases: []string{"mail"}},
		},
		Healthcheck: &Healthcheck{
			Test:        []string{"CMD-SHELL", "wget -qO- http://127.0.0.1:8025/ >/dev/null 2>&1"},
			Interval:    1 * time.Second,
			Timeout:     3 * time.Second,
			Retries:     20,
			StartPeriod: 1 * time.Second,
		},
		Security:  hardenedSecurity(),
		Resources: roleResources(RoleMail),
		Init:      true,
		Restart:   RestartNo,
	}
}

// AdminerSpec builds the global adminer container spec.
func AdminerSpec() ContainerSpec {
	return ContainerSpec{
		Name:   "locorum-global-adminer",
		Image:  version.AdminerImage,
		Tty:    true,
		Labels: PlatformLabels(RoleAdminer, "", version.Version),
		Env: []string{
			"ADMINER_DEFAULT_SERVER=database",
		},
		Ports: []PortMap{
			{ContainerPort: "8080", Proto: "tcp"},
		},
		Networks: []NetworkAttachment{
			{Network: GlobalNetwork, Aliases: []string{"adminer"}},
		},
		Healthcheck: &Healthcheck{
			Test:        []string{"CMD-SHELL", "wget -qO- http://127.0.0.1:8080/ >/dev/null 2>&1"},
			Interval:    1 * time.Second,
			Timeout:     3 * time.Second,
			Retries:     20,
			StartPeriod: 1 * time.Second,
		},
		Security:  hardenedSecurity(),
		Resources: roleResources(RoleAdminer),
		Init:      true,
		Restart:   RestartNo,
	}
}

// SiteNetworkSpec is the spec for a site's internal bridge network.
func SiteNetworkSpec(site *types.Site) NetworkSpec {
	return NetworkSpec{
		Name:     SiteNetworkName(site.Slug),
		Internal: true,
		Driver:   "bridge",
		Labels:   PlatformLabels(RoleSiteNetwork, site.Slug, version.Version),
	}
}

// GlobalNetworkSpec is the spec for the global bridge network.
func GlobalNetworkSpec() NetworkSpec {
	return NetworkSpec{
		Name:   GlobalNetwork,
		Driver: "bridge",
		Labels: PlatformLabels(RoleGlobalNetwork, "", version.Version),
	}
}

// SiteVolumeSpec is the spec for a site's database data volume.
func SiteVolumeSpec(site *types.Site) VolumeSpec {
	return VolumeSpec{
		Name:   SiteVolumeName(site.Slug),
		Labels: PlatformLabels(RoleDatabaseData, site.Slug, version.Version),
	}
}

func intPair(a, b int) string {
	return itoa(a) + ":" + itoa(b)
}

func itoa(i int) string {
	// Avoid a strconv import in this file. Container UID:GID never
	// exceeds the host's UID range, so a small fast-path itoa is fine.
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
