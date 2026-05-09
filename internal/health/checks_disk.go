package health

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/docker"
	"github.com/PeterBooker/locorum/internal/platform"
)

// DiskLowDefaults are the default thresholds. The user can override via the
// health.disk_warn_gb / health.disk_blocker_gb settings.
const (
	DefaultDiskWarnBytes    int64 = 5 * 1024 * 1024 * 1024 // 5 GB
	DefaultDiskBlockerBytes int64 = 1 * 1024 * 1024 * 1024 // 1 GB
)

// DiskLowConfig is the per-check tuning. All fields have sensible defaults.
type DiskLowConfig struct {
	WarnBytes    int64 // 0 → DefaultDiskWarnBytes
	BlockerBytes int64 // 0 → DefaultDiskBlockerBytes

	// Path is the host filesystem path to statfs. Typically the user's
	// HomeDir; ~/.locorum lives there too. Empty path → no host check.
	Path string
}

// DiskLowCheck reports the daemon's `system df` total + the host
// filesystem's free bytes at the configured Path. Two thresholds:
// WarnBytes (default 5 GB) → warn; BlockerBytes (default 1 GB) → blocker.
//
// The check is **expensive** (`docker system df` walks every container,
// image, volume, build-cache record). To avoid making Docker slow for
// other callers we:
//
//   - Default to a 15-minute cadence.
//   - Wrap the engine call in a [Breaker] (3 timeouts → open for 1 hour).
//   - Pass a 30s deadline via the per-check Budget.
type DiskLowCheck struct {
	engine  docker.Engine
	cfg     DiskLowConfig
	breaker *Breaker

	mu          sync.Mutex
	lastReport  docker.DiskReport
	lastFree    int64
	lastReadErr error
}

// NewDiskLowCheck builds the check. Pass platform.Get().HomeDir as the
// statfs path in production.
func NewDiskLowCheck(engine docker.Engine, cfg DiskLowConfig) *DiskLowCheck {
	if cfg.WarnBytes <= 0 {
		cfg.WarnBytes = DefaultDiskWarnBytes
	}
	if cfg.BlockerBytes <= 0 {
		cfg.BlockerBytes = DefaultDiskBlockerBytes
	}
	if cfg.BlockerBytes >= cfg.WarnBytes {
		// Blocker must be tighter than warn; correct silently rather
		// than surfacing a config error to the user — the wrong order
		// is almost certainly a typo.
		cfg.BlockerBytes = cfg.WarnBytes / 5
	}
	return &DiskLowCheck{
		engine:  engine,
		cfg:     cfg,
		breaker: &Breaker{MaxFailures: 3, Cooldown: time.Hour},
	}
}

func (*DiskLowCheck) ID() string             { return "disk-low" }
func (*DiskLowCheck) Cadence() time.Duration { return 15 * time.Minute }
func (*DiskLowCheck) Budget() time.Duration  { return 30 * time.Second }

func (c *DiskLowCheck) Run(ctx context.Context) ([]Finding, error) {
	// 1. Cheap host statfs first. Always populated; survives a slow
	//    docker daemon.
	free, freeErr := c.readHostFree()

	// 2. Docker disk usage, gated by the breaker.
	report, dockerErr := c.readDocker(ctx)

	// Build the finding from the cheaper of the two thresholds.
	if free > 0 && free < c.cfg.BlockerBytes {
		return []Finding{c.blockerFinding(free, report)}, nil
	}
	if free > 0 && free < c.cfg.WarnBytes {
		return []Finding{c.warnFinding(free, report)}, nil
	}

	// Both signals report "ok" or unavailable — no finding.
	if errors.Is(dockerErr, ErrCircuitOpen) {
		// Surface a small Info row so the user understands why the
		// disk-bytes line is missing, not a Warn.
		return []Finding{{
			ID:          "disk-check-skipped",
			Severity:    SeverityInfo,
			Title:       "Skipped Docker disk-usage probe",
			Detail:      "Repeated timeouts on `docker system df`; will retry next cycle.",
			Remediation: "Restart Docker if this persists.",
		}}, nil
	}

	if freeErr != nil && dockerErr != nil {
		// Both broken — keep the Run-error path (translated to
		// check-failed by the runner).
		return nil, fmt.Errorf("disk check: host=%w docker=%w", freeErr, dockerErr)
	}
	return nil, nil
}

// readHostFree returns bytes available at cfg.Path (empty → 0, nil).
func (c *DiskLowCheck) readHostFree() (int64, error) {
	if c.cfg.Path == "" {
		return 0, nil
	}
	free, err := platform.HostFreeBytes(c.cfg.Path)
	c.mu.Lock()
	c.lastFree = free
	c.lastReadErr = err
	c.mu.Unlock()
	return free, err
}

// readDocker calls engine.DiskUsage gated by the breaker.
func (c *DiskLowCheck) readDocker(ctx context.Context) (docker.DiskReport, error) {
	if !c.breaker.Allow() {
		return docker.DiskReport{}, ErrCircuitOpen
	}
	rep, err := c.engine.DiskUsage(ctx)
	if err != nil {
		c.breaker.OnFailure()
		return docker.DiskReport{}, err
	}
	c.breaker.OnSuccess()
	c.mu.Lock()
	c.lastReport = rep
	c.mu.Unlock()
	return rep, nil
}

// LastSnapshot returns the last successful host-free + disk-report read.
// Used by the UI status-bar segment so it doesn't have to wait for a
// fresh check.
func (c *DiskLowCheck) LastSnapshot() (free int64, report docker.DiskReport) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastFree, c.lastReport
}

func (c *DiskLowCheck) warnFinding(free int64, report docker.DiskReport) Finding {
	_ = report // currently informational; future: include in detail
	return Finding{
		ID:          c.ID(),
		Severity:    SeverityWarn,
		Title:       "Low disk space",
		Detail:      fmt.Sprintf("Only %s free at %s.", humanBytes(free), c.cfg.Path),
		Remediation: "Free disk space; consider `docker system prune` to reclaim image cache.",
	}
}

func (c *DiskLowCheck) blockerFinding(free int64, report docker.DiskReport) Finding {
	_ = report
	return Finding{
		ID:          c.ID(),
		Severity:    SeverityBlocker,
		Title:       "Critically low disk space",
		Detail:      fmt.Sprintf("Only %s free at %s; site operations will fail.", humanBytes(free), c.cfg.Path),
		Remediation: "Free disk space immediately. Run `docker system prune --all` to reclaim Docker's footprint.",
	}
}

// ErrCircuitOpen is returned by the breaker-gated docker.DiskUsage when
// the circuit is open. Callers translate this into a "skipped" finding
// rather than treating it as a hard failure.
var ErrCircuitOpen = errors.New("health: disk-usage circuit open")

// humanBytes renders bytes as a short human string with at most one
// decimal. Avoids pulling in dustin/go-humanize for one call site.
func humanBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case n >= TB:
		return fmt.Sprintf("%.1f TB", float64(n)/float64(TB))
	case n >= GB:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(GB))
	case n >= MB:
		return fmt.Sprintf("%.0f MB", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
