// Package testutil holds shared test fixtures and helpers.
//
// Scope rules — keep this package small:
//
//  1. Helpers take testing.TB and register their own teardown via
//     t.Cleanup. Callers should never plumb teardown manually.
//  2. No imports from internal/ui. UI rendering tests live next to the
//     widget under internal/ui/.
//
// See TESTING.md §3.1.
package testutil
