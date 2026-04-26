package docker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/docker/docker/errdefs"
)

// retryClass groups Docker SDK errors by remediation strategy. Anything not
// in this set is considered a hard failure and surfaces immediately.
type retryClass int

const (
	retryNone          retryClass = iota
	retryBuildKitRace             // moby/buildkit#6521 ("parent snapshot does not exist")
	retryNameInUse                // 409 Conflict, container name in use
	retryNetworkExists            // 409 Conflict, network already exists
	retryDaemonRestart            // ECONNREFUSED to docker.sock
)

// retryConfig caps the global retry budget. Per-class overrides are applied
// in withRetry below.
const (
	retryBudgetDefault   = 90 * time.Second
	retryAttemptsDefault = 5
	retryBackoffMin      = 200 * time.Millisecond
	retryBackoffMax      = 30 * time.Second
)

// classifyError maps a Docker SDK error to a retryClass. Returns retryNone
// for anything we don't recognise.
func classifyError(err error) retryClass {
	if err == nil {
		return retryNone
	}

	msg := strings.ToLower(err.Error())

	// BuildKit "parent snapshot does not exist" — surfaces during ImagePull
	// when the daemon's snapshotter is mid-GC.
	if strings.Contains(msg, "parent snapshot") && strings.Contains(msg, "does not exist") {
		return retryBuildKitRace
	}

	// Daemon socket connection refused — Docker Desktop restart, daemon
	// upgrade, etc. Both wrapped errors and bare net errors land here.
	if isConnRefused(err) {
		return retryDaemonRestart
	}

	// Docker SDK 409 Conflicts split into name-in-use vs network-exists.
	// The errdefs typed-conflict path is preferred, but the daemon and
	// some intermediate wrappers can lose the type — fall back to string
	// match so we still recover from genuine conflicts.
	switch {
	case errdefs.IsConflict(err),
		strings.Contains(msg, "conflict"):
		switch {
		case strings.Contains(msg, "name") && strings.Contains(msg, "already in use"),
			strings.Contains(msg, "container") && strings.Contains(msg, "already exists"):
			return retryNameInUse
		case strings.Contains(msg, "network") && strings.Contains(msg, "already exists"),
			strings.Contains(msg, "network with name"):
			return retryNetworkExists
		}
	}

	return retryNone
}

func isConnRefused(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return true
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		if errors.Is(netErr.Err, syscall.ECONNREFUSED) {
			return true
		}
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "is the docker daemon running")
}

// withRetry runs op until it succeeds, the retry budget is exhausted, or ctx
// cancels. recover is called between attempts when the previous attempt's
// classification suggests a side-effect cleanup might unblock the next one
// (e.g. force-removing a container after a name-in-use conflict). recover
// itself is non-fatal: if it errors, we log and try op again anyway.
func withRetry[T any](
	ctx context.Context,
	desc string,
	op func(context.Context) (T, error),
	recover func(context.Context, retryClass) error,
) (T, error) {
	var zero T

	deadline := time.Now().Add(retryBudgetDefault)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	backoff := retryBackoffMin
	maxAttempts := retryAttemptsDefault

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		default:
		}

		result, err := op(ctx)
		if err == nil {
			return result, nil
		}

		class := classifyError(err)
		if class == retryNone {
			return zero, err
		}

		// Per-class shaping. A few classes are budget-bounded with custom
		// attempt counts; others use the default backoff curve.
		switch class {
		case retryBuildKitRace:
			// Immediate retry up to 3 times; this race resolves in <1s.
			if attempt >= 3 {
				return zero, fmt.Errorf("%s: %w (buildkit snapshot race after 3 attempts): %v", desc, ErrTransient, err)
			}
			backoff = 50 * time.Millisecond
		case retryNameInUse, retryNetworkExists:
			// Side-effect cleanup, then exactly one retry.
			if recover != nil {
				if recErr := recover(ctx, class); recErr != nil {
					slog.Warn("retry recover failed", "op", desc, "err", recErr.Error())
				}
			}
			if attempt > 1 {
				return zero, fmt.Errorf("%s: %w: %v", desc, ErrTransient, err)
			}
		case retryDaemonRestart:
			// Exponential backoff up to 5 attempts; daemon restarts can
			// take 10-30s on Docker Desktop.
			backoff = backoff * 2
			if backoff > retryBackoffMax {
				backoff = retryBackoffMax
			}
		}

		if time.Now().Add(backoff).After(deadline) {
			return zero, fmt.Errorf("%s: %w: budget exhausted: %v", desc, ErrTransient, err)
		}

		slog.Info("retrying after transient docker error",
			"op", desc, "attempt", attempt, "class", class, "backoff", backoff, "err", err.Error())

		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
	}

	return zero, fmt.Errorf("%s: %w: max attempts (%d) reached", desc, ErrTransient, maxAttempts)
}

// withRetryErr is the unit-typed convenience wrapper for ops that don't
// return a value. Avoids the (struct{}, error) noise at every call site.
func withRetryErr(
	ctx context.Context,
	desc string,
	op func(context.Context) error,
	recover func(context.Context, retryClass) error,
) error {
	_, err := withRetry(ctx, desc, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, op(ctx)
	}, recover)
	return err
}

// isNotFoundLike returns true for the assortment of "not found" errors the
// Docker SDK can return. errdefs.IsNotFound covers most cases; some older
// APIs return string-shaped errors instead.
func isNotFoundLike(err error) bool {
	if err == nil {
		return false
	}
	if errdefs.IsNotFound(err) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such container") ||
		strings.Contains(msg, "no such network") ||
		strings.Contains(msg, "no such volume") ||
		strings.Contains(msg, "no such image")
}
