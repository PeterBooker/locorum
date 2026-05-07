package traefik

import (
	"errors"
	"strings"

	"github.com/PeterBooker/locorum/internal/router"
)

// classifyDockerStartError translates the opaque Docker CreateContainer /
// ContainerStart error string into one of our sentinels when the pattern
// is recognisable. Returns the original error untouched when nothing
// matches.
//
// Daemon error strings vary across Docker SDK versions and platforms; we
// match on the substrings the SDK consistently surfaces:
//
//   - "address already in use"  (Linux userland-proxy)
//   - "port is already allocated" (Docker Engine API)
//   - "bind: An attempt was made..." (Windows Docker Desktop)
//
// Update this list when a new variant shows up rather than spraying string
// matches across the codebase.
func classifyDockerStartError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "address already in use"),
		strings.Contains(msg, "port is already allocated"),
		strings.Contains(msg, "only one usage of each socket address"),
		strings.Contains(msg, "bind: an attempt was made"):
		return errors.Join(router.ErrPortInUse, err)
	}
	return err
}
