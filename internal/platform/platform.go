// Package platform identifies the host Locorum is running on. It centralises
// every signal we'd otherwise sprinkle across `runtime.GOOS` and ad-hoc
// `os.LookupEnv("WSL_DISTRO_NAME")` checks: OS, architecture, WSL2 mode,
// macOS Rosetta, sanitised username, home directory, and host paths.
//
// The package has *no* internal dependencies — it is a leaf safe to import
// from anywhere. The exported [Info] is immutable after [Init] returns; the
// runtime mutates nothing. Reads do not lock.
//
// Concurrency: [Init] is idempotent — multiple goroutines may call it; the
// first wins. [Get] returns the cached pointer with no synchronisation.
//
// Performance: cold [Init] is bounded by the [WSLInfoTimeout] context
// deadline (500 ms) but only blocks for shell-out when the cheap signals
// already say "we're in WSL". Native Linux/macOS/Windows paths run
// entirely in-memory (<10 ms).
package platform

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

// WSLInfoTimeout caps the `wslinfo --networking-mode` shell-out. On timeout
// the field stays empty; the caller is informational, not load-bearing.
const WSLInfoTimeout = 500 * time.Millisecond

// LocorumDataDirName is the subdirectory under HomeDir that stores Locorum's
// runtime state (settings DB, router config, certs, etc.). Centralised here
// so tests can override it via NewForTest.
const LocorumDataDirName = ".locorum"

// Info is an immutable snapshot of host identification produced once at
// startup. All fields are safe for concurrent reads without locking.
//
// A zero-value Info is technically valid but semantically wrong — always go
// through [Init]/[Get]/[NewForTest].
type Info struct {
	// OS mirrors runtime.GOOS at Init time.
	OS string

	// Arch mirrors runtime.GOARCH at Init time.
	Arch string

	// WSL is the zero value on non-WSL hosts; .Active is the canonical
	// "are we running inside WSL?" check.
	WSL WSLInfo

	// Username is the host login name, NFKC-normalised and stripped of
	// characters Docker rejects in container-name suffixes. Never empty
	// — falls back to "locorum" if the OS-reported name is unusable.
	// Use this anywhere a username flows into a container, label, env
	// var, or path.
	Username string

	// UID and GID are os.Getuid() / os.Getgid(); both -1 on Windows.
	UID, GID int

	// HomeDir is the user's home directory, resolved through
	// os.UserHomeDir(). On WSL with a Windows-side daemon the value is
	// the Linux-side path — translate via DockerPath when feeding it to
	// a host-Windows Docker.
	HomeDir string

	// UnderRosetta is true on macOS amd64 binaries that the kernel is
	// translating on an arm64 host. Triggers a hard-fail at startup —
	// see preflight_darwin.go.
	UnderRosetta bool

	// Hostname is best-effort; "" if os.Hostname errored.
	Hostname string
}

// WSLInfo carries the Windows-Subsystem-for-Linux specifics. Active is the
// only field code outside this package should branch on; the rest are
// diagnostic.
type WSLInfo struct {
	// Active is true when running inside a WSL2 distro (the default for
	// any Linux build under WSL). Multiple signals voted on this; see
	// detectWSL_linux.
	Active bool

	// Distro is the value of WSL_DISTRO_NAME, "" on non-WSL or when the
	// env var is missing inside the distro.
	Distro string

	// NetworkingMode is "nat" / "mirrored" / "virtioproxy" / "" if
	// `wslinfo --networking-mode` was unavailable or timed out. Only
	// populated when Active is true.
	NetworkingMode string

	// KernelRelease mirrors /proc/sys/kernel/osrelease. Diagnostic only.
	KernelRelease string
}

// pkgInfo is the package-level cached pointer. Init publishes here; Get
// reads. atomic.Pointer keeps the read path lock-free.
var pkgInfo atomic.Pointer[Info]

// Init runs once at process start. Subsequent calls are no-ops returning
// the cached *Info. Safe to call concurrently — the first goroutine wins
// and later callers receive the same pointer.
//
// The context bounds shell-out steps (today: `wslinfo`). Pass a Background
// context unless you have a specific deadline to enforce.
func Init(ctx context.Context) *Info {
	if cached := pkgInfo.Load(); cached != nil {
		return cached
	}
	info := detect(ctx)
	if pkgInfo.CompareAndSwap(nil, info) {
		return info
	}
	// Lost the race; the winner's pointer is canonical.
	return pkgInfo.Load()
}

// Get returns the previously-initialised *Info. Panics if Init has not
// been called yet — this is intentional: the rest of the codebase assumes
// platform was initialised in main, and a missing call is a wiring bug.
func Get() *Info {
	v := pkgInfo.Load()
	if v == nil {
		panic("platform: Get called before Init")
	}
	return v
}

// IsInitialized reports whether Init has been called. Tests use this to
// avoid the panic; production code should never need it.
func IsInitialized() bool {
	return pkgInfo.Load() != nil
}

// NewForTest installs the given *Info as the package-level cache,
// returning a func that restores the previous value. Tests call this
// in a defer so multiple parallel tests don't leak state to each other.
//
// IMPORTANT: this is the *only* way to override the cache after Init.
// Production code must not call it.
func NewForTest(t *Info) (restore func()) {
	prev := pkgInfo.Load()
	pkgInfo.Store(t)
	return func() {
		if prev == nil {
			pkgInfo.Store(nil)
			return
		}
		pkgInfo.Store(prev)
	}
}

// detect builds a fresh Info. Pure function from the host's current state
// — same machine, same answer.
func detect(ctx context.Context) *Info {
	uid, gid := getUIDGID()

	homeDir, _ := os.UserHomeDir()

	rawUser := osLoginName()
	username := SanitiseUsername(rawUser)

	host, _ := os.Hostname()

	info := &Info{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Username:     username,
		UID:          uid,
		GID:          gid,
		HomeDir:      homeDir,
		Hostname:     host,
		UnderRosetta: isUnderRosetta(),
	}

	info.WSL = detectWSL(ctx)

	return info
}

// getUIDGID returns os.Getuid()/Getgid() on Unix and -1,-1 on Windows.
// Wrapped so tests don't need to fork to exercise the Windows path.
func getUIDGID() (int, int) {
	uid, gid := os.Getuid(), os.Getgid()
	return uid, gid
}

// osLoginName is the best raw signal for the host login name. We try in
// order: $USER, $USERNAME (Windows convention), $LOGNAME. Empty inputs
// drop through to SanitiseUsername's fallback.
func osLoginName() string {
	for _, key := range []string{"USER", "USERNAME", "LOGNAME"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	return ""
}
