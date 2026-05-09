package testutil

import (
	"context"
	"fmt"
	"strings"
	"testing"

	dockerlabels "github.com/PeterBooker/locorum/internal/docker"
)

// DockerInspector is the read-only Engine subset RequireNoDockerLeaks
// needs. The interface lets fakes satisfy it without a daemon.
type DockerInspector interface {
	ContainersByLabel(ctx context.Context, match map[string]string) ([]dockerlabels.ContainerInfo, error)
	NetworksByLabel(ctx context.Context, match map[string]string) ([]dockerlabels.NetworkInfo, error)
}

// VolumeLister is optional — the production Engine has no read-only
// volume listing today (only RemoveVolumesByLabel). Integration tests
// adapt the SDK themselves; pass nil to skip the volume check.
type VolumeLister interface {
	VolumesByLabel(ctx context.Context, match map[string]string) ([]string, error)
}

// RequireNoDockerLeaks asserts no containers/networks/volumes labelled
// io.locorum.platform=locorum (and, if siteSlug != "", io.locorum.site
// matching) survive the test. Call last in t.Cleanup, after lifecycle
// teardown.
func RequireNoDockerLeaks(t testing.TB, eng DockerInspector, volLister VolumeLister, siteSlug string) {
	t.Helper()
	ctx := context.Background()

	match := map[string]string{
		dockerlabels.LabelPlatform: dockerlabels.PlatformValue,
	}
	if siteSlug != "" {
		match[dockerlabels.LabelSite] = siteSlug
	}

	containers, err := eng.ContainersByLabel(ctx, match)
	if err != nil {
		t.Fatalf("RequireNoDockerLeaks: list containers: %v", err)
	}
	networks, err := eng.NetworksByLabel(ctx, match)
	if err != nil {
		t.Fatalf("RequireNoDockerLeaks: list networks: %v", err)
	}
	var volumes []string
	if volLister != nil {
		volumes, err = volLister.VolumesByLabel(ctx, match)
		if err != nil {
			t.Fatalf("RequireNoDockerLeaks: list volumes: %v", err)
		}
	}

	if len(containers) == 0 && len(networks) == 0 && len(volumes) == 0 {
		return
	}

	t.Errorf("Docker resource leak after test (slug=%q):\n%s%s%s",
		siteSlug,
		containerSection(containers),
		networkSection(networks),
		listSection("volumes", volumes))
}

func containerSection(cs []dockerlabels.ContainerInfo) string {
	if len(cs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  containers:\n")
	for _, c := range cs {
		fmt.Fprintf(&sb, "    - %s (%s)\n", strings.Join(c.Names, ","), c.State)
	}
	return sb.String()
}

func networkSection(ns []dockerlabels.NetworkInfo) string {
	if len(ns) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  networks:\n")
	for _, n := range ns {
		fmt.Fprintf(&sb, "    - %s\n", n.Name)
	}
	return sb.String()
}

func listSection(kind string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "  %s:\n", kind)
	for _, n := range names {
		fmt.Fprintf(&sb, "    - %s\n", n)
	}
	return sb.String()
}
