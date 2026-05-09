package health

import (
	"context"
	"strconv"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
)

// PortConflictCheck warns when 80 or 443 is held by a process that isn't
// our router. The flow is described in CROSS-PLATFORM.md note v2 #1:
//
//  1. dial 127.0.0.1:port. No listener → no finding.
//  2. listener present. Ask docker engine whether
//     locorum-global-router is running. If yes, the port is ours by
//     design — no finding. If no, somebody else holds it; emit a warning.
//
// Cheap (one TCP SYN + one container-list lookup) and never binds the port.
type PortConflictCheck struct {
	engine    docker.Engine
	port      int
	routerCN  string // container name to attribute the bind to (locorum-global-router)
	humanPort string // for finding text ("80", "443")
}

// NewPortConflictCheck constructs a check for a single port. The router's
// container name is a parameter so the health package doesn't depend on
// internal/router/traefik (which would drag genmark + tls + docker spec
// templating into our import graph). The Bundled() factory passes
// traefik.ContainerName.
func NewPortConflictCheck(engine docker.Engine, port int, routerContainerName string) *PortConflictCheck {
	return &PortConflictCheck{
		engine:    engine,
		port:      port,
		routerCN:  routerContainerName,
		humanPort: strconv.Itoa(port),
	}
}

func (c *PortConflictCheck) ID() string           { return "port-conflict-" + c.humanPort }
func (*PortConflictCheck) Cadence() time.Duration { return 5 * time.Minute }
func (*PortConflictCheck) Budget() time.Duration  { return time.Second }

func (c *PortConflictCheck) Run(ctx context.Context) ([]Finding, error) {
	if !portInUse(ctx, c.port) {
		return nil, nil
	}
	// Listener present. Is it our router?
	running, err := c.engine.ContainerIsRunning(ctx, c.routerCN)
	if err == nil && running {
		// Locorum's own bind. Nothing to warn about.
		return nil, nil
	}
	return []Finding{{
		ID:       c.ID(),
		Severity: SeverityWarn,
		Title:    "Port " + c.humanPort + " is held by another process",
		Detail: "Another process is listening on 127.0.0.1:" + c.humanPort +
			". Locorum's router cannot bind it; sites may be unreachable.",
		Remediation: "Stop the conflicting service (`lsof -iTCP:" + c.humanPort + "` lists candidates), then re-check.",
		HelpURL:     "https://docs.locorum.dev/troubleshooting/ports",
	}}, nil
}
