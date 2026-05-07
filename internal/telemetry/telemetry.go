// Package telemetry is the opt-in usage-event scaffold described by
// LEARNINGS §7.3. Phase A — what's wired today — defines the Sink
// interface, ships a no-op implementation, and exposes Track / Flush
// helpers so the rest of the codebase can call instrumentation now and
// the eventual transport drops in without touching call sites.
//
// Phase B (real transport) is deferred until a privacy doc lands and a
// vendor is chosen. Until then, every Track call is dropped by the
// noop sink and no network IO ever happens — even if the user opts in
// via the Settings card.
package telemetry

import (
	"context"
	"sync/atomic"
)

// Sink is the destination for telemetry events. Implementations MUST be
// safe for concurrent use. Track is non-blocking (fire-and-forget); the
// sink is expected to buffer and flush on its own schedule, plus on
// Flush.
type Sink interface {
	// Track records a single event. props is a flat map; values must be
	// JSON-serialisable. Implementations MUST drop unknown keys (see the
	// Phase B allow-list note in UX.md §5.2) so a typo at a call site
	// can't leak path or credential data.
	Track(event string, props map[string]any)

	// Flush attempts to drain any buffered events before the supplied
	// context elapses. Best-effort; nil error means "drained or empty,"
	// non-nil means "some events remain in flight."
	Flush(ctx context.Context) error
}

// Default returns the process-wide sink installed via SetDefault.
// Initially the noop sink — call SetDefault from main.go after deciding
// the user's opt-in state.
func Default() Sink {
	if s := defaultSink.Load(); s != nil {
		return *s
	}
	return Noop{}
}

// SetDefault swaps the process-wide sink. Concurrent-safe via
// atomic.Pointer; in-flight Track calls against the previous sink
// continue to that sink (no shared state).
func SetDefault(s Sink) {
	if s == nil {
		s = Noop{}
	}
	defaultSink.Store(&s)
}

// Track is a convenience wrapper around Default().Track. Callers that
// already hold a Sink reference can use it directly; instrumentation
// scattered across the codebase prefers this.
func Track(event string, props map[string]any) {
	Default().Track(event, props)
}

// Flush drains the default sink. Called from main.go on shutdown.
func Flush(ctx context.Context) error {
	return Default().Flush(ctx)
}

// Noop is the always-installed sink that drops every event. Bind it
// explicitly when initialising tests so a forgotten SetDefault doesn't
// see whichever sink the previous test left in place.
type Noop struct{}

func (Noop) Track(string, map[string]any) {}
func (Noop) Flush(context.Context) error  { return nil }

var defaultSink atomic.Pointer[Sink]
