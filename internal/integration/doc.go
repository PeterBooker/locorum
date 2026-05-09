// Package integration holds real-Docker integration tests, all build-
// tagged `integration`. Linux-only — macOS/Windows CI runners have no
// Docker daemon (TESTING.md §3.2.2). Run with `make integration`.
//
// This file exists so `go list` doesn't trip on an otherwise-empty
// non-integration build of the package.
package integration
