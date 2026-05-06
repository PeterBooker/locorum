package sitesteps

import (
	"context"
	"fmt"
	"strings"

	"github.com/PeterBooker/locorum/internal/docker"
)

// Describe implementations for the existing step types. Adding these
// in a separate file keeps the original sitesteps.go stable while
// teaching every step the dry-run vocabulary the orch.Describer
// interface expects (P4 in AGENTS-SUPPORT.md).
//
// Each Describe must be pure: read state, format a string, return.
// Anything that touches Docker, the filesystem, or git belongs in
// Apply, not Describe. The dry-run runner explicitly trusts Describe
// to be side-effect-free; a buggy implementation that, say, pulled an
// image during a dry-run would silently make the preview slow and
// surprise users counting on `--dry-run` for safety.

func (s *EnsureNetworkStep) Describe(_ context.Context) (string, error) {
	if s.Site == nil {
		return "ensure per-site network (no site)", nil
	}
	return fmt.Sprintf("ensure docker network for site %s", s.Site.Slug), nil
}

func (s *EnsureVolumeStep) Describe(_ context.Context) (string, error) {
	if s.Site == nil {
		return "ensure database volume (no site)", nil
	}
	return fmt.Sprintf("ensure database volume for site %s", s.Site.Slug), nil
}

func (s *PullImagesStep) Describe(_ context.Context) (string, error) {
	images := make([]string, 0, len(s.Specs))
	for _, sp := range s.Specs {
		if sp.Image != "" {
			images = append(images, sp.Image)
		}
	}
	if len(images) == 0 {
		return "pull container images (none specified)", nil
	}
	return "pull " + strings.Join(images, ", "), nil
}

func (s *ChownStep) Describe(_ context.Context) (string, error) {
	if s.Site == nil {
		return "chown bind-mounted dirs to the PHP UID/GID (no site)", nil
	}
	return fmt.Sprintf("chown bind-mounts of site %s to the PHP UID/GID", s.Site.Slug), nil
}

func (s *CreateContainersStep) Describe(_ context.Context) (string, error) {
	names := specNamesLocal(s.Specs)
	if len(names) == 0 {
		return "create containers (none specified)", nil
	}
	return "create or recreate containers: " + strings.Join(names, ", "), nil
}

func (s *WaitReadyStep) Describe(_ context.Context) (string, error) {
	if len(s.Containers) == 0 {
		return "wait for containers to report ready (none specified)", nil
	}
	return "wait for ready: " + strings.Join(s.Containers, ", "), nil
}

func (s *StopContainersStep) Describe(_ context.Context) (string, error) {
	if len(s.Containers) == 0 {
		return "stop containers (none specified)", nil
	}
	return "stop containers: " + strings.Join(s.Containers, ", "), nil
}

func (s *RemoveContainersStep) Describe(_ context.Context) (string, error) {
	if len(s.Containers) == 0 {
		return "remove containers (none specified)", nil
	}
	return "remove containers: " + strings.Join(s.Containers, ", "), nil
}

func (s *RegisterRoutesStep) Describe(_ context.Context) (string, error) {
	hosts := []string{s.Route.PrimaryHost}
	if s.Route.WildcardHost != "" {
		hosts = append(hosts, s.Route.WildcardHost)
	}
	return "register router routes for " + strings.Join(hosts, ", "), nil
}

func (s *RemoveRoutesStep) Describe(_ context.Context) (string, error) {
	return fmt.Sprintf("remove router routes for %s", s.Slug), nil
}

func (s *RemoveNetworkStep) Describe(_ context.Context) (string, error) {
	if s.Site == nil {
		return "remove per-site network (no site)", nil
	}
	return fmt.Sprintf("remove docker network for site %s", s.Site.Slug), nil
}

func (s *RemoveSiteConfigsStep) Describe(_ context.Context) (string, error) {
	if s.Site == nil {
		return "remove generated site configs", nil
	}
	return fmt.Sprintf("remove generated configs for site %s", s.Site.Slug), nil
}

func (s *PurgeVolumeStep) Describe(_ context.Context) (string, error) {
	if s.Site == nil {
		return "purge database volume (no site)", nil
	}
	return fmt.Sprintf("DESTROY database volume for site %s (data loss!)", s.Site.Slug), nil
}

func (s *HookStep) Describe(_ context.Context) (string, error) {
	if s.Label == "" {
		return "run hooks", nil
	}
	return s.Label, nil
}

func (s *FuncStep) Describe(_ context.Context) (string, error) {
	if s.Label == "" {
		return "(unnamed glue step)", nil
	}
	return s.Label, nil
}

// specNamesLocal mirrors specNames in sites.go. Local helper so
// describe.go doesn't reach across packages for one trivial map.
func specNamesLocal(specs []docker.ContainerSpec) []string {
	out := make([]string, 0, len(specs))
	for _, sp := range specs {
		if sp.Name != "" {
			out = append(out, sp.Name)
		}
	}
	return out
}
