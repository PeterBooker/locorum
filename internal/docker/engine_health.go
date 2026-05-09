package docker

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"
)

// healthPollInterval is the cadence at which WaitReady inspects the
// container. Faster than Docker's own healthcheck cadence so the GUI sees
// state transitions immediately rather than waiting for the next probe tick.
const healthPollInterval = 250 * time.Millisecond

// initialSettlePeriod is how long WaitReady waits for a container *without*
// a healthcheck before declaring it ready. The container only needs to be
// in the "running" state at the end of this window.
const initialSettlePeriod = 1 * time.Second

// WaitReady blocks until the container is healthy or timeout elapses. On
// timeout, returns ErrContainerNotReady wrapped with the last 50 log lines
// so the GUI can surface what actually failed.
//
// LOCORUM_HEALTH_TIMEOUT_MULT (float, default 1.0) scales the timeout —
// useful on slow CI hardware where MySQL's first-start init can take
// 60-90s instead of the local-dev typical 20s.
func (d *Docker) WaitReady(ctx context.Context, name string, timeout time.Duration) error {
	timeout = scaleTimeout(timeout)

	deadline := time.Now().Add(timeout)
	if d2, ok := ctx.Deadline(); ok && d2.Before(deadline) {
		deadline = d2
	}

	hasHealthcheck := false
	startedRunning := false
	startedAt := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		info, err := d.cli.ContainerInspect(ctx, name)
		if err != nil {
			if isNotFoundLike(err) {
				return fmt.Errorf("%w: %s", ErrNotFound, name)
			}
			return fmt.Errorf("inspect %q: %w", name, err)
		}

		// Detect healthcheck on first iteration; subsequent inspects are
		// faster than re-checking image config.
		if info.Config != nil && info.Config.Healthcheck != nil && len(info.Config.Healthcheck.Test) > 0 {
			hasHealthcheck = true
		}

		state := info.State
		if state == nil {
			// Inconsistent inspect; let the loop retry.
			time.Sleep(healthPollInterval)
			continue
		}

		// Hard failure: the container died.
		if state.Status == "exited" || state.Status == "dead" || state.Status == "removing" {
			return d.notReady(ctx, name, fmt.Errorf("container %q in state %q (exit=%d)", name, state.Status, state.ExitCode))
		}

		if state.Running {
			startedRunning = true
			if hasHealthcheck && state.Health != nil {
				switch state.Health.Status {
				case "healthy":
					return nil
				case "unhealthy":
					return d.notReady(ctx, name, fmt.Errorf("container %q is unhealthy", name))
				case "starting", "":
					// Keep polling.
				}
			} else if !hasHealthcheck && time.Since(startedAt) >= initialSettlePeriod {
				// No healthcheck defined; running for the settle period is
				// our readiness signal. This is the documented fallback.
				return nil
			}
		}

		if time.Now().After(deadline) {
			return d.notReady(ctx, name, fmt.Errorf("timeout after %s waiting for %q (running=%v, started_running=%v)",
				timeout, name, state.Running, startedRunning))
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(healthPollInterval):
		}
	}
}

// notReady wraps the precipitating error with ErrContainerNotReady and
// appends the last 50 log lines for diagnostics.
func (d *Docker) notReady(ctx context.Context, name string, cause error) error {
	logs, _ := d.ContainerLogs(ctx, name, 50)
	if logs == "" {
		return fmt.Errorf("%w: %w", ErrContainerNotReady, cause)
	}
	return fmt.Errorf("%w: %w\n--- last 50 log lines ---\n%s", ErrContainerNotReady, cause, logs)
}

func scaleTimeout(t time.Duration) time.Duration {
	mult := os.Getenv("LOCORUM_HEALTH_TIMEOUT_MULT")
	if mult == "" {
		return t
	}
	f, err := strconv.ParseFloat(mult, 64)
	if err != nil || f <= 0 {
		return t
	}
	return time.Duration(float64(t) * f)
}
