package docker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"
)

// ContainerSpec is the full declarative description of a container Locorum
// intends to create. It replaces the loose four-arg createContainer signature
// and lets the engine make idempotency, security, and resource decisions
// from a single source of truth.
//
// Zero values are deliberately safe: an empty Security is "drop ALL caps,
// no-new-privileges"; an empty Resources is "10m × 3 log files, 1024 pids";
// an empty Restart is "no". See specs_builders.go for the role-specific
// presets.
type ContainerSpec struct {
	Name       string
	Image      string
	Cmd        []string
	Entrypoint []string
	Env        []string
	EnvSecrets []EnvSecret
	User       string
	WorkingDir string
	Tty        bool
	Labels     map[string]string

	Ports      []PortMap
	Mounts     []Mount
	Networks   []NetworkAttachment
	ExtraHosts []string

	Healthcheck *Healthcheck
	Resources   Resources
	Security    SecurityOptions
	Init        bool
	Restart     RestartPolicy
}

// EnvSecret is an environment variable whose value must never appear in
// log lines, error messages, or telemetry. The engine sets it on the
// container exactly like Env, but redacts it from anything Locorum itself
// emits. We cannot hide it from `docker inspect`; that's documented.
type EnvSecret struct {
	Key   string
	Value string
}

// Healthcheck describes the container readiness probe Docker runs.
// WaitReady polls the inspected health state on a tighter cadence than
// Docker's own checks so the GUI surfaces transitions immediately.
type Healthcheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	Retries     int
	StartPeriod time.Duration
}

// Resources caps a container's footprint. Defaults applied by the engine
// when a field is left zero are:
//
//	LogMaxSize:  "10m"
//	LogMaxFiles: 3
//	PidsLimit:   1024
//	MemoryLimit: 0  (unlimited; revisited per role in a follow-up)
type Resources struct {
	MemoryLimit int64
	CPUShares   int64
	LogMaxSize  string
	LogMaxFiles int
	PidsLimit   int64
	Ulimits     []Ulimit
}

// Ulimit is a single getrlimit(2)-style limit applied to the container's
// processes. Mirrors the Docker SDK shape but kept inside our package so
// callers don't import docker/api/types directly.
type Ulimit struct {
	Name string
	Soft int64
	Hard int64
}

// SecurityOptions hardens the container kernel surface. The engine treats
// a zero value as "CapDrop=ALL, NoNewPrivileges=true" — opt out by setting
// fields explicitly. Only opt out with a recorded reason.
type SecurityOptions struct {
	CapDrop         []string
	CapAdd          []string
	NoNewPrivileges bool
	ReadOnlyRootFS  bool
	SecurityOpt     []string
}

// RestartPolicy governs the Docker daemon's container-level restart loop.
// Locorum manages lifecycle itself, so the default is "no" — a restart
// loop on a service container hides startup bugs from the user.
type RestartPolicy string

const (
	RestartNo            RestartPolicy = "no"
	RestartOnFailure     RestartPolicy = "on-failure"
	RestartUnlessStopped RestartPolicy = "unless-stopped"
)

// PortMap is one host→container port binding. HostIP defaults to "0.0.0.0";
// set "127.0.0.1" for loopback-only admin endpoints (e.g. router admin API).
type PortMap struct {
	HostIP        string
	HostPort      string
	ContainerPort string
	Proto         string // "tcp" (default) or "udp"
}

// Mount is the typed parent for the three mount kinds Locorum uses. Exactly
// one of BindMount/VolumeMount/TmpfsMount fields is non-zero; the engine
// rejects ambiguity at apply time.
type Mount struct {
	Bind   *BindMount
	Volume *VolumeMount
	Tmpfs  *TmpfsMount
}

// BindMount mounts a host path into the container.
type BindMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

// VolumeMount mounts a Docker named volume.
type VolumeMount struct {
	Name     string
	Target   string
	ReadOnly bool
}

// TmpfsMount mounts a tmpfs at the given target path.
type TmpfsMount struct {
	Target  string
	Options string // "size=64m,uid=1000" etc.
}

