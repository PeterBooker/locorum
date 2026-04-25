// Package version exposes build-time identity for the Locorum binary.
// Values are overridden via -ldflags "-X" at release-build time.
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)
