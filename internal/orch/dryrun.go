package orch

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Describer is the optional interface a Step implements to opt into
// dry-run reporting. Steps without Describe still run under Dry but
// surface a generic "(no preview)" line; that's intentional so a Plan
// can be partially dry-run-clean even before every step has been
// updated.
//
// Implementations must be pure: the Describe call is allowed to read
// configuration (e.g. inspect a docker spec), but must not write
// anywhere or call out to the network. The contract is "describe what
// Apply would do without committing to it."
type Describer interface {
	Describe(ctx context.Context) (string, error)
}

// DryResult is the structured outcome of Dry. Not the same shape as
// Result: dry-run never fails individual steps and never rolls back,
// because nothing was applied.
type DryResult struct {
	PlanName string
	Started  time.Time
	Duration time.Duration
	Steps    []DryStepResult
}

// DryStepResult is one step's preview line. Description is the user-
// visible string the Describer returned, or a sentinel for steps that
// don't implement Describer.
type DryStepResult struct {
	Name        string
	Description string
	Implemented bool
	Error       error
}

// noPreview is the sentinel used when a step doesn't implement
// Describer. Surfaced verbatim so dry-run output keeps every step's
// position even when some are silent.
const noPreview = "(no preview available — step does not implement orch.Describer)"

// Dry runs plan in describe-only mode. Each step is interrogated via
// Describer; non-implementers fall back to noPreview. The plan's
// real Apply is never invoked, so Dry is always safe to call against
// production state.
//
// Errors from Describe surface in the per-step DryStepResult.Error;
// Dry itself returns a non-nil error only when ctx is cancelled (so
// callers can distinguish "user gave up" from "describe surfaced a
// problem").
func Dry(ctx context.Context, plan Plan) (DryResult, error) {
	res := DryResult{
		PlanName: plan.Name,
		Started:  time.Now(),
		Steps:    make([]DryStepResult, 0, len(plan.Steps)),
	}
	for _, s := range plan.Steps {
		if ctx.Err() != nil {
			res.Duration = time.Since(res.Started)
			return res, ctx.Err()
		}
		entry := DryStepResult{Name: s.Name()}
		if d, ok := s.(Describer); ok {
			entry.Implemented = true
			line, err := safeDescribe(ctx, d)
			entry.Description = line
			if err != nil {
				entry.Error = err
			}
		} else {
			entry.Description = noPreview
		}
		res.Steps = append(res.Steps, entry)
	}
	res.Duration = time.Since(res.Started)
	return res, nil
}

func safeDescribe(ctx context.Context, d Describer) (out string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = errors.New("describe panicked: " + recString(rec))
		}
	}()
	return d.Describe(ctx)
}

// Format renders the dry-run result as a multi-line string suitable
// for `locorum site delete --dry-run` output.
func (r DryResult) Format() string {
	out := fmt.Sprintf("Plan: %s (%d steps)\n", r.PlanName, len(r.Steps))
	for i, s := range r.Steps {
		out += fmt.Sprintf("  %d. %s\n", i+1, s.Name)
		if s.Error != nil {
			out += fmt.Sprintf("     ! describe error: %s\n", s.Error)
		}
		if s.Description != "" {
			out += fmt.Sprintf("     - %s\n", s.Description)
		}
	}
	return out
}
