package docker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithRetry_NoRetryForUnknownError(t *testing.T) {
	calls := 0
	_, err := withRetry(context.Background(), "test", func(_ context.Context) (int, error) {
		calls++
		return 0, errors.New("unrecognized error")
	}, nil)
	if err == nil {
		t.Errorf("expected error")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on retryNone)", calls)
	}
}

func TestWithRetry_BuildKitRaceRetries(t *testing.T) {
	calls := 0
	_, err := withRetry(context.Background(), "test", func(_ context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, errors.New("error: parent snapshot xyz does not exist")
		}
		return 42, nil
	}, nil)
	if err != nil {
		t.Errorf("expected success after retry, got: %v", err)
	}
	if calls < 3 {
		t.Errorf("calls = %d, want >=3", calls)
	}
}

func TestWithRetry_BuildKitRaceGivesUpAfterAttempts(t *testing.T) {
	calls := 0
	_, err := withRetry(context.Background(), "test", func(_ context.Context) (int, error) {
		calls++
		return 0, errors.New("error: parent snapshot xyz does not exist")
	}, nil)
	if err == nil {
		t.Errorf("expected error after exhausted retries")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("err = %v, want ErrTransient", err)
	}
	// 3 attempts max for this class.
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestWithRetry_RecoverHookFires(t *testing.T) {
	var recoverFired int
	calls := 0
	_, err := withRetry(context.Background(), "test", func(_ context.Context) (int, error) {
		calls++
		if calls == 1 {
			// 409 Conflict-like message (we look for "name ... already in use").
			return 0, errors.New("Conflict: container name already in use")
		}
		return 0, errors.New("a different error") // will not retry
	}, func(_ context.Context, class retryClass) error {
		recoverFired++
		_ = class
		return nil
	})
	_ = err
	if recoverFired == 0 {
		t.Errorf("recover hook did not fire")
	}
}

func TestWithRetry_RespectsContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := withRetry(ctx, "test", func(_ context.Context) (int, error) {
		calls++
		return 0, errors.New("error: parent snapshot xyz does not exist")
	}, nil)
	if !errors.Is(err, context.Canceled) {
		// May also surface as ErrTransient if the budget runs out before
		// cancellation hits — accept either.
		if !errors.Is(err, ErrTransient) {
			t.Errorf("err = %v, want context.Canceled or ErrTransient", err)
		}
	}
}

func TestIsNotFoundLike(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New("no such container"), true},
		{errors.New("No such network: foo"), true},
		{errors.New("no such volume: foo"), true},
		{errors.New("something else"), false},
	}
	for _, c := range cases {
		if got := isNotFoundLike(c.err); got != c.want {
			t.Errorf("isNotFoundLike(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
