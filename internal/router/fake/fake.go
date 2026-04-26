// Package fake is an in-memory Router used by tests that want to verify
// SiteManager wiring without spinning up Docker or Traefik.
package fake

import (
	"context"
	"sync"

	"github.com/PeterBooker/locorum/internal/router"
)

// Router records every call it receives. Use Calls() / Sites() / Services()
// to assert against the wiring.
type Router struct {
	mu       sync.Mutex
	running  bool
	sites    map[string]router.SiteRoute
	services map[string]router.ServiceRoute
	calls    []string
}

func New() *Router {
	return &Router{
		sites:    map[string]router.SiteRoute{},
		services: map[string]router.ServiceRoute{},
	}
}

func (r *Router) EnsureRunning(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = true
	r.calls = append(r.calls, "EnsureRunning")
	return nil
}

func (r *Router) Stop(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = false
	r.calls = append(r.calls, "Stop")
	return nil
}

func (r *Router) UpsertSite(_ context.Context, route router.SiteRoute) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sites[route.Slug] = route
	r.calls = append(r.calls, "UpsertSite:"+route.Slug)
	return nil
}

func (r *Router) RemoveSite(_ context.Context, slug string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sites, slug)
	r.calls = append(r.calls, "RemoveSite:"+slug)
	return nil
}

func (r *Router) UpsertService(_ context.Context, route router.ServiceRoute) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services[route.Name] = route
	r.calls = append(r.calls, "UpsertService:"+route.Name)
	return nil
}

func (r *Router) RemoveService(_ context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.services, name)
	r.calls = append(r.calls, "RemoveService:"+name)
	return nil
}

func (r *Router) Health(_ context.Context) (router.Health, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	expected := len(r.sites) + len(r.services)
	return router.Health{
		Reachable:     r.running,
		LoadedRouters: expected,
		Expected:      expected,
	}, nil
}

func (r *Router) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *Router) Sites() map[string]router.SiteRoute {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]router.SiteRoute, len(r.sites))
	for k, v := range r.sites {
		out[k] = v
	}
	return out
}

func (r *Router) Services() map[string]router.ServiceRoute {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]router.ServiceRoute, len(r.services))
	for k, v := range r.services {
		out[k] = v
	}
	return out
}

func (r *Router) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}
