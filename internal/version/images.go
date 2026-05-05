package version

import (
	"strconv"
	"strings"
)

// Pinned Docker images used by Locorum. Centralised so upgrades are atomic
// across the codebase and integration tests pin to the same versions.
const (
	TraefikImage = "traefik:v3.5"
	NginxImage   = "nginx:1.28-alpine"
	ApacheImage  = "httpd:2.4-alpine"
	MailhogImage = "mailhog/mailhog"
	AdminerImage = "adminer:latest"
	AlpineImage  = "alpine:3"

	// Per-site backend images get the user-configurable version suffix appended.
	WodbyPHPImagePrefix = "wodby/php:"
	MySQLImagePrefix    = "mysql:"
	MariaDBImagePrefix  = "mariadb:"
	RedisImagePrefix    = "redis:"
	RedisImageSuffix    = "-alpine"
)

// MinSupportedDockerServerMajor / MinSupportedDockerServerMinor are the
// Docker daemon version below which we surface a "Docker too old" warning
// in the system-health panel. Engine versions ≥ 24.0 carry the BuildKit
// + healthcheck features Locorum relies on; older daemons still mostly
// work but ship subtle incompatibilities (esp. with Compose v2 and the
// modern image SBOM). 24.0 dropped 2023-06; almost three years of slack.
const (
	MinSupportedDockerServerMajor = 24
	MinSupportedDockerServerMinor = 0
)

// DockerServerVersion is a parsed Major.Minor.Patch view of a daemon's
// `ServerVersion` string. We hand-roll this rather than pulling in a
// semver library — the daemon's reported version is always in
// `<major>.<minor>.<patch>` shape (Docker's own VERSION file enforces it
// across the moby/docker repo).
type DockerServerVersion struct {
	Major, Minor, Patch int
	Suffix              string // any non-numeric trailer (e.g. "-rc1", "-dev")
}

// IsZero reports whether v is the unparsed zero value.
func (v DockerServerVersion) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0 && v.Suffix == ""
}

// LessThan reports whether v < (major.minor) (patch is ignored for the
// minimum-version gate; we never want to warn over a patch-level bump).
func (v DockerServerVersion) LessThan(major, minor int) bool {
	if v.Major != major {
		return v.Major < major
	}
	return v.Minor < minor
}

// ParseDockerServer splits a Docker daemon ServerVersion string into a
// DockerServerVersion. Returns the zero value plus the original string as
// .Suffix when parsing fails — callers can use IsZero() to detect that.
//
// Accepted shapes (Docker's actual outputs over the years):
//
//	"24.0.7"          → 24, 0, 7
//	"25.0.3-rc1"      → 25, 0, 3, suffix "-rc1"
//	"20.10.21+azure"  → 20, 10, 21, suffix "+azure"
//	"dev"             → zero value, suffix "dev"
func ParseDockerServer(s string) DockerServerVersion {
	out := DockerServerVersion{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	// Split on the first non-digit-non-dot byte. Everything to the right
	// is the suffix; everything to the left is the dotted version triple.
	i := 0
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == '.' {
			i++
			continue
		}
		break
	}
	num := s[:i]
	out.Suffix = s[i:]
	parts := strings.Split(num, ".")
	if len(parts) >= 1 {
		out.Major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		out.Minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		out.Patch, _ = strconv.Atoi(parts[2])
	}
	return out
}
