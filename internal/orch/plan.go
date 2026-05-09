// Package orch is the small step-orchestrator at the heart of Locorum's
// site-lifecycle implementation. It defines three types and a runner: Plan,
// Step, Result, and Run. Every site lifecycle method (start/stop/delete/
// clone) is expressed as a Plan of named, idempotent, individually-rollback-
// able steps.
//
// The package is deliberately small: this is not a workflow engine. It does
// not persist state, does not have triggers, does not branch. A Plan is an
// ordered sequence of side-effecting work; if any step fails, prior
// successful steps are rolled back in reverse order. A Result captures the
// per-step outcome and the final error.
//
// Why this exists rather than inlining the steps in SiteManager:
//   - Partial-failure recovery is undefined when the lifecycle is a long
//     procedural function. With Plan, every step's Rollback is explicit.
//   - Progress reporting becomes uniform: one OnStepStart/OnStepDone hook
//     fires for every kind of step, and the GUI renders the same checklist
//     regardless of which lifecycle method is running.
//   - Steps are testable in isolation against fake engines.
//
// Concurrency: Run executes steps sequentially. Steps that need internal
// parallelism (e.g. four image pulls) implement that themselves; the Plan
// stays a readable linear sequence.
package orch

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Status reports the per-step outcome.
type Status string

const (
	StatusPending    Status = "pending"
	StatusRunning    Status = "running"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusRolledBack Status = "rolled-back"
	StatusSkipped    Status = "skipped"
)

// Step is one unit of orchestrated work.
//
// Apply must be safe to retry: a Plan that fails part-way may be re-run, and
// EnsureContainer-style idempotency carries the load. Returning a non-nil
// error from Apply triggers rollback of every previously-Succeeded step in
// reverse order.
//
// Rollback is best-effort. Returning an error is logged but does NOT abort
// the rollback chain — we keep undoing prior steps. Users care about a
// clean state more than knowing every cleanup failure.
//
// Names should be stable, short, and human-readable. They appear in logs,
// progress UI, and the audit log.
type Step interface {
	Name() string
	Apply(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Plan is an ordered sequence of steps run as a single transaction.
type Plan struct {
	Name  string
	Steps []Step
}

// Result captures the outcome of running a Plan.
type Result struct {
	PlanName   string
	Started    time.Time
	Duration   time.Duration
	Steps      []StepResult
	RolledBack bool
	FinalError error
}

// StepResult captures the outcome of a single step.
type StepResult struct {
	Name     string
	Started  time.Time
	Duration time.Duration
	Status   Status
	Error    error
}

// Callbacks are the per-event hooks Run invokes. All fields are optional;
// nil callbacks are no-ops. Callbacks fire synchronously on the runner's
// goroutine; cheap operations (struct copies, channel sends) only.
type Callbacks struct {
	OnStepStart func(StepResult)
	OnStepDone  func(StepResult)
	OnPlanDone  func(Result)
}

// Run executes plan, emitting progress callbacks and rolling back on
// failure. It always returns a fully-populated Result, even on error, so
// the caller can render a uniform after-action panel.
//
// On context cancellation: in-flight step's context cancels; remaining
// steps are marked Skipped; previously-Succeeded steps are rolled back.
func Run(ctx context.Context, plan Plan, cb Callbacks) Result {
	res := Result{
		PlanName: plan.Name,
		Started:  time.Now(),
		Steps:    make([]StepResult, len(plan.Steps)),
	}
	for i, s := range plan.Steps {
		res.Steps[i] = StepResult{Name: s.Name(), Status: StatusPending}
	}

	for i, s := range plan.Steps {
		if ctx.Err() != nil {
			res.Steps[i].Status = StatusSkipped
			res.Steps[i].Error = ctx.Err()
			continue
		}

		sr := StepResult{Name: s.Name(), Started: time.Now(), Status: StatusRunning}
		res.Steps[i] = sr
		fire(cb.OnStepStart, sr)

		err := safeApply(ctx, s)
		sr.Duration = time.Since(sr.Started)
		if err != nil {
			sr.Status = StatusFailed
			sr.Error = err
			res.Steps[i] = sr
			fire(cb.OnStepDone, sr)

			// Mark every later step skipped and (if any) re-fire the done
			// callback so the GUI hides their spinners without claiming
			// they ran.
			for j := i + 1; j < len(res.Steps); j++ {
				res.Steps[j].Status = StatusSkipped
			}

			res.RolledBack = rollbackPrior(ctx, plan.Steps, i, &res, cb)
			res.FinalError = err
			res.Duration = time.Since(res.Started)
			fire(cb.OnPlanDone, res)
			return res
		}

		sr.Status = StatusSucceeded
		res.Steps[i] = sr
		fire(cb.OnStepDone, sr)
	}

	if ctx.Err() != nil {
		res.FinalError = ctx.Err()
		// Roll back any steps we did complete. Index points one past the
		// last completed step.
		for i := len(plan.Steps) - 1; i >= 0; i-- {
			if res.Steps[i].Status == StatusSucceeded {
				res.RolledBack = rollbackPrior(ctx, plan.Steps, i+1, &res, cb)
				break
			}
		}
	}

	res.Duration = time.Since(res.Started)
	fire(cb.OnPlanDone, res)
	return res
}

// rollbackPrior calls Rollback on every Succeeded step before failedAt, in
// reverse order, using a fresh non-cancelled-context for cleanup. Returns
// true once at least one Rollback fired.
func rollbackPrior(parent context.Context, steps []Step, failedAt int, res *Result, cb Callbacks) bool {
	// Cleanup runs even if the parent context cancelled — losing track of
	// orphan resources is worse than respecting the cancellation here. We
	// strip cancellation but inherit values/tracing, then layer a
	// generous deadline of our own.
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 60*time.Second)
	defer cancel()

	found := false
	for i := failedAt - 1; i >= 0; i-- {
		if res.Steps[i].Status != StatusSucceeded {
			continue
		}
		found = true
		s := steps[i]
		err := safeRollback(rbCtx, s)
		res.Steps[i].Status = StatusRolledBack
		res.Steps[i].Error = err
		fire(cb.OnStepDone, res.Steps[i])
		if err != nil {
			slog.Warn("rollback step failed",
				"plan", res.PlanName, "step", s.Name(), "err", err.Error())
		}
	}
	return found
}

// safeApply runs Step.Apply with panic recovery so a bug in one step
// doesn't take the whole process down — the panic surfaces as a step error.
func safeApply(ctx context.Context, s Step) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = errors.New("step panicked: " + recString(rec))
		}
	}()
	return s.Apply(ctx)
}

func safeRollback(ctx context.Context, s Step) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = errors.New("rollback panicked: " + recString(rec))
		}
	}()
	return s.Rollback(ctx)
}

func recString(rec any) string {
	switch v := rec.(type) {
	case error:
		return v.Error()
	case string:
		return v
	default:
		return "unknown panic"
	}
}

// fire invokes cb if non-nil. Recovers from panics so a misbehaving
// callback can't break the runner. Callbacks fire synchronously; long work
// must hand off to a goroutine in the implementation.
func fire[T any](cb func(T), val T) {
	if cb == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("orch callback panicked", "panic", recString(rec))
		}
	}()
	cb(val)
}

// Compile-time guard: the runner is stateless so it can be re-entered for
// independent Plans concurrently. The mutex below exists only as a place
// to hang documentation — none is held at runtime.
var _ = (*sync.Mutex)(nil)
