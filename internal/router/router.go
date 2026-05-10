// Package router defines the contract for the global routing layer that
// terminates TLS, performs hostname-based routing, and forwards traffic to
// per-site web containers and global services (mail, adminer).
//
// The interface is the seam between sites/app code and the routing engine.
// The production implementation lives in router/traefik; router/fake is
// available for tests.
package router

import (
	"context"

	"github.com/PeterBooker/locorum/internal/tls"
)

// Router orchestrates the global routing layer. Implementations must be
// safe for concurrent use.
type Router interface {
	// EnsureRunning creates or updates the router container and waits for
	// it to be healthy. Idempotent — safe to call repeatedly during
	// Initialize.
	EnsureRunning(ctx context.Context) error

	// Stop stops and removes the router container. On-disk dynamic configs
	// are kept so a subsequent EnsureRunning resumes routing instantly.
	Stop(ctx context.Context) error

	// UpsertSite registers or updates routing for a site. Hot-reloaded;
	// returns once the new routes are observable via Health.
	UpsertSite(ctx context.Context, route SiteRoute) error

	// RemoveSite removes routing for a site. Hot-reloaded.
	RemoveSite(ctx context.Context, slug string) error

	// UpsertService registers a global service (mail, adminer, future addons).
	UpsertService(ctx context.Context, route ServiceRoute) error

	// RemoveService removes a global service by name.
	RemoveService(ctx context.Context, name string) error

	// Health reports whether the router is reachable, how many routes are
	// loaded, and any configuration errors surfaced by the engine.
	Health(ctx context.Context) (Health, error)
}

// SiteRoute describes one site's routing requirements. Slug is the
// canonical identifier; PrimaryHost is what users type into a browser.
type SiteRoute struct {
	Slug               string
	PrimaryHost        string   // e.g. "myslug.localhost"
	ExtraHosts         []string // additional hostnames (no wildcards)
	WildcardHost       string   // "" or "*.myslug.localhost" for multisite subdomain
	ExtraWildcardHosts []string // additional wildcard hosts (e.g. "*.myslug.<lan>.sslip.io")
	Backend            string   // e.g. "http://locorum-myslug-web:80"
	Cert               tls.CertPath
}

// ServiceRoute describes a global service exposed under a fixed hostname.
type ServiceRoute struct {
	Name      string   // "mail", "adminer"
	Hostnames []string // primary + aliases
	Backend   string   // e.g. "http://locorum-global-mail:8025"
	Cert      tls.CertPath
}

// Health reports the live state of the router.
type Health struct {
	Reachable     bool     // admin API responded
	LoadedRouters int      // number of HTTP routers reported by the engine
	Expected      int      // number Locorum has written; LoadedRouters should >= this once stable
	Errors        []string // configuration errors surfaced by the admin API
}
