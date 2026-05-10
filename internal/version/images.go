package version

import (
	"strconv"
	"strings"
)

// Pinned Docker images used by Locorum. Centralised so upgrades are
// atomic and Renovate (`renovate: image=...` markers) opens one PR per
// tag bump. TESTING.md §3.10.4 plans a follow-up to pin digests as well.
const (
	// renovate: image=traefik versioning=docker
	TraefikImage = "traefik:v3.5"
	// renovate: image=nginx versioning=docker
	NginxImage = "nginx:1.28-alpine"
	// renovate: image=httpd versioning=docker
	ApacheImage = "httpd:2.4-alpine"
	// renovate: image=mailhog/mailhog versioning=docker
	MailhogImage = "mailhog/mailhog"
	// renovate: image=adminer versioning=docker
	// Pinned to a major+minor tag (not :latest) so a malicious upstream
	// re-tag of `:latest` cannot land in our admin DB UI silently. Bump
	// in lockstep with the renovate PR.
	AdminerImage = "adminer:5.4.2-standalone"
	// renovate: image=alpine versioning=docker
	AlpineImage = "alpine:3"

	// Per-site backend images get the user-configurable version suffix appended.
	WodbyPHPImagePrefix = "wodby/php:"
	MySQLImagePrefix    = "mysql:"
	MariaDBImagePrefix  = "mariadb:"
	RedisImagePrefix    = "redis:"
	RedisImageSuffix    = "-alpine"
)

// WP-CLI is bundled as a phar binary downloaded once at app start and
// bind-mounted into every PHP container at /usr/local/bin/wp. The wodby
// image does not carry wp-cli, but every Locorum lifecycle path that
// touches WordPress (`wp core install` after StartSite, search-replace
// after DB import, multisite convert) needs it. Pinning the version +
// SHA-512 here means:
//
//   - One PR (Renovate-style) bumps the binary atomically — no drift.
//   - The on-disk phar is content-addressed: a corrupted or tampered
//     download is detected and refused before the file is moved into
//     place (see internal/wpcli.EnsurePhar).
//   - The release URL is reproducible: GitHub's release-asset URL is
//     immutable per tag, so verification is deterministic across hosts.
//
// To bump:
//
//	curl -sSL https://github.com/wp-cli/wp-cli/releases/download/vX.Y.Z/wp-cli-X.Y.Z.phar | sha512sum
//
// then update both constants and the test fixture.
const (
	WPCliVersion = "v2.12.0"
	WPCliSHA512  = "be928f6b8ca1e8dfb9d2f4b75a13aa4aee0896f8a9a0a1c45cd5d2c98605e6172e6d014dda2e27f88c98befc16c040cbb2bd1bfa121510ea5cdf5f6a30fe8832"
)

// WPCliDownloadURL returns the canonical GitHub Releases URL for the
// pinned wp-cli phar. Centralised so the install logic and the
// docs/build pipeline share one definition.
func WPCliDownloadURL() string {
	num := strings.TrimPrefix(WPCliVersion, "v")
	return "https://github.com/wp-cli/wp-cli/releases/download/" + WPCliVersion + "/wp-cli-" + num + ".phar"
}

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
