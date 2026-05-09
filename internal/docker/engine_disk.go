package docker

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker/api/types"
	"golang.org/x/sync/singleflight"
)

// DiskReport is the engine-level summary of `docker system df`. We expose a
// small, stable struct rather than the SDK's full DiskUsage so the health
// package doesn't end up depending on docker/api/types.
//
// All sizes are bytes. A field of zero is "no data on this kind of object",
// not "we failed to look it up" — DiskUsage as a whole returns an error in
// the failure case and empties this struct.
type DiskReport struct {
	// LayersSize is the on-disk footprint of all Docker image layers.
	LayersSize int64

	// BuildCacheSize is the BuildKit cache size (separate from layers).
	BuildCacheSize int64

	// VolumeSize is the *reported* size of all named volumes — only
	// populated when the daemon's volume driver implements the
	// optional size measurement (the local driver does for Linux,
	// not on macOS). Zero on platforms where it's unavailable.
	VolumeSize int64

	// ImageCount is the number of unique tagged images.
	ImageCount int

	// ContainerCount is the number of containers (running or stopped).
	ContainerCount int

	// VolumeCount is the number of named volumes.
	VolumeCount int
}

// diskUsageGroup deduplicates concurrent DiskUsage callers — see DiskUsage.
var diskUsageGroup singleflight.Group

// DiskUsage returns the daemon's `system df` report. The call is **slow**:
// the daemon walks every container/image/volume/build-cache record, and on a
// busy workstation it can take >5 s. We mitigate via:
//
//   - singleflight: concurrent callers share one in-flight RPC.
//   - context propagation: the caller's deadline bounds the call.
//   - shape conversion: the caller never sees the full SDK types, so
//     adding a field doesn't break the contract.
//
// The 30-second outer deadline mentioned in the plan is enforced by the
// **caller**, not here — the health.disk-low check passes a context with
// that deadline. This keeps DiskUsage usable from other call sites that
// have their own timing budget.
func (d *Docker) DiskUsage(ctx context.Context) (DiskReport, error) {
	v, err, _ := diskUsageGroup.Do("system-df", func() (any, error) {
		return d.diskUsageInner(ctx)
	})
	if err != nil {
		return DiskReport{}, err
	}
	rep, _ := v.(DiskReport)
	return rep, nil
}

func (d *Docker) diskUsageInner(ctx context.Context) (DiskReport, error) {
	if d.cli == nil {
		return DiskReport{}, errors.New("docker: client not initialised")
	}
	du, err := d.cli.DiskUsage(ctx, types.DiskUsageOptions{})
	if err != nil {
		return DiskReport{}, fmt.Errorf("docker system df: %w", err)
	}
	out := DiskReport{
		LayersSize:     du.LayersSize,
		ImageCount:     len(du.Images),
		ContainerCount: len(du.Containers),
		VolumeCount:    len(du.Volumes),
	}
	for _, bc := range du.BuildCache {
		if bc == nil {
			continue
		}
		out.BuildCacheSize += bc.Size
	}
	for _, v := range du.Volumes {
		if v == nil || v.UsageData == nil {
			continue
		}
		if v.UsageData.Size > 0 {
			out.VolumeSize += v.UsageData.Size
		}
	}
	return out, nil
}
