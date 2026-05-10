package health

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsSink is the optional observability hook. Default is the no-op
// sink; tests inject a recorder to assert per-check timing and outcomes.
type MetricsSink interface {
	ObserveCheck(id string, dur time.Duration, findings int, err error)
	ObserveAction(id string, dur time.Duration, err error)
}

// noopMetrics is the default sink — does nothing. Avoids nil-checks at
// every call site.
type noopMetrics struct{}

func (noopMetrics) ObserveCheck(string, time.Duration, int, error) {}
func (noopMetrics) ObserveAction(string, time.Duration, error)     {}

// Default tuning. Documented in CROSS-PLATFORM.md § Performance budgets.
const (
	defaultMinCadence  = 5 * time.Minute
	defaultBudget      = 5 * time.Second
	defaultSemSize     = 8
	defaultActionPool  = 4
	suspendBudgetTrips = 3
)

// Options is the runner constructor parameters. All fields have sensible
// zero-value defaults so the simplest call site is `health.NewRunner(health.Options{}, checks...)`.
type Options struct {
	// MinCadence is the minimum interval between check runs. Each check
	// runs at max(check.Cadence(), MinCadence). Default 5m.
	MinCadence time.Duration

	// MaxConcurrentChecks bounds the number of checks running at once.
	// Default 8.
	MaxConcurrentChecks int

	// MaxConcurrentActions bounds the number of [Action.Run] calls in
	// flight. Default 4.
	MaxConcurrentActions int

	// Logger is the slog logger. Default slog.Default(); typically
	// callers pass slog.With("subsys", "health").
	Logger *slog.Logger

	// Metrics is the observability sink. Default no-op.
	Metrics MetricsSink

	// Now overrides time.Now for tests.
	Now func() time.Time
}

// runnerState is the dedup + suspension book-keeping. Guarded by
// runnerState.mu. Keeping it separate from Runner means the read path
// (Snapshot()) needs no lock — atomic.Pointer is enough.
type runnerState struct {
	mu sync.Mutex

	// Per-finding key (id|severity|dedupKey) → first-seen wall clock.
	// Drives the "noticed 3m ago" rendering.
	firstSeen map[string]time.Time

	// Per-check budget-breach counter. Decrements on a successful run
	// (within budget); reaching suspendBudgetTrips suspends the check
	// for one cycle.
	budgetTrips map[string]int

	// Per-check next-allowed-run time. Drives suspension-cooldown.
	nextRunAt map[string]time.Time
}

// Runner schedules Checks and publishes the aggregated Snapshot. Construct
// once via [NewRunner], call [Runner.Start] to begin the periodic loop, and
// [Runner.Close] at shutdown.
type Runner struct {
	opts    Options
	checks  []Check
	sem     chan struct{}
	actions *actionExecutor
	snap    atomic.Pointer[Snapshot]
	state   *runnerState

	subscribersMu sync.Mutex
	subscribers   []func(Snapshot)

	closeOnce sync.Once
	cancel    context.CancelFunc
	doneCh    chan struct{}
}

// NewRunner constructs a runner with the given checks. The returned runner
// has not yet started ticking — call [Runner.Start] when the caller is
// ready to begin scheduling.
func NewRunner(opts Options, checks ...Check) *Runner {
	if opts.MinCadence <= 0 {
		opts.MinCadence = defaultMinCadence
	}
	if opts.MaxConcurrentChecks <= 0 {
		opts.MaxConcurrentChecks = defaultSemSize
	}
	if opts.MaxConcurrentActions <= 0 {
		opts.MaxConcurrentActions = defaultActionPool
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Metrics == nil {
		opts.Metrics = noopMetrics{}
	}

	r := &Runner{
		opts:   opts,
		checks: append([]Check(nil), checks...),
		sem:    make(chan struct{}, opts.MaxConcurrentChecks),
		state: &runnerState{
			firstSeen:   map[string]time.Time{},
			budgetTrips: map[string]int{},
			nextRunAt:   map[string]time.Time{},
		},
		doneCh: make(chan struct{}),
	}
	r.actions = newActionExecutor(opts.MaxConcurrentActions, opts.Logger, opts.Metrics)
	// Publish an empty snapshot so reads before the first batch don't
	// see a nil pointer.
	r.snap.Store(&Snapshot{})
	return r
}

// Start kicks off the periodic loop. The loop runs until ctx is cancelled
// or [Runner.Close] is called. Subsequent calls are no-ops.
//
// One initial RunNow fires immediately; subsequent runs follow the cadence.
// This means the UI gets a populated snapshot within seconds of startup,
// not after waiting for the first 5-minute tick.
func (r *Runner) Start(ctx context.Context) {
	if r.cancel != nil {
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	go r.loop(loopCtx)
}

// loop is the runner's heartbeat. Cadence is opts.MinCadence; per-check
// next-run-at is checked at every tick so checks with longer cadences
// silently skip until their slot.
func (r *Runner) loop(ctx context.Context) {
	defer close(r.doneCh)

	// Fire one immediate batch; users opening the app shouldn't wait
	// 5 minutes to see "Docker is down".
	r.runBatch(ctx)

	t := time.NewTicker(r.opts.MinCadence)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.runBatch(ctx)
		}
	}
}

