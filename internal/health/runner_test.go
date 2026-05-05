package health

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCheck is the minimal Check used by runner tests.
type fakeCheck struct {
	id      string
	cadence time.Duration
	budget  time.Duration
	runs    atomic.Int32
	output  []Finding
	err     error
	delay   time.Duration
}

func (c *fakeCheck) ID() string             { return c.id }
func (c *fakeCheck) Cadence() time.Duration { return c.cadence }
func (c *fakeCheck) Budget() time.Duration  { return c.budget }
func (c *fakeCheck) Run(ctx context.Context) ([]Finding, error) {
	c.runs.Add(1)
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.output, c.err
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunnerRunNowProducesSnapshot(t *testing.T) {
	c := &fakeCheck{id: "ok", budget: time.Second, output: []Finding{
		{ID: "ok", Severity: SeverityInfo, Title: "Hello"},
	}}
	r := NewRunner(Options{Logger: quietLogger()}, c)
	defer r.Close()

	snap := r.RunNow(context.Background())
	if got := len(snap.Findings); got != 1 {
		t.Fatalf("expected 1 finding, got %d", got)
	}
	if snap.Findings[0].Detected.IsZero() {
		t.Error("Detected must be populated by the runner")
	}
	if snap.Generation == 0 {
		t.Error("Generation must increment on publish")
	}
}

func TestRunnerSortsBySeverity(t *testing.T) {
	c := &fakeCheck{id: "x", budget: time.Second, output: []Finding{
		{ID: "warn-a", Severity: SeverityWarn, Title: "warn a"},
		{ID: "block-z", Severity: SeverityBlocker, Title: "block z"},
		{ID: "info-m", Severity: SeverityInfo, Title: "info m"},
	}}
	r := NewRunner(Options{Logger: quietLogger()}, c)
	defer r.Close()

	snap := r.RunNow(context.Background())
	want := []string{"block-z", "warn-a", "info-m"}
	if len(snap.Findings) != len(want) {
		t.Fatalf("len mismatch: %d vs %d", len(snap.Findings), len(want))
	}
	for i, w := range want {
		if snap.Findings[i].ID != w {
			t.Errorf("position %d: got %q want %q", i, snap.Findings[i].ID, w)
		}
	}
}

func TestRunnerPreservesDetectedAcrossRuns(t *testing.T) {
	first := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	calls := atomic.Int32{}
	now := func() time.Time {
		// Run 1 returns first; run 2 returns first+1m. The runner
		// must NOT re-mint Detected on run 2 because the (ID, Severity,
		// DedupKey) key is unchanged.
		if calls.Load() < 1 {
			return first
		}
		return first.Add(time.Minute)
	}
	c := &fakeCheck{id: "x", budget: time.Second, output: []Finding{
		{ID: "stable", Severity: SeverityWarn, Title: "stable"},
	}}
	r := NewRunner(Options{Logger: quietLogger(), Now: now}, c)
	defer r.Close()

	r.RunNow(context.Background())
	calls.Store(2)
	snap := r.RunNow(context.Background())

	if got := snap.Findings[0].Detected; !got.Equal(first) {
		t.Errorf("Detected should be preserved at %v; got %v", first, got)
	}
}

func TestRunnerDedupKeyDistinguishesFindings(t *testing.T) {
	c := &fakeCheck{id: "x", budget: time.Second, output: []Finding{
		{ID: "wsl-mnt-c", Severity: SeverityWarn, DedupKey: "/mnt/c/a"},
		{ID: "wsl-mnt-c", Severity: SeverityWarn, DedupKey: "/mnt/c/b"},
	}}
	r := NewRunner(Options{Logger: quietLogger()}, c)
	defer r.Close()

	snap := r.RunNow(context.Background())
	if len(snap.Findings) != 2 {
		t.Fatalf("expected 2 distinct findings via DedupKey, got %d", len(snap.Findings))
	}
}

func TestRunnerCheckFailedWrapsErrors(t *testing.T) {
	c := &fakeCheck{id: "boom", budget: time.Second, err: errors.New("kaboom")}
	r := NewRunner(Options{Logger: quietLogger()}, c)
	defer r.Close()

	snap := r.RunNow(context.Background())
	if len(snap.Findings) != 1 {
		t.Fatalf("expected 1 wrapper finding, got %d", len(snap.Findings))
	}
	f := snap.Findings[0]
	if f.ID != "check-failed" {
		t.Errorf("expected ID=check-failed, got %q", f.ID)
	}
	if f.DedupKey != "boom" {
		t.Errorf("expected DedupKey=boom, got %q", f.DedupKey)
	}
}

func TestRunnerOverBudgetMarksStale(t *testing.T) {
	c := &fakeCheck{
		id: "slow", budget: 50 * time.Millisecond, delay: 200 * time.Millisecond,
		output: []Finding{{ID: "slow", Severity: SeverityWarn, Title: "slow"}},
	}
	r := NewRunner(Options{Logger: quietLogger()}, c)
	defer r.Close()

	snap := r.RunNow(context.Background())
	// The check returns ctx.Err on timeout — so the wrapper finding is
	// what we see. Stale on findings makes sense when the check returns
	// findings *and* takes too long; for context.DeadlineExceeded the
	// runner emits the check-failed envelope.
	if len(snap.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(snap.Findings))
	}
	if snap.Findings[0].ID != "check-failed" {
		t.Errorf("expected check-failed envelope from over-budget check; got %q", snap.Findings[0].ID)
	}
}

