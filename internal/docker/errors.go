package docker

import "errors"

// Engine error sentinels. Callers can branch with errors.Is(err, …); humans
// see the wrapped context via fmt.Errorf("%w") chains. New code should not
// compare against Docker SDK errors directly — wrap them in one of these.
var (
	// ErrNotFound is returned by Engine methods when an inspected resource
	// does not exist. Wraps Docker SDK 404s.
	ErrNotFound = errors.New("docker resource not found")

	// ErrAlreadyExists is returned when a Create call raced with another
	// caller and the resource is already present. Idempotent helpers like
	// EnsureContainer translate this into a no-op; callers see it only when
	// they invoke a primitive create directly.
	ErrAlreadyExists = errors.New("docker resource already exists")

	// ErrContainerNotReady is returned by WaitReady when a container does
	// not become healthy within its deadline. The wrapped error message
	// includes the last N lines of the container's logs.
	ErrContainerNotReady = errors.New("container not ready")

	// ErrTransient marks an error the engine considers retryable but has
	// exhausted its retry budget on. Wraps the original cause; callers
	// should treat it as a final failure but may surface a "this might be
	// flaky, try again later" hint.
	ErrTransient = errors.New("transient docker error")

	// ErrSpecAmbiguous is returned by spec validation when a Mount has more
	// than one of {Bind, Volume, Tmpfs} populated, or none of them.
	ErrSpecAmbiguous = errors.New("mount spec must have exactly one of bind/volume/tmpfs")

	// ErrDaemonUnreachable is returned by Ping (and wrapped by lifecycle
	// methods) when the Docker daemon socket is unreachable — Docker
	// Desktop closed, the systemd unit stopped, the user's group is wrong,
	// etc. UI code branches on errors.Is(err, ErrDaemonUnreachable) to
	// surface a banner action ("Show how to start Docker") instead of a
	// raw stack trace.
	ErrDaemonUnreachable = errors.New("docker daemon unreachable")
)