// RunNow forces a fresh batch and returns the resulting snapshot. Used by
// the "Re-check" button in the UI. Safe to call at any time; multiple
// concurrent callers each see the latest published snapshot but only one
// batch runs at a time (we serialise via the sem semaphore — a check
// already running is awaited rather than started twice).
func (r *Runner) RunNow(ctx context.Context) Snapshot {
	r.runBatch(ctx)
	return *r.snap.Load()
}

// Snapshot returns the most recently published snapshot. Lock-free —
// dereferences an atomic.Pointer.
func (r *Runner) Snapshot() Snapshot {
	return *r.snap.Load()
}

// DiskFreeBytes returns the most recent host-filesystem free-bytes
// reading from the disk-low check, or 0 if no DiskLowCheck is registered
// or it has not produced a reading yet. Used by the UI status-bar
// segment so it doesn't have to maintain its own polling cadence.
func (r *Runner) DiskFreeBytes() int64 {
	for _, c := range r.checks {
		if dl, ok := c.(*DiskLowCheck); ok {
			free, _ := dl.LastSnapshot()
			return free
		}
	}
	return 0
}

// Subscribe registers a callback that fires after each snapshot
// publication. The callback runs on the runner goroutine; it should be
// non-blocking (typical body: copy the snapshot into UI state and call
// window.Invalidate()).
//
// Returned closure detaches the subscription.
func (r *Runner) Subscribe(fn func(Snapshot)) (unsubscribe func()) {
	r.subscribersMu.Lock()
	r.subscribers = append(r.subscribers, fn)
	idx := len(r.subscribers) - 1
	r.subscribersMu.Unlock()

	return func() {
		r.subscribersMu.Lock()
		defer r.subscribersMu.Unlock()
		if idx >= len(r.subscribers) {
			return
		}
		r.subscribers[idx] = nil
	}
}

// Close stops the loop, drains the action executor, and unblocks any
// sleeping Start. Idempotent.
func (r *Runner) Close() error {
	r.closeOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
			<-r.doneCh
		}
		r.actions.close()
	})
	return nil
}

// SubmitAction enqueues a Finding's Action.Run for execution. Returns
// ErrAlreadyRunning if the same finding ID is already in flight. The done
// callback fires on the executor goroutine — UI must Invalidate and not
// block.
func (r *Runner) SubmitAction(ctx context.Context, id string, a Action, done func(error)) error {
	return r.actions.submit(ctx, id, a, done)
}

// runBatch is the meat of the runner: schedule checks, collect findings,
// dedup, sort, publish.
func (r *Runner) runBatch(ctx context.Context) {
	// Mark "running"; UI disables Re-check.
	r.publishRunning(true)
	defer r.publishRunning(false)

	now := r.now()
	type result struct {
		check Check
		out   []Finding
		err   error
		dur   time.Duration
		over  bool
	}
	resultsCh := make(chan result, len(r.checks))

	var wg sync.WaitGroup
	for _, c := range r.checks {
		// Suspension: skip this check if its next-run-at is in the future.
		if r.suspendedUntil(c.ID()).After(now) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Bound concurrency. ctx is the loop ctx — cancellation
			// propagates correctly.
			select {
			case r.sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-r.sem }()

			budget := c.Budget()
			if budget <= 0 {
				budget = defaultBudget
			}
			cctx, cancel := context.WithTimeout(ctx, budget)
			start := r.now()
			out, err := r.safeRun(cctx, c)
			cancel()
			dur := r.now().Sub(start)
			over := errors.Is(err, context.DeadlineExceeded) || dur > budget

			resultsCh <- result{check: c, out: out, err: err, dur: dur, over: over}
		}()
	}
	wg.Wait()
	close(resultsCh)

	// Collate results. Errors get translated into a `check-failed:<id>`
	// warn finding so the user sees something is wrong even on internal
	// failures.
	var collected []Finding
	for res := range resultsCh {
		r.opts.Metrics.ObserveCheck(res.check.ID(), res.dur, len(res.out), res.err)
		r.recordBudget(res.check.ID(), res.over)

		if res.err != nil {
			r.opts.Logger.Error("health: check failed",
				"check", res.check.ID(),
				"duration_ms", res.dur.Milliseconds(),
				"err", res.err.Error(),
			)
			collected = append(collected, Finding{
				ID:          "check-failed",
				DedupKey:    res.check.ID(),
				Severity:    SeverityWarn,
				Title:       fmt.Sprintf("Health check %q failed", res.check.ID()),
				Detail:      res.err.Error(),
				Remediation: "Check the application logs and re-run.",
			})
			continue
		}
		for _, f := range res.out {
			if res.over {
				f.Stale = true
			}
			collected = append(collected, f)
		}
		r.opts.Logger.Debug("health: check ok",
			"check", res.check.ID(),
			"duration_ms", res.dur.Milliseconds(),
			"findings", len(res.out),
		)
	}

	r.publish(collected)
}

