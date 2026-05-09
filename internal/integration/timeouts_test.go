//go:build integration

package integration

import (
	"context"
	"testing"
	"time"
)

// timeoutCtx wraps context.WithTimeout and registers cancel via
// t.Cleanup, replacing the lostcancel-prone `ctx, _ := WithTimeout(...)`.
func timeoutCtx(t *testing.T, parent context.Context, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, d)
	t.Cleanup(cancel)
	return ctx
}
