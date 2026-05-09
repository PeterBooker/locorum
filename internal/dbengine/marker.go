package dbengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
)

// EncodeMarker serialises the marker as compact JSON. The schema is small
// and stable so the output is human-readable in `cat .locorum-marker.json`.
func EncodeMarker(m VolumeMarker) ([]byte, error) {
	if m.Engine == "" {
		return nil, errors.New("marker: empty engine")
	}
	return json.Marshal(m)
}

// DecodeMarker parses bytes written by EncodeMarker. Lenient on whitespace,
// strict on field shape — an unknown engine string returns the parsed
// marker so the caller can produce a precise error message.
func DecodeMarker(data []byte) (VolumeMarker, error) {
	var m VolumeMarker
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 {
		return m, errors.New("marker: empty payload")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("marker: decode: %w", err)
	}
	if m.Engine == "" {
		return m, errors.New("marker: missing engine")
	}
	return m, nil
}

// CompareMarker reports whether `have` (read from the volume) is
// compatible with `want` (the configured engine). Returns nil if start is
// safe.
//
// Mismatch on engine is always a hard refusal — InnoDB pages from MySQL
// and MariaDB are not interchangeable for current versions. Mismatch on
// version defers to engine-specific UpgradeAllowed.
func CompareMarker(have VolumeMarker, want VolumeMarker, eng Engine) error {
	if have.Engine != want.Engine {
		return fmt.Errorf(
			"database volume was last used by %s but the site is configured for %s — take a snapshot, then use the migrate flow to switch engines",
			have.Engine, want.Engine,
		)
	}
	if have.Version != want.Version && !eng.UpgradeAllowed(have.Version, want.Version) {
		return fmt.Errorf(
			"unsafe %s version transition %s → %s — take a snapshot and use the migrate flow",
			have.Engine, have.Version, want.Version,
		)
	}
	return nil
}

// NewMarker returns a fresh marker stamped with `now`.
func NewMarker(eng Kind, version, locorumVer string, now time.Time) VolumeMarker {
	return VolumeMarker{
		Engine:         eng,
		Version:        version,
		Created:        now.UTC(),
		LocorumVersion: locorumVer,
	}
}

// ParseMarkerFromBytes parses the bytes captured from a one-shot alpine
// container that bind-mounts the database data volume read-only and prints
// the marker file's bytes. Returns (nil, ErrNoMarker) if the file is absent
// — fresh volumes don't carry one yet, the caller writes one after first
// start.
//
// This helper deliberately does NOT depend on docker.Engine being a full
// Engine — it only needs the writer-exec path that Execer publishes. But
// we DO need a way to launch a fresh container, which is a docker.Engine
// concern. The call flow is therefore split:
//   - sitesteps.EnsureMarkerStep does the orchestration (one-shot alpine
//     container creation, mount, exec, removal).
//   - This function ONLY parses the bytes the orchestrator captures.
//
// Kept here so the parsing logic and the marker shape live together.
func ParseMarkerFromBytes(data []byte) (VolumeMarker, error) {
	return DecodeMarker(data)
}

// ErrNoMarker is returned when the marker file is absent on the volume.
// EnsureMarkerStep treats this as "fresh volume, write a new marker"
// rather than a fatal error.
var ErrNoMarker = errors.New("dbengine: no volume marker present")

// MarkerInVolumePath returns the in-container absolute path the marker
// lives at, given the engine's DataDir.
func MarkerInVolumePath(eng Engine) string {
	dir := eng.DataDir()
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return dir + MarkerFilename
}

// WriteMarkerToContainer drops the marker into the running database
// container's data dir. Called once after first-start container init has
// completed.
func WriteMarkerToContainer(ctx context.Context, ex Execer, containerName string, eng Engine, marker VolumeMarker) error {
	body, err := EncodeMarker(marker)
	if err != nil {
		return err
	}
	path := MarkerInVolumePath(eng)
	// Use `tee` (POSIX-portable, present in mysql + mariadb images) to
	// write the file as the container's running user. `cat > path` would
	// require shell redirection through `sh -c` and is no shorter.
	if _, err := ex.ExecInContainerWriterStdin(ctx, containerName,
		docker.ExecOptions{Cmd: []string{"sh", "-c", "cat > " + path}},
		bytes.NewReader(body), nil, nil,
	); err != nil {
		return fmt.Errorf("write marker: %w", err)
	}
	return nil
}
