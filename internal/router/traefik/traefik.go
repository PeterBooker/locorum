// Package traefik implements router.Router using a single Traefik v3
// container backed by Traefik's file provider. Dynamic configs are written
// to ~/.locorum/router/dynamic and Traefik reloads them via fsnotify.
//
// The orchestrator is the only thing in the codebase that knows about
// Traefik specifics — sites and app code interact through the
// router.Router interface.
package traefik

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	dcontainer "github.com/docker/docker/api/types/container"
	dnetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"golang.org/x/crypto/bcrypt"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/genmark"
	"github.com/PeterBooker/locorum/internal/platform"
	"github.com/PeterBooker/locorum/internal/router"
	tlspkg "github.com/PeterBooker/locorum/internal/tls"
	"github.com/PeterBooker/locorum/internal/version"
)

const (
	// ContainerName is the single Traefik container managed by Locorum.
	ContainerName = "locorum-global-router"

	// NetworkName is the bridge network shared with mail/adminer/per-site
	// web containers — Traefik resolves backends by container name on it.
	NetworkName = "locorum-global"

	// NetworkAlias is the DNS name other containers use to reach Traefik.
	NetworkAlias = "router"

	// AdminBindHost binds the Traefik admin API to host loopback only;
	// other host processes can poll it but external network cannot.
	AdminBindHost = "127.0.0.1"
	AdminPort     = "8888"

	// Container-side bind-mount targets. Aligned with host paths so cert
	// references inside dynamic configs translate by simple prefix swap.
	ContainerStaticPath = "/etc/traefik/static.yaml"
	ContainerDynamicDir = "/etc/traefik/dynamic"
	ContainerCertsDir   = "/etc/traefik/certs"

	tickInterval         = 250 * time.Millisecond
	routeAddTimeout      = 5 * time.Second
	startupReadyDeadline = 30 * time.Second

	// adminUsername is constant; the password is randomised per process.
	adminUsername = "locorum"
)

// Config controls Traefik orchestrator behavior. HomeDir is the user's home
// (the orchestrator owns ~/.locorum/router and ~/.locorum/certs underneath
// it). AppVersion is stamped on every container label.
//
// HTTPPort / HTTPSPort are the host-side bindings for plain and TLS
// traffic. Zero falls back to the IANA defaults (80/443) so existing
// callers that leave them unset retain the historical behaviour.
type Config struct {
	HomeDir    string
	AppVersion string
	LogLevel   string // INFO, DEBUG, WARN, ERROR; defaults to INFO
	HTTPPort   int    // host port for entrypoint :80; 0 → 80
	HTTPSPort  int    // host port for entrypoint :443; 0 → 443
}

// Router is the production router.Router implementation.
type Router struct {
	cfg      Config
	docker   *docker.Docker
	tls      tlspkg.Provider
	renderer *Renderer
	client   *Client

	// Admin API basic-auth credentials. Generated per process; the bcrypt
	// hash is written into api.yaml dynamic config so Traefik can verify
	// requests, while the plaintext password lives only in this struct
	// (and the Client) — never on disk.
	adminPassword string
	adminHash     string

	hostRouterDir  string
	hostStaticPath string
	hostDynamicDir string
	hostAPIPath    string
	hostCertsDir   string

	mu            sync.Mutex
	siteRoutes    map[string]struct{}
	serviceRoutes map[string]struct{}
}

// New constructs a Traefik-backed router. Templates are parsed eagerly from
// configFS so a malformed install fails before EnsureRunning is called. A
// random admin password is generated and bcrypt-hashed up front so the
// admin API is never exposed without auth.
func New(cfg Config, d *docker.Docker, prov tlspkg.Provider, configFS fs.FS) (*Router, error) {
	rend, err := NewRenderer(configFS)
	if err != nil {
		return nil, err
	}

	password, hash, err := generateAdminCredentials()
	if err != nil {
		return nil, fmt.Errorf("generate admin credentials: %w", err)
	}

	if cfg.HTTPPort == 0 {
		cfg.HTTPPort = 80
	}
	if cfg.HTTPSPort == 0 {
		cfg.HTTPSPort = 443
	}

	r := &Router{
		cfg:            cfg,
		docker:         d,
		tls:            prov,
		renderer:       rend,
		client:         NewClient("http://"+AdminBindHost+":"+AdminPort, adminUsername, password),
		adminPassword:  password,
		adminHash:      hash,
		hostRouterDir:  filepath.Join(cfg.HomeDir, ".locorum", "router"),
		hostStaticPath: filepath.Join(cfg.HomeDir, ".locorum", "router", "static.yaml"),
		hostDynamicDir: filepath.Join(cfg.HomeDir, ".locorum", "router", "dynamic"),
		hostAPIPath:    filepath.Join(cfg.HomeDir, ".locorum", "router", "dynamic", "api.yaml"),
		hostCertsDir:   filepath.Join(cfg.HomeDir, ".locorum", "certs"),
		siteRoutes:     map[string]struct{}{},
		serviceRoutes:  map[string]struct{}{},
	}
	return r, nil
}

