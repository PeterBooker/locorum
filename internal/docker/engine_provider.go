package docker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/PeterBooker/locorum/internal/version"
)

// providerLogOnce ensures the "could not parse Docker server version" warning
// fires at most once per process. The pure-text path is fine to ignore on
// the second-and-later calls; we already logged it.
var providerLogOnce sync.Once

// ProviderInfo returns Docker daemon identification, cached after first
// successful call. Concurrent callers share the same lookup; cache misses
// re-fetch.
func (d *Docker) ProviderInfo(ctx context.Context) (ProviderInfo, error) {
	d.pmu.RLock()
	cached := d.pinfo
	d.pmu.RUnlock()
	if cached != nil {
		return *cached, nil
	}

	info, err := d.cli.Info(ctx)
	if err != nil {
		return ProviderInfo{}, fmt.Errorf("docker info: %w", err)
	}

	parsed := version.ParseDockerServer(info.ServerVersion)
	if parsed.IsZero() && info.ServerVersion != "" {
		providerLogOnce.Do(func() {
			slog.Warn("docker: could not parse server version", "raw", info.ServerVersion)
		})
	}

	pi := ProviderInfo{
		Name:            classifyProviderName(info.Name, info.OperatingSystem),
		OperatingSystem: info.OperatingSystem,
		OSType:          info.OSType,
		Architecture:    info.Architecture,
		ServerVersion:   info.ServerVersion,
		ServerVersionP:  parsed,
		Rootless:        isRootless(info.SecurityOptions),
		IsDockerDesktop: isDockerDesktop(info.OperatingSystem, info.Name),
		NCPU:            info.NCPU,
		MemTotal:        info.MemTotal,
	}

	d.pmu.Lock()
	d.pinfo = &pi
	d.pmu.Unlock()
	return pi, nil
}

// RefreshProviderInfo invalidates the cached provider info. Call after the
// daemon may have changed (e.g. user restarted Docker Desktop).
func (d *Docker) RefreshProviderInfo() {
	d.pmu.Lock()
	d.pinfo = nil
	d.pmu.Unlock()
}

func classifyProviderName(daemonName, os string) string {
	osLower := strings.ToLower(os)
	switch {
	case strings.Contains(osLower, "docker desktop"):
		return "Docker Desktop"
	case strings.Contains(osLower, "orbstack"):
		return "OrbStack"
	case strings.Contains(osLower, "rancher"):
		return "Rancher Desktop"
	case strings.Contains(osLower, "colima"):
		return "Colima"
	case strings.Contains(osLower, "podman"):
		return "Podman"
	}
	if daemonName != "" {
		return daemonName
	}
	return "docker"
}

func isDockerDesktop(os, name string) bool {
	osLower := strings.ToLower(os)
	if strings.Contains(osLower, "docker desktop") {
		return true
	}
	nameLower := strings.ToLower(name)
	return strings.Contains(nameLower, "docker-desktop")
}

// isRootless inspects info.SecurityOptions for "rootless" entries. Docker
// reports e.g. "name=rootless" in this list when running rootless.
func isRootless(opts []string) bool {
	for _, o := range opts {
		if strings.Contains(strings.ToLower(o), "rootless") {
			return true
		}
	}
	return false
}
