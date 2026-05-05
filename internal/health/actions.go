package health

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ErrAlreadyRunning is returned by [Runner.SubmitAction] when the same
// finding ID already has an action in flight. UI code can ignore this
// silently; the second click is a duplicate.
var ErrAlreadyRunning = errors.New("health: action already running")

// actionExecutor runs Action.Run on a bounded worker pool. Per-finding-ID
// re-entrancy is enforced so the same fix can't run twice concurrently.
//
// The done callback fires *on the executor goroutine*. UI subscribers
// must not block — the convention is to invalidate the window and store
// the error in UIState.
type actionExecutor struct {
	sem      chan struct{}
	inflight sync.Map // string → *atomic.Bool
	log      *slog.Logger
	metrics  MetricsSink

	closeOnce sync.Once
	closeCh   chan struct{}
	wg        sync.WaitGroup
}

func newActionExecutor(cap int, log *slog.Logger, metrics MetricsSink) *actionExecutor {
	if cap <= 0 {
		cap = defaultActionPool
	}
	return &actionExecutor{
		sem:     make(chan struct{}, cap),
		log:     log,
		metrics: metrics,
		closeCh: make(chan struct{}),
	}
}

// submit kicks off Action.Run for a given finding ID. Returns
// ErrAlreadyRunning if the same ID is already in flight.
func (e *actionExecutor) submit(ctx context.Context, id string, a Action, done func(error)) error {
	if a.Run == nil {
		return errors.New("health: action has no Run func")
	}

	guard, _ := e.inflight.LoadOrStore(id, &atomic.Bool{})
	bg := guard.(*atomic.Bool)
	if !bg.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}

	timeout := a.Timeout
	if timeout <= 0 {
		timeout = DefaultActionTimeout
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()

		// Bound concurrency. Bail if Close is in flight.
		select {
		case e.sem <- struct{}{}:
		case <-e.closeCh:
			bg.Store(false)
			return
		case <-ctx.Done():
			bg.Store(false)
			if done != nil {
				done(ctx.Err())
			}
			return
		}
		defer func() { <-e.sem }()
		defer bg.Store(false)

		actCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		start := time.Now()
		err := safeRunAction(actCtx, a)
		dur := time.Since(start)

		outcome := "ok"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			outcome = "timeout"
		case err != nil:
			outcome = "err"
		}
		e.log.Info("health: action complete",
			"action", id,
			"trigger", "user",
			"outcome", outcome,
			"duration_ms", dur.Milliseconds(),
			"err", errOrEmpty(err),
		)
		e.metrics.ObserveAction(id, dur, err)

		if done != nil {
			done(err)
		}
	}()
	return nil
}

func safeRunAction(ctx context.Context, a Action) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = errors.New("action panicked")
		}
	}()
	return a.Run(ctx)
}

func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// close drains in-flight actions and prevents future submits from starting
// new ones. Idempotent.
func (e *actionExecutor) close() {
	e.closeOnce.Do(func() {
		close(e.closeCh)
		e.wg.Wait()
	})
}