// generateAdminCredentials returns a random base64-encoded password (32
// bytes of entropy) and its bcrypt hash. bcrypt cost is the minimum (4) —
// the password has 256 bits of entropy, so increasing the cost only slows
// down our own polling without meaningfully raising the brute-force bar.
func generateAdminCredentials() (password, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", err
	}
	password = base64.RawURLEncoding.EncodeToString(buf)
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		return "", "", err
	}
	return password, string(h), nil
}

func (r *Router) EnsureRunning(ctx context.Context) error {
	if err := r.prepareDirs(); err != nil {
		return fmt.Errorf("preparing router dirs: %w", err)
	}

	running, err := r.docker.ContainerIsRunning(ctx, ContainerName)
	if err != nil {
		return fmt.Errorf("inspecting router: %w", err)
	}
	if running {
		if _, err := r.client.HTTPRouters(ctx); err == nil {
			return nil
		}
		slog.Info("router stale, recreating")
		if err := r.docker.RemoveContainer(ctx, ContainerName); err != nil {
			return fmt.Errorf("remove stale router: %w", err)
		}
	} else {
		// Defensive: a stopped-but-existing container would block create.
		if exists, _ := r.docker.ContainerExists(ctx, ContainerName); exists {
			if err := r.docker.RemoveContainer(ctx, ContainerName); err != nil {
				return fmt.Errorf("remove stopped router: %w", err)
			}
		}
	}

	// We're starting fresh — any leftover per-site dynamic configs from a
	// previous (possibly crashed) session would route to backends that no
	// longer exist. Sweep them. Service configs are regenerated by the
	// caller via UpsertService, so leaving them is harmless.
	if err := r.cleanStaleSiteConfigs(); err != nil {
		return fmt.Errorf("clean stale site routes: %w", err)
	}
	if err := r.writeStaticConfig(); err != nil {
		return fmt.Errorf("writing static config: %w", err)
	}
	if err := r.writeAPIConfig(); err != nil {
		return fmt.Errorf("writing api config: %w", err)
	}
	if err := r.createContainer(ctx); err != nil {
		return fmt.Errorf("create router container: %w", classifyDockerStartError(err))
	}
	if err := r.waitReady(ctx); err != nil {
		return fmt.Errorf("router did not become ready: %w", err)
	}
	return nil
}

