package sitesteps

import (
	"context"
	"testing"

	"github.com/PeterBooker/locorum/internal/dbengine"
	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/docker/fake"
)

// freshVolumeMarkerStdout is the stdout the volume-marker reader returns
// when the file is absent: empty string, exit 0.
func freshVolumeMarker() docker.OneShotResult {
	return docker.OneShotResult{Stdout: nil, ExitCode: 0}
}

// jsonMarker formats a marker payload identical to what
// dbengine.EncodeMarker produces, with a trailing newline so the cat
// pipeline's output matches what the parser expects.
func jsonMarker(t *testing.T, kind dbengine.Kind, version string) docker.OneShotResult {
	t.Helper()
	body, err := dbengine.EncodeMarker(dbengine.VolumeMarker{
		Engine: kind, Version: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	return docker.OneShotResult{Stdout: body, ExitCode: 0}
}

func TestEnsureMarkerStep_FreshVolumeAllowsStart(t *testing.T) {
	eng := fake.New()
	eng.OneShotScript = []fake.OneShotScripted{{Result: freshVolumeMarker()}}
	site := testSite()

	step := &EnsureMarkerStep{Engine: eng, Site: site}
	if err := step.Apply(context.Background()); err != nil {
		t.Errorf("fresh volume should pass; got %v", err)
	}
}

func TestEnsureMarkerStep_MatchAllowsStart(t *testing.T) {
	eng := fake.New()
	eng.OneShotScript = []fake.OneShotScripted{{Result: jsonMarker(t, dbengine.MySQL, "8.4")}}
	site := testSite()

	step := &EnsureMarkerStep{Engine: eng, Site: site}
	if err := step.Apply(context.Background()); err != nil {
		t.Errorf("matching marker should pass; got %v", err)
	}
}

func TestEnsureMarkerStep_EngineMismatchRefuses(t *testing.T) {
	eng := fake.New()
	eng.OneShotScript = []fake.OneShotScripted{{Result: jsonMarker(t, dbengine.MariaDB, "11.4")}}
	site := testSite() // configured for MySQL 8.4

	step := &EnsureMarkerStep{Engine: eng, Site: site}
	err := step.Apply(context.Background())
	if err == nil {
		t.Fatal("engine mismatch should refuse")
	}
}

func TestEnsureMarkerStep_VersionDowngradeRefuses(t *testing.T) {
	eng := fake.New()
	eng.OneShotScript = []fake.OneShotScripted{{Result: jsonMarker(t, dbengine.MySQL, "8.4")}}
	site := testSite()
	site.DBVersion = "5.7" // attempting to downgrade

	step := &EnsureMarkerStep{Engine: eng, Site: site}
	err := step.Apply(context.Background())
	if err == nil {
		t.Fatal("downgrade should refuse")
	}
}

func TestEnsureMarkerStep_RunFailureProceeds(t *testing.T) {
	// One-shot infrastructure fails; we proceed with a warning rather
	// than block every site start because docker hiccupped.
	eng := fake.New()
	eng.OneShotScript = []fake.OneShotScripted{{Err: context.DeadlineExceeded}}
	site := testSite()

	step := &EnsureMarkerStep{Engine: eng, Site: site}
	if err := step.Apply(context.Background()); err != nil {
		t.Errorf("infra failure should not fail the step; got %v", err)
	}
}
