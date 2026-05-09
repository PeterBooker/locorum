// Package health is Locorum's system-health surface. Each Check is a small
// pure-ish function that returns a slice of Findings; the Runner schedules
// checks on a typed cadence and publishes the aggregated Snapshot via an
// atomic.Pointer that the UI layer can read on every frame without locking.
//
// # Adding a new check
//
// Implement [Check] in a new file:
//
//	type ratelimitCheck struct{ engine docker.Engine }
//	func (ratelimitCheck) ID() string                  { return "docker-ratelimit" }
//	func (ratelimitCheck) Cadence() time.Duration       { return 0 } // use runner default
//	func (ratelimitCheck) Budget() time.Duration        { return 5 * time.Second }
//	func (c ratelimitCheck) Run(ctx context.Context) ([]Finding, error) { ... }
//
// Then add an instance to [Bundled] so it ships by default.
//
// # Concurrency model
//
//   - The runner runs each check in its own goroutine inside a fixed-size
//     semaphore (cap 8). Long checks don't starve short ones.
//   - Each check call gets a per-call context with the check's [Check.Budget]
//     deadline. A check that exceeds budget twice in a row is marked
//     [Finding.Stale] in the published snapshot; three breaches → suspended
//     for one cycle.
//   - The published [Snapshot] is read via [Runner.Snapshot] which loads an
//     atomic.Pointer — no lock, lock-free for callers.
//   - Checks must NOT capture or share mutable state across runs. Pass the
//     world in via the constructor; the [Run] method is otherwise pure.
package health

import (
	"context"
	"time"
)

// Severity is the urgency of a Finding. Ordered: Info < Warn < Blocker.
// The runner sorts findings descending by severity so the UI panel renders
// blockers first.
type Severity int8

const (
	// SeverityInfo is for informational signals — provider name, mutagen
	// recommendation. No badge in the nav rail; appears in the panel only.
	SeverityInfo Severity = 1

	// SeverityWarn surfaces an amber dot in the nav rail and a toast on
	// first detection. The user can keep using the app.
	SeverityWarn Severity = 2

	// SeverityBlocker is "you cannot use Locorum right now" — Rosetta,
	// Docker down. Surfaces a modal on the very first frame after detection.
	SeverityBlocker Severity = 3
)

// String renders the severity as a lowercase token. Suitable for slog
// fields and JSON output.
func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarn:
		return "warn"
	case SeverityBlocker:
		return "blocker"
	default:
		return "unknown"
	}
}

// Finding is the unit of system-health output. Two Findings are treated as
// the same observation across runs when their (ID, Severity, DedupKey)
// triple matches — the runner preserves [Finding.Detected] in that case so
// the UI can render "noticed 3m ago" without falsely re-minting on detail
// churn.
//
// Checks return a slice of these. They MUST NOT set [Finding.Detected] —
// the runner sets it during snapshot publication.
type Finding struct {
	// ID is a stable identifier for this *kind* of finding, e.g.
	// "rosetta", "disk-low", "port-conflict-80". Never includes a
	// per-instance suffix; use [DedupKey] for that.
	ID string

	// Severity is the urgency band.
	Severity Severity

	// DedupKey is an optional discriminator within a single ID — e.g.
	// "/var/lib/docker" for a per-volume warning. Two findings with the
	// same ID but different DedupKey are independent rows in the panel.
	DedupKey string

	// Title is the short rendering ("Docker is not running"). One line,
	// max ~40 chars.
	Title string

	// Detail is the single-sentence explanation. Plain text; the UI
	// wraps and styles. No newlines.
	Detail string

	// Remediation is the imperative one-line "what to do next". Verbs
	// in present tense ("Install mkcert", not "You should install
	// mkcert"). UI may render this prefixed with → or ↳.
	Remediation string

	// HelpURL is an optional documentation link. The UI renders a small
	// "Learn more" affordance when set. Empty when no docs apply.
	HelpURL string

	// Action is an optional one-click fix. Nil for findings the user
	// must resolve outside Locorum.
	Action *Action

	// Detected is the wall-clock time this finding was first observed
	// in the *current process*. Set by the runner; checks must leave
	// it zero. Preserved across runs when (ID, Severity, DedupKey)
	// matches the previous snapshot.
	Detected time.Time

	// Stale is set by the runner when the producing check exceeded its
	// budget on the most recent run. The UI renders a clock icon next
	// to the finding so the user knows the data may be stale.
	Stale bool
}

