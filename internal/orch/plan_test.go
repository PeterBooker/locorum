package orch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// recordingStep records Apply/Rollback calls for assertions in tests.
type recordingStep struct {
	name      string
	applyErr  error
	applyHook func(context.Context) error
	rb        *atomic.Int32
	applied   *atomic.Int32
}

func (s *recordingStep) Name() string { return s.name }
func (s *recordingStep) Apply(ctx context.Context) error {
	if s.applied != nil {
		s.applied.Add(1)
	}
	if s.applyHook != nil {
		return s.applyHook(ctx)
	}
	return s.applyErr
}
func (s *recordingStep) Rollback(_ context.Context) error {
	if s.rb != nil {
		s.rb.Add(1)
	}
	return nil
}

func TestRun_EmptyPlan(t *testing.T) {
	var planDone atomic.Int32
	res := Run(context.Background(), Plan{Name: "empty"}, Callbacks{
		OnPlanDone: func(Result) { planDone.Add(1) },
	})
	if res.FinalError != nil {
		t.Errorf("FinalError = %v, want nil", res.FinalError)
	}
	if res.RolledBack {
		t.Errorf("RolledBack = true, want false")
	}
	if planDone.Load() != 1 {
		t.Errorf("OnPlanDone fired %d times, want 1", planDone.Load())
	}
}

func TestRun_AllSucceed(t *testing.T) {
	var applied, rb atomic.Int32
	steps := []Step{
		&recordingStep{name: "a", rb: &rb, applied: &applied},
		&recordingStep{name: "b", rb: &rb, applied: &applied},
		&recordingStep{name: "c", rb: &rb, applied: &applied},
	}
	res := Run(context.Background(), Plan{Name: "ok", Steps: steps}, Callbacks{})
	if res.FinalError != nil {
		t.Errorf("FinalError = %v, want nil", res.FinalError)
	}
	if applied.Load() != 3 {
		t.Errorf("applied = %d, want 3", applied.Load())
	}
	if rb.Load() != 0 {
		t.Errorf("rollbacks = %d, want 0", rb.Load())
	}
	for i, s := range res.Steps {
		if s.Status != StatusSucceeded {
			t.Errorf("step[%d].Status = %s, want succeeded", i, s.Status)
		}
	}
}

func TestRun_FailureRollsBackPrior(t *testing.T) {
	var rbOrder []string
	failErr := errors.New("boom")
	steps := []Step{
		recordingRBStep("a", nil, &rbOrder),
		recordingRBStep("b", nil, &rbOrder),
		recordingRBStep("c", failErr, &rbOrder),
	}

	res := Run(context.Background(), Plan{Name: "fail", Steps: steps}, Callbacks{})
	if res.FinalError == nil {
		t.Fatalf("FinalError = nil, want %v", failErr)
	}
	if !errors.Is(res.FinalError, failErr) && res.FinalError.Error() != failErr.Error() {
		t.Errorf("FinalError = %v, want %v", res.FinalError, failErr)
	}
	if !res.RolledBack {
		t.Errorf("RolledBack = false, want true")
	}

	// Rollback runs in reverse on the prior succeeded steps.
	if len(rbOrder) != 2 {
		t.Fatalf("rollback order = %v, want 2 entries", rbOrder)
	}
	if rbOrder[0] != "b" || rbOrder[1] != "a" {
		t.Errorf("rollback order = %v, want [b a]", rbOrder)
	}

	if res.Steps[0].Status != StatusRolledBack {
		t.Errorf("step[0] = %s, want rolled-back", res.Steps[0].Status)
	}
	if res.Steps[1].Status != StatusRolledBack {
		t.Errorf("step[1] = %s, want rolled-back", res.Steps[1].Status)
	}
	if res.Steps[2].Status != StatusFailed {
		t.Errorf("step[2] = %s, want failed", res.Steps[2].Status)
	}
}