func (r *Router) cleanStaleSiteConfigs() error {
	matches, err := filepath.Glob(filepath.Join(r.hostDynamicDir, "site-*.yaml"))
	if err != nil {
		return err
	}
	for _, m := range matches {
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	r.mu.Lock()
	r.siteRoutes = map[string]struct{}{}
	r.mu.Unlock()
	return nil
}

func (r *Router) Stop(ctx context.Context) error {
	return r.docker.RemoveContainer(ctx, ContainerName)
}

func (r *Router) UpsertSite(ctx context.Context, route router.SiteRoute) error {
	cert, certErr := r.ensureCert(ctx, "site-"+route.Slug, hostnamesFor(route))
	if certErr != nil {
		slog.Warn("issuing site cert failed; site will be HTTP-only",
			"slug", route.Slug, "err", certErr)
	}

	payload, err := r.renderer.Site(route, r.containerCert(cert))
	if err != nil {
		return fmt.Errorf("render site %q: %w", route.Slug, err)
	}

	target := filepath.Join(r.hostDynamicDir, "site-"+route.Slug+".yaml")
	wasPresent := fileExists(target)
	if err := genmark.WriteAtomic(target, payload, 0o600); err != nil {
		return fmt.Errorf("write site config: %w", err)
	}

	r.mu.Lock()
	_, alreadyKnown := r.siteRoutes[route.Slug]
	r.siteRoutes[route.Slug] = struct{}{}
	expected := r.expectedRouterCountLocked()
	r.mu.Unlock()

	if !wasPresent && !alreadyKnown {
		if err := r.waitForRouterCount(ctx, expected); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) RemoveSite(ctx context.Context, slug string) error {
	target := filepath.Join(r.hostDynamicDir, "site-"+slug+".yaml")
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove site config: %w", err)
	}
	if err := r.tls.Remove(ctx, "site-"+slug); err != nil {
		slog.Warn("remove cert failed", "slug", slug, "err", err)
	}
	r.mu.Lock()
	delete(r.siteRoutes, slug)
	r.mu.Unlock()
	return nil
}

func (r *Router) UpsertService(ctx context.Context, route router.ServiceRoute) error {
	cert, certErr := r.ensureCert(ctx, "svc-"+route.Name, route.Hostnames)
	if certErr != nil {
		slog.Warn("issuing service cert failed; service will be HTTP-only",
			"name", route.Name, "err", certErr)
	}

	payload, err := r.renderer.Service(route, r.containerCert(cert))
	if err != nil {
		return fmt.Errorf("render service %q: %w", route.Name, err)
	}

	target := filepath.Join(r.hostDynamicDir, "svc-"+route.Name+".yaml")
	wasPresent := fileExists(target)
	if err := genmark.WriteAtomic(target, payload, 0o600); err != nil {
		return fmt.Errorf("write service config: %w", err)
	}

	r.mu.Lock()
	_, alreadyKnown := r.serviceRoutes[route.Name]
	r.serviceRoutes[route.Name] = struct{}{}
	expected := r.expectedRouterCountLocked()
	r.mu.Unlock()

	if !wasPresent && !alreadyKnown {
		if err := r.waitForRouterCount(ctx, expected); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) RemoveService(ctx context.Context, name string) error {
	target := filepath.Join(r.hostDynamicDir, "svc-"+name+".yaml")
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove service config: %w", err)
	}
	if err := r.tls.Remove(ctx, "svc-"+name); err != nil {
		slog.Warn("remove cert failed", "name", name, "err", err)
	}
	r.mu.Lock()
	delete(r.serviceRoutes, name)
	r.mu.Unlock()
	return nil
}

func (r *Router) Health(ctx context.Context) (router.Health, error) {
	routers, err := r.client.HTTPRouters(ctx)
	if err != nil {
		return router.Health{Reachable: false}, err
	}

	var errs []string
	for _, rt := range routers {
		if rt.Status == "disabled" {
			errs = append(errs, fmt.Sprintf("router %q is disabled: %v", rt.Name, rt.Errors))
			continue
		}
		if len(rt.Errors) > 0 {
			errs = append(errs, fmt.Sprintf("router %q: %v", rt.Name, rt.Errors))
		}
	}

	r.mu.Lock()
	expected := r.expectedRouterCountLocked()
	r.mu.Unlock()

	return router.Health{
		Reachable:     true,
		LoadedRouters: len(routers),
		Expected:      expected,
		Errors:        errs,
	}, nil
}

// expectedRouterCountLocked returns the number of HTTP routers the engine
// should report once configs have been picked up. The "+1" accounts for
// the locorum-api router we register ourselves to expose the admin API.
// Caller must hold r.mu.
func (r *Router) expectedRouterCountLocked() int {
	return 1 + len(r.siteRoutes) + len(r.serviceRoutes)
}

func (r *Router) prepareDirs() error {
	for _, dir := range []string{
		filepath.Dir(r.hostStaticPath),
		r.hostDynamicDir,
		r.hostCertsDir,
	} {
		// 0o700: router configs include the bcrypt-hashed admin API password
		// and references to per-site TLS keys; cert dirs hold the keys
		// themselves. None of this should be readable by other local users.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %q: %w", dir, err)
		}
	}
	return nil
}

func (r *Router) writeStaticConfig() error {
	payload, err := r.renderer.Static(r.cfg.LogLevel)
	if err != nil {
		return err
	}
	return genmark.WriteAtomic(r.hostStaticPath, payload, 0o600)
}

