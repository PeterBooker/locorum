package ui

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/router"
	"github.com/PeterBooker/locorum/internal/sites"
	"github.com/PeterBooker/locorum/internal/tls"
)

func TestSurfaceErrorNilDoesNothing(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	SurfaceError(s, "ctx", nil, nil)
	if got := s.ErrorBannerSnapshot(); got.Message != "" {
		t.Fatalf("nil err produced banner %q", got.Message)
	}
}

func TestSurfaceErrorDaemonUnreachable(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	wrapped := fmt.Errorf("starting site: %w", docker.ErrDaemonUnreachable)
	SurfaceError(s, "Failed to start site", wrapped, nil)
	got := s.ErrorBannerSnapshot()
	if !got.HasAction {
		t.Fatalf("daemon-unreachable banner missing action")
	}
	if got.ActionID != "open-docker-help" {
		t.Fatalf("ActionID = %q, want open-docker-help", got.ActionID)
	}
}

func TestSurfaceErrorPortInUse(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	wrapped := fmt.Errorf("router: %w", router.ErrPortInUse)
	SurfaceError(s, "ignored", wrapped, nil)
	got := s.ErrorBannerSnapshot()
	if got.ActionID != "open-network-settings" {
		t.Fatalf("ActionID = %q, want open-network-settings", got.ActionID)
	}
}

func TestSurfaceErrorMkcertMissing(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	SurfaceError(s, "ignored", tls.ErrMkcertMissing, nil)
	got := s.ErrorBannerSnapshot()
	if got.ActionID != "open-mkcert-help" {
		t.Fatalf("ActionID = %q, want open-mkcert-help", got.ActionID)
	}
}

func TestSurfaceErrorSiteNotRunningWithStarter(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	called := make(chan struct{}, 1)
	starter := func() { called <- struct{}{} }
	SurfaceError(s, "Failed to open shell", sites.ErrSiteNotRunning, starter)

	got := s.ErrorBannerSnapshot()
	if got.ActionID != "start-site" {
		t.Fatalf("ActionID = %q, want start-site", got.ActionID)
	}
	if !s.TriggerErrorAction() {
		t.Fatalf("TriggerErrorAction returned false")
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatalf("starter never invoked")
	}
}

func TestSurfaceErrorSiteNotRunningWithoutStarterFallsBack(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	SurfaceError(s, "Failed to do thing", sites.ErrSiteNotRunning, nil)
	got := s.ErrorBannerSnapshot()
	if got.HasAction {
		t.Fatalf("starter==nil should suppress action; got %+v", got)
	}
	if got.Message == "" {
		t.Fatalf("empty banner with non-nil err")
	}
}

func TestSurfaceErrorUnknownFallsBackPlain(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	SurfaceError(s, "Boom", errors.New("garden-variety"), nil)
	got := s.ErrorBannerSnapshot()
	if got.HasAction {
		t.Fatalf("unknown err should not produce action")
	}
	if got.Message != "Boom: garden-variety" {
		t.Fatalf("Message = %q, want %q", got.Message, "Boom: garden-variety")
	}
}