func TestRun_RollbackErrorDoesNotAbortChain(t *testing.T) {
	var applies, rollbacks atomic.Int32
	steps := []Step{
		&errorRBStep{name: "a", applies: &applies, rollbacks: &rollbacks},
		&errorRBStep{name: "b", applies: &applies, rollbacks: &rollbacks},
		&recordingStep{name: "c", applyErr: errors.New("boom")},
	}
	res := Run(context.Background(), Plan{Name: "fail", Steps: steps}, Callbacks{})
	if !res.RolledBack {
		t.Errorf("RolledBack = false, want true")
	}
	if rollbacks.Load() != 2 {
		t.Errorf("rollbacks = %d, want 2 (both a and b should still rollback)", rollbacks.Load())
	}
}

func TestRun_PanicInStepBecomesError(t *testing.T) {
	steps := []Step{
		&recordingStep{
			name: "boom",
			applyHook: func(_ context.Context) error {
				panic("oh no")
			},
		},
	}
	res := Run(context.Background(), Plan{Name: "panic", Steps: steps}, Callbacks{})
	if res.FinalError == nil {
		t.Errorf("expected FinalError after panic")
	}
	if res.Steps[0].Status != StatusFailed {
		t.Errorf("status = %s, want failed", res.Steps[0].Status)
	}
}

func TestRun_ContextCancelMidPlan(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	steps := []Step{
		&recordingStep{name: "first"},
		&recordingStep{
			name: "stalls",
			applyHook: func(_ context.Context) error {
				cancel()
				return nil
			},
		},
		&recordingStep{name: "third"},
	}
	res := Run(ctx, Plan{Name: "cancel", Steps: steps}, Callbacks{})
	if res.FinalError == nil {
		t.Errorf("expected FinalError on cancel")
	}
	// Third step skipped; first rolled back.
	if res.Steps[2].Status != StatusSkipped {
		t.Errorf("step[2] = %s, want skipped", res.Steps[2].Status)
	}
}

func TestRun_StartCallbackFiresPerStep(t *testing.T) {
	var starts, dones atomic.Int32
	steps := []Step{
		&recordingStep{name: "a"},
		&recordingStep{name: "b"},
	}
	Run(context.Background(), Plan{Steps: steps}, Callbacks{
		OnStepStart: func(StepResult) { starts.Add(1) },
		OnStepDone:  func(StepResult) { dones.Add(1) },
	})
	if starts.Load() != 2 || dones.Load() != 2 {
		t.Errorf("starts=%d dones=%d, want 2 each", starts.Load(), dones.Load())
	}
}

// Helper: recordingRBStep that appends its name to a shared list on rollback.
func recordingRBStep(name string, applyErr error, list *[]string) Step {
	return &orderedRBStep{name: name, applyErr: applyErr, list: list}
}

type orderedRBStep struct {
	name     string
	applyErr error
	list     *[]string
}

func (s *orderedRBStep) Name() string                  { return s.name }
func (s *orderedRBStep) Apply(_ context.Context) error { return s.applyErr }
func (s *orderedRBStep) Rollback(_ context.Context) error {
	*s.list = append(*s.list, s.name)
	return nil
}

type errorRBStep struct {
	name      string
	applies   *atomic.Int32
	rollbacks *atomic.Int32
}

func (s *errorRBStep) Name() string { return s.name }
func (s *errorRBStep) Apply(_ context.Context) error {
	s.applies.Add(1)
	return nil
}
func (s *errorRBStep) Rollback(_ context.Context) error {
	s.rollbacks.Add(1)
	return errors.New("rollback failed")
}

// quick sanity: time tracking sane.
func TestRun_RecordsDuration(t *testing.T) {
	start := time.Now()
	res := Run(context.Background(), Plan{
		Name: "p",
		Steps: []Step{&recordingStep{
			name: "slow",
			applyHook: func(_ context.Context) error {
				time.Sleep(5 * time.Millisecond)
				return nil
			},
		}},
	}, Callbacks{})
	if res.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", res.Duration)
	}
	if res.Started.Before(start) {
		t.Errorf("Started before invocation")
	}
}