// safeRun runs a single check, catching panics so a misbehaving check
// can't take down the runner goroutine.
func (r *Runner) safeRun(ctx context.Context, c Check) (out []Finding, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	return c.Run(ctx)
}

// recordBudget tracks consecutive over-budget runs per check. After
// suspendBudgetTrips breaches we suspend the check for one MinCadence cycle.
func (r *Runner) recordBudget(id string, overBudget bool) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	if !overBudget {
		delete(r.state.budgetTrips, id)
		return
	}
	r.state.budgetTrips[id]++
	if r.state.budgetTrips[id] >= suspendBudgetTrips {
		r.state.nextRunAt[id] = r.now().Add(r.opts.MinCadence)
		r.opts.Logger.Warn("health: check suspended for over-budget breaches",
			"check", id,
			"cycles", r.state.budgetTrips[id],
		)
		// Reset the counter so we re-evaluate after the cooldown.
		r.state.budgetTrips[id] = 0
	}
}

func (r *Runner) suspendedUntil(id string) time.Time {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return r.state.nextRunAt[id]
}

// publishRunning toggles the Running flag without rebuilding the rest of
// the snapshot. Used by runBatch to bracket "I am working" and "I am idle".
func (r *Runner) publishRunning(running bool) {
	prev := r.snap.Load()
	cp := *prev
	cp.Running = running
	r.snap.Store(&cp)
	r.notifySubscribers(cp)
}

// publish builds the new snapshot from collected findings + state and
// stores it, increments Generation, fires subscribers.
func (r *Runner) publish(collected []Finding) {
	r.state.mu.Lock()

	// Dedup against firstSeen. Build a fresh map of "still present" keys
	// so we can drop stale firstSeen entries (otherwise it grows
	// monotonically over a long-running session).
	stillPresent := make(map[string]struct{}, len(collected))
	now := r.now()
	for i := range collected {
		f := &collected[i]
		key := findingKey(*f)
		stillPresent[key] = struct{}{}
		if first, ok := r.state.firstSeen[key]; ok {
			f.Detected = first
		} else {
			f.Detected = now
			r.state.firstSeen[key] = now
		}
	}
	for key := range r.state.firstSeen {
		if _, ok := stillPresent[key]; !ok {
			delete(r.state.firstSeen, key)
		}
	}

	r.state.mu.Unlock()

	// Sort: blocker before warn before info; then ID asc; then DedupKey asc.
	sort.SliceStable(collected, func(i, j int) bool {
		a, b := collected[i], collected[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.DedupKey < b.DedupKey
	})

	prev := r.snap.Load()
	next := Snapshot{
		Findings:   collected,
		LastRun:    now,
		Running:    false,
		Generation: prev.Generation + 1,
	}
	r.snap.Store(&next)
	r.notifySubscribers(next)
}

func (r *Runner) notifySubscribers(s Snapshot) {
	r.subscribersMu.Lock()
	subs := make([]func(Snapshot), len(r.subscribers))
	copy(subs, r.subscribers)
	r.subscribersMu.Unlock()
	for _, fn := range subs {
		if fn == nil {
			continue
		}
		fn(s)
	}
}

func (r *Runner) now() time.Time {
	if r.opts.Now != nil {
		return r.opts.Now()
	}
	return time.Now()
}

// findingKey is the dedup key used by firstSeen. Two findings sharing an
// (ID, Severity, DedupKey) triple are the "same observation".
func findingKey(f Finding) string {
	// Use a separator unlikely to appear in IDs (which are slug-shaped).
	return f.ID + "|" + f.Severity.String() + "|" + f.DedupKey
}
