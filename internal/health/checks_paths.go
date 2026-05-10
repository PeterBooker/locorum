package health

import (
	"context"
	"time"

	"github.com/PeterBooker/locorum/internal/platform"
)

// SiteRootLister is the read-only interface the path-shape checks need.
// SiteManager satisfies it via a tiny method (Roots(ctx) → []string).
//
// Defining this interface here (rather than importing
// *sites.SiteManager) keeps the health package decoupled from the
// site-lifecycle subsystem; production wiring is one line in main.go.
type SiteRootLister interface {
	Roots(ctx context.Context) []string
}

// WSLMntCCheck warns when any registered site lives under /mnt/c (or any
// /mnt/<letter>/) — DrvFS is roughly 10× slower than native ext4 for the
// many-small-files WordPress workload.
type WSLMntCCheck struct {
	platformInfo *platform.Info
	sites        SiteRootLister
}

// NewWSLMntCCheck builds the check.
func NewWSLMntCCheck(info *platform.Info, sites SiteRootLister) *WSLMntCCheck {
	return &WSLMntCCheck{platformInfo: info, sites: sites}
}

func (*WSLMntCCheck) ID() string             { return "wsl-mnt-c" }
func (*WSLMntCCheck) Cadence() time.Duration { return 30 * time.Minute }
func (*WSLMntCCheck) Budget() time.Duration  { return time.Second }

func (c *WSLMntCCheck) Run(ctx context.Context) ([]Finding, error) {
	if c.platformInfo == nil || !c.platformInfo.WSL.Active {
		return nil, nil
	}
	if c.sites == nil {
		return nil, nil
	}
	var out []Finding
	for _, root := range c.sites.Roots(ctx) {
		if !platform.IsMntC(root) {
			continue
		}
		out = append(out, Finding{
			ID:          c.ID(),
			DedupKey:    root,
			Severity:    SeverityWarn,
			Title:       "Site stored on slow Windows-mounted drive",
			Detail:      "The site at " + root + " lives under /mnt/c (DrvFS), which is much slower than native WSL ext4.",
			Remediation: "Move the site to a path under /home/<user>/ for native filesystem speed.",
			HelpURL:     "https://docs.locorum.dev/wsl-performance",
		})
	}
	return out, nil
}

// WindowsLongPathCheck flags registered sites whose root path would
// breach Windows' MAX_PATH limit once WordPress's deepest plugin path is
// appended. Severity is conditional: when the OS has LongPathsEnabled
// set, the kernel handles the overflow and we emit a soft Warn (some
// legacy plugins still don't honour the manifest opt-in). When the flag
// is unset, the site cannot reliably function and we emit a Blocker —
// the lifecycle layer's AddSite/StartSite refuse to start the same site
// for the same reason; the panel surfaces the rationale.
type WindowsLongPathCheck struct {
	platformInfo *platform.Info
	sites        SiteRootLister
}

// NewWindowsLongPathCheck builds the check.
func NewWindowsLongPathCheck(info *platform.Info, sites SiteRootLister) *WindowsLongPathCheck {
	return &WindowsLongPathCheck{platformInfo: info, sites: sites}
}

func (*WindowsLongPathCheck) ID() string             { return "windows-longpath" }
func (*WindowsLongPathCheck) Cadence() time.Duration { return 30 * time.Minute }
func (*WindowsLongPathCheck) Budget() time.Duration  { return time.Second }

func (c *WindowsLongPathCheck) Run(ctx context.Context) ([]Finding, error) {
	if c.platformInfo == nil || c.platformInfo.OS != "windows" {
		return nil, nil
	}
	if c.sites == nil {
		return nil, nil
	}
	var out []Finding
	for _, root := range c.sites.Roots(ctx) {
		if !platform.IsLongPath(root) {
			continue
		}
		f := Finding{
			ID:       c.ID(),
			DedupKey: root,
			HelpURL:  "https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation",
		}
		if c.platformInfo.LongPathsEnabled {
			f.Severity = SeverityWarn
			f.Title = "Path may exceed Windows 260-character limit"
			f.Detail = "The site at " + root + " is long enough that some WordPress plugin assets " +
				"may exceed Windows MAX_PATH. LongPathsEnabled is set, but not every plugin honours it."
			f.Remediation = "Move the site under a shorter path for the strongest guarantee."
		} else {
			f.Severity = SeverityBlocker
			f.Title = "Path exceeds Windows 260-character limit"
			f.Detail = "The site at " + root + " breaches Windows MAX_PATH and LongPathsEnabled is not set; " +
				"Locorum will refuse to start it until the path is shortened or the registry flag is enabled."
			f.Remediation = "Move the site under a shorter path (≤ 200 chars), or enable LongPathsEnabled in the registry (admin) and restart Locorum."
		}
		out = append(out, f)
	}
	return out, nil
}
