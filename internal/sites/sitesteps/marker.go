package sitesteps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/types"
	"github.com/PeterBooker/locorum/internal/version"
)

// EnsureMarkerStep reads the volume marker before the database container
// boots and refuses to start if it disagrees with the configured engine
// or version. Fresh volumes (no marker file) pass; the post-start
// WriteMarkerStep stamps them.
//
// The check runs in a one-shot alpine container with the volume mounted
// read-only. ~150ms cost once per site start; cheap insurance against the
// silent-corruption class of bug (LEARNINGS F8).
type EnsureMarkerStep struct {
	Engine docker.Engine
	Site   *types.Site
}

func (s *EnsureMarkerStep) Name() string { return "ensure-marker" }
func (s *EnsureMarkerStep) Apply(ctx context.Context) error {
	eng := dbengine.Resolve(s.Site)
	mounts := []docker.OneShotMount{
		{Volume: docker.SiteVolumeName(s.Site.Slug), Target: "/target", ReadOnly: true},
	}
	// `cat` exits non-zero with "No such file or directory" when the
	// marker is absent; use `[ -f path ] && cat path` instead so we can
	// distinguish absent (exit 0, empty stdout) from read errors.
	cmd := []string{"sh", "-c", `f="/target/` + dbengine.MarkerFilename + `"; if [ -f "$f" ]; then cat "$f"; fi`}
	res, err := s.Engine.RunOneShotCapture(ctx, "locorum-marker-"+s.Site.Slug, "alpine:3", cmd, mounts)
	if err != nil {
		// One-shot infrastructure failed — log and continue. Refusing
		// to start every site because the docker daemon hiccuped is
		// worse than the rare engine-drift case the marker catches.
		slog.Warn("marker: read failed; proceeding without check",
			"site", s.Site.Slug, "err", err.Error())
		return nil
	}
	if res.ExitCode != 0 {
		slog.Warn("marker: read exited non-zero; proceeding without check",
			"site", s.Site.Slug, "exit", res.ExitCode, "stderr", string(res.Stderr))
		return nil
	}
	if len(strings.TrimSpace(string(res.Stdout))) == 0 {
		// Fresh volume — WriteMarkerStep will stamp it after start.
		return nil
	}
	have, err := dbengine.DecodeMarker(res.Stdout)
	if err != nil {
		// A malformed marker is suspicious but recoverable: log loudly
		// and overwrite at write time. Do NOT block start; users with
		// pre-marker volumes need a path forward.
		slog.Warn("marker: malformed payload",
			"site", s.Site.Slug, "err", err.Error())
		return nil
	}
	want := dbengine.VolumeMarker{
		Engine:  dbengine.Kind(s.Site.DBEngine),
		Version: s.Site.DBVersion,
	}
	if err := dbengine.CompareMarker(have, want, eng); err != nil {
		return fmt.Errorf("database volume engine mismatch: %w", err)
	}
	return nil
}
func (s *EnsureMarkerStep) Rollback(_ context.Context) error { return nil }

// WriteMarkerStep stamps the volume marker after the database container
// is up. Idempotent — re-writing on every start keeps the marker fresh
// (Created stays the original timestamp; LocorumVersion advances).
type WriteMarkerStep struct {
	// Execer is *docker.Docker (or a fake) — the dbengine.Execer
	// surface. We accept the wider Engine here so the StartSite plan can
	// pass sm.d as both Engine (for other steps) and Execer (here).
	Execer dbengine.Execer
	Site   *types.Site
}

func (s *WriteMarkerStep) Name() string { return "write-marker" }
func (s *WriteMarkerStep) Apply(ctx context.Context) error {
	eng := dbengine.Resolve(s.Site)

	// Read the existing marker; if it was already written on a prior
	// run, preserve the Created timestamp so the marker tells the user
	// when the volume was first initialised, not when it last booted.
	containerName := docker.SiteContainerName(s.Site.Slug, "database")
	created := time.Now().UTC()
	if existing, err := readMarkerFromContainer(ctx, s.Execer, containerName, eng); err == nil && !existing.Created.IsZero() {
		created = existing.Created
	}

	marker := dbengine.VolumeMarker{
		Engine:         dbengine.Kind(s.Site.DBEngine),
		Version:        s.Site.DBVersion,
		Created:        created,
		LocorumVersion: version.Version,
	}
	if err := dbengine.WriteMarkerToContainer(ctx, s.Execer, containerName, eng, marker); err != nil {
		// Marker write is observability-only after the start succeeded;
		// log loudly but don't fail the lifecycle.
		slog.Warn("marker: write failed", "site", s.Site.Slug, "err", err.Error())
	}
	return nil
}
func (s *WriteMarkerStep) Rollback(_ context.Context) error { return nil }

func readMarkerFromContainer(ctx context.Context, ex dbengine.Execer, container string, eng dbengine.Engine) (dbengine.VolumeMarker, error) {
	path := dbengine.MarkerInVolumePath(eng)
	var stdout strings.Builder
	exit, err := ex.ExecInContainerWriter(ctx, container,
		docker.ExecOptions{Cmd: []string{"sh", "-c", `[ -f "` + path + `" ] && cat "` + path + `"`}},
		stringWriter{b: &stdout}, nil,
	)
	if err != nil {
		return dbengine.VolumeMarker{}, err
	}
	if exit != 0 || stdout.Len() == 0 {
		return dbengine.VolumeMarker{}, errors.New("marker not present")
	}
	return dbengine.DecodeMarker([]byte(stdout.String()))
}

type stringWriter struct{ b *strings.Builder }

func (s stringWriter) Write(p []byte) (int, error) {
	return s.b.Write(p)
}