func TestRunnerSubscribeFiresOnPublish(t *testing.T) {
	c := &fakeCheck{id: "x", budget: time.Second}
	r := NewRunner(Options{Logger: quietLogger()}, c)
	defer r.Close()

	var mu sync.Mutex
	var received []uint64
	unsubscribe := r.Subscribe(func(s Snapshot) {
		mu.Lock()
		received = append(received, s.Generation)
		mu.Unlock()
	})
	defer unsubscribe()

	r.RunNow(context.Background())
	r.RunNow(context.Background())

	mu.Lock()
	defer mu.Unlock()
	// Each RunNow emits two snapshots: Running=true then Running=false.
	if len(received) < 2 {
		t.Errorf("expected ≥2 subscriber callbacks, got %d", len(received))
	}
}

func TestActionExecutorReentrancyGuard(t *testing.T) {
	r := NewRunner(Options{Logger: quietLogger()})
	defer r.Close()

	started := make(chan struct{})
	finish := make(chan struct{})
	a := Action{
		Run: func(ctx context.Context) error {
			close(started)
			<-finish
			return nil
		},
		Timeout: time.Second,
	}

	if err := r.SubmitAction(context.Background(), "x", a, nil); err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	<-started

	// While in flight, second submit must error with ErrAlreadyRunning.
	a2 := Action{Run: func(ctx context.Context) error { return nil }}
	if err := r.SubmitAction(context.Background(), "x", a2, nil); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("expected ErrAlreadyRunning, got %v", err)
	}

	close(finish)
}

func TestRunnerCloseIdempotent(t *testing.T) {
	r := NewRunner(Options{Logger: quietLogger()})
	if err := r.Close(); err != nil {
		t.Errorf("first close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second close: %v", err)
	}
}

func TestSnapshotHelpers(t *testing.T) {
	s := &Snapshot{Findings: []Finding{
		{ID: "a", Severity: SeverityWarn},
		{ID: "b", Severity: SeverityInfo},
		{ID: "c", Severity: SeverityWarn},
	}}
	if !s.Has("a") {
		t.Error("Has(a) should be true")
	}
	if s.Has("z") {
		t.Error("Has(z) should be false")
	}
	if got := s.HighestSeverity(); got != SeverityWarn {
		t.Errorf("HighestSeverity = %v, want Warn", got)
	}
	if got := s.CountAt(SeverityWarn); got != 2 {
		t.Errorf("CountAt(Warn) = %d, want 2", got)
	}
	if got := s.CountAt(SeverityBlocker); got != 0 {
		t.Errorf("CountAt(Blocker) = %d, want 0", got)
	}
}

func TestBreakerStateMachine(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	b := &Breaker{MaxFailures: 2, Cooldown: time.Hour, Now: clock}

	if b.State() != "closed" {
		t.Fatalf("initial state = %q, want closed", b.State())
	}
	b.OnFailure()
	if b.State() != "closed" {
		t.Errorf("after 1 failure: %q, want closed", b.State())
	}
	b.OnFailure()
	if b.State() != "open" {
		t.Errorf("after 2 failures: %q, want open", b.State())
	}
	if b.Allow() {
		t.Error("Allow should be false when open")
	}
	// Advance past cooldown.
	now = now.Add(2 * time.Hour)
	if b.State() != "half-open" {
		t.Errorf("after cooldown: %q, want half-open", b.State())
	}
	if !b.Allow() {
		t.Error("Allow should be true in half-open")
	}
	b.OnSuccess()
	if b.State() != "closed" {
		t.Errorf("after success in half-open: %q, want closed", b.State())
	}
}
