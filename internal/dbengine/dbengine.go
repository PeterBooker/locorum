// Package dbengine is the per-engine plug-in surface for Locorum's
// database story. Every database touchpoint outside this package — site
// container creation, snapshot, restore, import filtering, version
// transitions — flows through dbengine.For(site.DBEngine) so the rest of
// the codebase stays engine-agnostic.
//
// Two engines are supported today: MySQL and MariaDB. A third can be
// added by writing one new file (mysql.go is the canonical reference) and
// extending AllKinds.
package dbengine

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/types"
)

// Kind is the database engine identifier persisted on Site.DBEngine and
// embedded into snapshot filenames. Stable string — never rename, never
// change capitalisation, or shipped snapshots stop matching their site.
type Kind string

const (
	MySQL   Kind = "mysql"
	MariaDB Kind = "mariadb"
)

// Default is the engine new sites land on when the user accepts the
// defaults. Mirrors the historical behaviour (MySQL pre-multi-engine).
const Default = MySQL

// AllKinds lists every supported engine in stable display order. The UI
// dropdown reads this so adding a new engine touches one file.
func AllKinds() []Kind { return []Kind{MySQL, MariaDB} }

// IsValid reports whether k is a known engine kind. Used at the storage
// boundary to fail fast on a corrupted DB row.
func IsValid(k Kind) bool {
	for _, v := range AllKinds() {
		if v == k {
			return true
		}
	}
	return false
}

// Execer is the slice of docker functionality engine implementations call
// for snapshot / restore. Production: *docker.Docker. Tests: an in-memory
// fake under internal/dbengine/fake.
//
// The interface is deliberately tiny — keeping the surface small means
// swapping engines or adding a fake doesn't ripple through callers.
type Execer interface {
	ExecInContainerWriter(ctx context.Context, name string, opts docker.ExecOptions, stdoutW, stderrW io.Writer) (int, error)
	ExecInContainerWriterStdin(ctx context.Context, name string, opts docker.ExecOptions, stdin io.Reader, stdoutW, stderrW io.Writer) (int, error)
}

// ImportFilter is a single line-rewriting rule applied to a streamed SQL
// dump during ImportDB. Each engine publishes its own chain via Filters().
//
// Filters are deterministic and side-effect-free. They run in declaration
// order. Returning nil from Fn drops the line entirely; returning a
// different slice replaces it; returning the input unchanged forwards it.
type ImportFilter struct {
	Name string
	Pat  *regexp.Regexp
	Fn   func(line []byte) []byte
}

// VolumeMarker is the on-volume signature an engine writes on first init,
// checked on subsequent starts to detect engine/version drift (LEARNINGS
// §4.6 / F8). Encoded as JSON so a future field addition is forward
// compatible — older Locorums round-trip unknown fields.
type VolumeMarker struct {
	Engine         Kind      `json:"engine"`
	Version        string    `json:"version"`
	Created        time.Time `json:"created"`
	LocorumVersion string    `json:"locorum_version"`
}

// MarkerFilename is the filename written into the database data volume so
// the marker can be checked without booting the database. Same name across
// engines so EnsureMarkerStep doesn't need engine-specific paths.
const MarkerFilename = ".locorum-marker.json"

// Engine is the per-engine plug-in surface. Implementations live in
// mysql.go and mariadb.go; tests substitute fake.Engine.
type Engine interface {
	// Kind returns the stable engine identifier.
	Kind() Kind

	// DefaultPort is the engine's TCP port inside its container. Both
	// MySQL and MariaDB use 3306; declared per-engine so a future engine
	// drops in cleanly.
	DefaultPort() int

	// Image returns the Docker image reference for the given engine
	// version. Centralising this here means a single point of update when
	// tags rotate.
	Image(version string) string

	// DataDir is the in-container path of the engine's data directory —
	// used by the volume-marker helper to know where to read the marker
	// from.
	DataDir() string

	// DefaultVersion is the version a new site lands on. Latest stable
	// supported by Locorum.
	DefaultVersion() string

	// KnownVersions is the ordered (newest first) list shown in the UI
	// dropdown. The list is finite by design — surfacing every tag that
	// Docker Hub publishes is hostile UX.
	KnownVersions() []string

	// ContainerSpec returns the docker.ContainerSpec for the site's
	// database container. Mirrors the discipline of internal/docker
	// builders: hardened security defaults, EnvSecret for the password,
	// healthcheck appropriate to the engine.
	ContainerSpec(site *types.Site, homeDir string) docker.ContainerSpec

	// Snapshot streams a logical dump of the site's database into w.
	// Implementations call ex.ExecInContainerWriter against the running
	// database container — bytes flow direct from `mysqldump` into w,
	// never landing on disk in plaintext. Returns the byte count written
	// to w and any error.
	Snapshot(ctx context.Context, ex Execer, site *types.Site, w io.Writer) (int64, error)

	// Restore streams a logical dump from r back into the site's
	// database. Idempotent at the SQL level (uses --add-drop-table style
	// dumps so repeated restores are clean).
	Restore(ctx context.Context, ex Execer, site *types.Site, r io.Reader) error

	// Filters is the line-rewriting chain applied during import.
	Filters() []ImportFilter

	// UpgradeAllowed reports whether moving this engine from `from` to
	// `to` is safe in-place (no volume migration needed). False means the
	// caller must take the snapshot+purge+restore path. Same engine only
	// — engine swaps always require migration.
	UpgradeAllowed(from, to string) bool

	// ConnectionURL returns a "mysql://user:pass@host:port/db" style URL
	// for surfacing in the DB Credentials panel when host-port publishing
	// is enabled. host typically "127.0.0.1", port from the published
	// host port.
	ConnectionURL(host, port string, site *types.Site) string

	// MariaDBConfDir / MySQLConfDir handling — engines that share the
	// MySQL conf format publish their target path here so the dev-tuned
	// config bind mounts at the right location.
	ConfMountTarget() string
}

// For returns the engine for kind. Returns an error for an unknown kind so
// callers handle a corrupted DB row deterministically rather than crashing.
func For(kind Kind) (Engine, error) {
	switch kind {
	case MySQL:
		return mysqlEngine{}, nil
	case MariaDB:
		return mariadbEngine{}, nil
	}
	return nil, fmt.Errorf("dbengine: unknown engine %q", kind)
}

// MustFor is For with panic-on-error. Use only at boundaries where the
// caller already validated the kind via IsValid.
func MustFor(kind Kind) Engine {
	eng, err := For(kind)
	if err != nil {
		panic(err)
	}
	return eng
}

// Resolve returns the engine for site, falling back to Default if the
// site has no engine recorded (a row from before the multi-engine
// migration).
func Resolve(site *types.Site) Engine {
	k := Kind(site.DBEngine)
	if !IsValid(k) {
		k = Default
	}
	return MustFor(k)
}
