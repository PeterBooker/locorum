package sitesteps

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/PeterBooker/locorum/internal/docker"
)

// removeIgnoringMissing removes each path, swallowing fs.ErrNotExist.
// Returns the first non-not-exist error encountered.
func removeIgnoringMissing(paths ...string) error {
	var firstErr error
	for _, p := range paths {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove %s: %w", p, err)
			}
		}
	}
	return firstErr
}

// removeVolumeByLabel asks the Engine for any volume tagged with the
// site-data role for the given slug and removes them. Implemented via the
// Engine's existing label-listing primitives so a fake engine doesn't need
// a separate volume-list method for tests.
func removeVolumeByLabel(ctx context.Context, eng docker.Engine, slug string) error {
	// Engine doesn't expose a typed volumes-by-label primitive yet; the
	// concrete *docker.Docker has one. Our purge path is reached via the
	// concrete engine.
	if dconc, ok := eng.(*docker.Docker); ok {
		return dconc.RemoveVolumesByLabel(ctx, map[string]string{
			docker.LabelPlatform: docker.PlatformValue,
			docker.LabelSite:     slug,
			docker.LabelRole:     docker.RoleDatabaseData,
		})
	}
	// Fakes don't track volumes — nothing to remove.
	return nil
}
