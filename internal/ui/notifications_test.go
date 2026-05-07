package ui

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestErrorBannerPlain(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	s.ShowError("boom")

	got := s.ErrorBannerSnapshot()
	if got.Message != "boom" {
		t.Fatalf("Message = %q, want %q", got.Message, "boom")
	}
	if got.HasAction {
		t.Fatalf("HasAction = true, want false")
	}
	if s.ActiveError() != "boom" {
		t.Fatalf("ActiveError = %q, want %q", s.ActiveError(), "boom")
	}
}

func TestErrorBannerWithActionRendersButton(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	s.ShowErrorWithAction("daemon down", NotifyAction{
		ID:    "open-docker",
		Label: "Show how",
		Run:   func() {},
	})

	got := s.ErrorBannerSnapshot()
	if !got.HasAction {
		t.Fatalf("HasAction = false, want true")
	}
	if got.ActionID != "open-docker" {
		t.Fatalf("ActionID = %q, want %q", got.ActionID, "open-docker")
	}
	if got.ActionLabel != "Show how" {
		t.Fatalf("ActionLabel = %q, want %q", got.ActionLabel, "Show how")
	}
	if got.Busy {
		t.Fatalf("Busy = true before trigger, want false")
	}
}

func TestErrorBannerNilRunFallsBackToPlain(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	// Run==nil should be indistinguishable from ShowError.
	s.ShowErrorWithAction("oops", NotifyAction{ID: "noop", Label: "Retry", Run: nil})
	got := s.ErrorBannerSnapshot()
	if got.HasAction {
		t.Fatalf("HasAction = true with nil Run, want false")
	}
	if got.Message != "oops" {
		t.Fatalf("Message = %q, want oops", got.Message)
	}
}

func TestErrorBannerTriggerActionRunsOnceAndMarksBusy(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	var calls atomic.Int32
	s.ShowErrorWithAction("port 80 is busy", NotifyAction{
		ID:    "open-network",
		Label: "Open Network",
		Run: func() {
			calls.Add(1)
		},
	})

	if !s.TriggerErrorAction() {
		t.Fatalf("TriggerErrorAction returned false on first call, want true")
	}
	// While busy, a second trigger must be a no-op.
	if s.TriggerErrorAction() {
		t.Fatalf("TriggerErrorAction returned true while busy, want false")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("Run invoked %d times, want 1", got)
	}
	got := s.ErrorBannerSnapshot()
	if !got.Busy {
		t.Fatalf("Busy = false after trigger, want true")
	}
}

func TestErrorBannerClear(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	s.ShowErrorWithAction("transient", NotifyAction{ID: "x", Label: "Retry", Run: func() {}})
	s.ClearErrorBanner()
	if got := s.ErrorBannerSnapshot(); got.Message != "" {
		t.Fatalf("after Clear, Message = %q, want empty", got.Message)
	}
	if s.TriggerErrorAction() {
		t.Fatalf("TriggerErrorAction after Clear returned true, want false")
	}
}

func TestErrorBannerExpires(t *testing.T) {
	t.Parallel()
	s := NewUIState()
	s.ShowError("ephemeral")
	// Force expiry by rewinding the clock-based field.
	s.mu.Lock()
	s.errorExpiry = time.Now().Add(-time.Second)
	s.mu.Unlock()

	if got := s.ActiveError(); got != "" {
		t.Fatalf("ActiveError after expiry = %q, want empty", got)
	}
	if got := s.ErrorBannerSnapshot(); got.Message != "" {
		t.Fatalf("Snapshot after expiry message = %q, want empty", got.Message)
	}
}

func TestNotifyActionHasRun(t *testing.T) {
	t.Parallel()
	if (NotifyAction{}).HasRun() {
		t.Fatalf("zero NotifyAction.HasRun() = true, want false")
	}
	if !(NotifyAction{Run: func() {}}).HasRun() {
		t.Fatalf("NotifyAction{Run: f}.HasRun() = false, want true")
	}
}