// writeAPIConfig writes the dynamic config that exposes Traefik's admin
// API on the internal entrypoint behind basic auth. Re-rendered on every
// EnsureRunning so the credentials match this process's in-memory copy.
func (r *Router) writeAPIConfig() error {
	payload, err := r.renderer.API(adminUsername, r.adminHash)
	if err != nil {
		return err
	}
	// API config holds the admin password's bcrypt hash; restrict
	// permissions so non-owner users cannot read it.
	return genmark.WriteAtomic(r.hostAPIPath, payload, 0o600)
}

func (r *Router) createContainer(ctx context.Context) error {
	cfg := &dcontainer.Config{
		Image:  version.TraefikImage,
		Cmd:    []string{"--configfile=" + ContainerStaticPath},
		Labels: docker.PlatformLabels(docker.RoleRouter, "", r.cfg.AppVersion),
		ExposedPorts: nat.PortSet{
			"80/tcp":                     {},
			"443/tcp":                    {},
			nat.Port(AdminPort + "/tcp"): {},
		},
	}

	// platform.DockerPath normalises slashes for every supported host;
	// without it a Windows native build would hand `\` to Docker and the
	// bind would silently fail with "no such file or directory".
	hostCfg := &dcontainer.HostConfig{
		Binds: []string{
			platform.DockerPath(r.hostStaticPath) + ":" + ContainerStaticPath + ":ro",
			platform.DockerPath(r.hostDynamicDir) + ":" + ContainerDynamicDir + ":ro",
			platform.DockerPath(r.hostCertsDir) + ":" + ContainerCertsDir + ":ro",
		},
		PortBindings: nat.PortMap{
			"80/tcp":                     {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(r.cfg.HTTPPort)}},
			"443/tcp":                    {{HostIP: "0.0.0.0", HostPort: strconv.Itoa(r.cfg.HTTPSPort)}},
			nat.Port(AdminPort + "/tcp"): {{HostIP: AdminBindHost, HostPort: AdminPort}},
		},
		NetworkMode:   dcontainer.NetworkMode(NetworkName),
		RestartPolicy: dcontainer.RestartPolicy{Name: dcontainer.RestartPolicyUnlessStopped},
	}

	netCfg := &dnetwork.NetworkingConfig{
		EndpointsConfig: map[string]*dnetwork.EndpointSettings{
			NetworkName: {Aliases: []string{NetworkAlias}},
		},
	}

	return r.docker.CreateContainer(ctx, ContainerName, version.TraefikImage, cfg, hostCfg, netCfg)
}

func (r *Router) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(startupReadyDeadline)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if _, err := r.client.HTTPRouters(ctx); err == nil {
			return nil
		}
		time.Sleep(tickInterval)
	}
	return fmt.Errorf("router admin API unreachable after %v", startupReadyDeadline)
}

func (r *Router) waitForRouterCount(ctx context.Context, expected int) error {
	deadline := time.Now().Add(routeAddTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		routers, err := r.client.HTTPRouters(ctx)
		if err == nil && len(routers) >= expected {
			return nil
		}
		time.Sleep(tickInterval)
	}
	return fmt.Errorf("route did not appear in router after %v (expected %d)", routeAddTimeout, expected)
}

func (r *Router) ensureCert(ctx context.Context, name string, hostnames []string) (tlspkg.CertPath, error) {
	status, err := r.tls.Available(ctx)
	if err != nil {
		return tlspkg.CertPath{}, err
	}
	if !status.Installed {
		return tlspkg.CertPath{}, fmt.Errorf("tls provider unavailable: %s", status.Message)
	}
	return r.tls.Issue(ctx, tlspkg.CertSpec{Name: name, Hostnames: hostnames})
}

func (r *Router) containerCert(cert tlspkg.CertPath) tlspkg.CertPath {
	if cert.IsZero() {
		return cert
	}
	return tlspkg.CertPath{
		CertFile: containerCertPath(cert.CertFile, r.hostCertsDir, ContainerCertsDir),
		KeyFile:  containerCertPath(cert.KeyFile, r.hostCertsDir, ContainerCertsDir),
	}
}

func hostnamesFor(route router.SiteRoute) []string {
	out := []string{route.PrimaryHost}
	out = append(out, route.ExtraHosts...)
	if route.WildcardHost != "" {
		out = append(out, route.WildcardHost)
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