// NetworkAttachment associates the container with a Docker network plus
// optional aliases. The first attachment is treated as the primary network.
type NetworkAttachment struct {
	Network string
	Aliases []string
}

// NetworkSpec describes a Docker network we want to ensure exists.
type NetworkSpec struct {
	Name     string
	Internal bool
	Driver   string // empty → "bridge"
	Labels   map[string]string
}

// VolumeSpec describes a Docker named volume we want to ensure exists.
type VolumeSpec struct {
	Name   string
	Labels map[string]string
}

// ConfigHash returns a stable SHA-256 of the spec's intent. Two specs that
// would produce the same Docker container shape (identical image, env,
// mounts, security options, etc.) hash identically; any change that would
// require recreating the container changes the hash.
//
// The hash deliberately ignores fields that vary independently of intent:
//   - Map iteration order in Labels and Env (canonicalised by sorting).
//   - LabelVersion (a Locorum upgrade alone must not force every container
//     to recreate; we hash *intent*, not Locorum's own version stamp).
//   - LabelConfigHash itself (would otherwise be self-referential).
//   - EnvSecret values (the keys are part of the intent, the secret values
//     belong to the runtime environment, and rotating a password should
//     not be conflated with shape drift).
func (s ContainerSpec) ConfigHash() string {
	type canonicalEnvSecret struct{ Key string }
	type canonicalSpec struct {
		Image       string
		Cmd         []string
		Entrypoint  []string
		Env         []string
		EnvSecrets  []canonicalEnvSecret
		User        string
		WorkingDir  string
		Tty         bool
		Labels      [][2]string
		Ports       []PortMap
		Mounts      []Mount
		Networks    []NetworkAttachment
		ExtraHosts  []string
		Healthcheck *Healthcheck
		Resources   Resources
		Security    SecurityOptions
		Init        bool
		Restart     RestartPolicy
	}

	envCopy := append([]string(nil), s.Env...)
	sort.Strings(envCopy)

	secretsCopy := make([]canonicalEnvSecret, 0, len(s.EnvSecrets))
	for _, sec := range s.EnvSecrets {
		secretsCopy = append(secretsCopy, canonicalEnvSecret{Key: sec.Key})
	}
	sort.Slice(secretsCopy, func(i, j int) bool { return secretsCopy[i].Key < secretsCopy[j].Key })

	labels := make([][2]string, 0, len(s.Labels))
	for k, v := range s.Labels {
		if k == LabelVersion || k == LabelConfigHash {
			continue
		}
		labels = append(labels, [2]string{k, v})
	}
	sort.Slice(labels, func(i, j int) bool {
		if labels[i][0] != labels[j][0] {
			return labels[i][0] < labels[j][0]
		}
		return labels[i][1] < labels[j][1]
	})

	c := canonicalSpec{
		Image:       s.Image,
		Cmd:         s.Cmd,
		Entrypoint:  s.Entrypoint,
		Env:         envCopy,
		EnvSecrets:  secretsCopy,
		User:        s.User,
		WorkingDir:  s.WorkingDir,
		Tty:         s.Tty,
		Labels:      labels,
		Ports:       s.Ports,
		Mounts:      s.Mounts,
		Networks:    s.Networks,
		ExtraHosts:  append([]string(nil), s.ExtraHosts...),
		Healthcheck: s.Healthcheck,
		Resources:   s.Resources,
		Security:    s.Security,
		Init:        s.Init,
		Restart:     s.Restart,
	}

	// Canonicalise list-of-strings ordering for fields where order is
	// semantically irrelevant. ExtraHosts and CapAdd/CapDrop are set-shaped.
	sort.Strings(c.ExtraHosts)
	c.Security.CapDrop = sortedCopy(c.Security.CapDrop)
	c.Security.CapAdd = sortedCopy(c.Security.CapAdd)
	c.Security.SecurityOpt = sortedCopy(c.Security.SecurityOpt)

	buf, err := json.Marshal(c)
	if err != nil {
		// json.Marshal of a fully-typed value cannot fail in practice; if it
		// somehow does, hash the empty input rather than panicking — the
		// resulting hash will trip the "drift" path and force a recreate,
		// which is the safe failure mode.
		buf = []byte{}
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