// Action is an optional one-click remediation attached to a Finding.
// [Run] is invoked from a bounded worker pool; the runner enforces the
// timeout and re-entrancy guard so implementations only need to do the
// work itself.
type Action struct {
	// Label is the button text ("Install mkcert", "Open Docker Desktop").
	Label string

	// Run performs the remediation. Returns nil on success; an error
	// surfaces as a transient toast plus a slog ERROR. Must not block
	// indefinitely — the runner enforces [Action.Timeout].
	Run func(context.Context) error

	// Timeout caps Run's wall-clock cost. Zero falls back to
	// [DefaultActionTimeout] (30 s).
	Timeout time.Duration
}

// DefaultActionTimeout caps Run when [Action.Timeout] is zero. Picked to
// be generous enough for an mkcert -install on cold cache without the
// user thinking the app froze.
const DefaultActionTimeout = 30 * time.Second

// Check is the contract every system-health probe satisfies.
//
// [Run] should be cheap on the hot path; expensive checks (disk usage)
// declare a slower [Cadence]. Errors returned by [Run] are *not* findings
// — they're internal failures the runner logs at ERROR and surfaces as a
// generic `check-failed:<id>` warn finding so the user still sees that
// something is wrong with the platform.
type Check interface {
	// ID returns a stable identifier. Must match [Finding.ID] for the
	// findings this check produces; the runner uses it for dedup,
	// logging, and the suspension state machine.
	ID() string

	// Cadence is the minimum interval between runs. Zero means "use
	// the runner default" (typically 5 minutes).
	Cadence() time.Duration

	// Budget is the per-call wall-clock budget. The runner attaches a
	// context with this deadline to every Run call. A check returning
	// late marks the resulting findings Stale and may be suspended.
	Budget() time.Duration

	// Run produces zero or more findings. Returning (nil, nil) means
	// "all clear for this concern". Errors are logged + surfaced via
	// the check-failed envelope.
	Run(ctx context.Context) ([]Finding, error)
}

// Snapshot is the published view of system health. The Runner publishes
// these via [atomic.Pointer]; subscribers compare [Snapshot.Generation]
// to detect "anything changed since the last frame".
type Snapshot struct {
	// Findings is sorted by Severity desc, then ID asc, then DedupKey
	// asc. Stable across runs that produce the same set.
	Findings []Finding

	// LastRun is the wall-clock time the most recent batch completed.
	// Zero when the runner has never published.
	LastRun time.Time

	// Running is true while a RunNow batch is in flight. The UI
	// disables the Re-check button when set.
	Running bool

	// Generation is a monotonic counter that increments on every
	// publication. UI subscribers compare against their last-seen
	// value to skip redundant invalidates.
	Generation uint64
}

// Has reports whether any finding with id (any DedupKey) is present.
// Convenience for UI code asking "is there a blocker right now?".
func (s *Snapshot) Has(id string) bool {
	if s == nil {
		return false
	}
	for _, f := range s.Findings {
		if f.ID == id {
			return true
		}
	}
	return false
}

// HighestSeverity returns the worst severity present, or 0 (no findings).
func (s *Snapshot) HighestSeverity() Severity {
	if s == nil {
		return 0
	}
	var worst Severity
	for _, f := range s.Findings {
		if f.Severity > worst {
			worst = f.Severity
		}
	}
	return worst
}

// CountAt counts findings of the given severity. Convenience for UI badge
// logic ("show amber if ≥1 warn AND no blocker").
func (s *Snapshot) CountAt(level Severity) int {
	if s == nil {
		return 0
	}
	n := 0
	for _, f := range s.Findings {
		if f.Severity == level {
			n++
		}
	}
	return n
}
