package health

import (
	"context"
	"time"

	"github.com/PeterBooker/locorum/internal/platform"
)

// RosettaCheck reports a Blocker if a macOS amd64 binary is being executed
// under Rosetta on an arm64 host. Mostly redundant with the preflight
// hard-fail (preflight_darwin.go) — the preflight runs **before** Gio
// loads, so this UI-side check covers cases the preflight skipped (e.g.
// `osascript` failed; we kept running with stderr only) and gives the
// settings panel a stable "yes you should fix this" row.
type RosettaCheck struct {
	platformInfo *platform.Info
}

// NewRosettaCheck constructs a check using the given platform info. Pass
// platform.Get() in production; tests inject a synthetic Info.
func NewRosettaCheck(info *platform.Info) *RosettaCheck {
	return &RosettaCheck{platformInfo: info}
}

func (*RosettaCheck) ID() string             { return "rosetta" }
func (*RosettaCheck) Cadence() time.Duration { return 0 } // runner default
func (*RosettaCheck) Budget() time.Duration  { return time.Second }

func (c *RosettaCheck) Run(_ context.Context) ([]Finding, error) {
	if c.platformInfo == nil {
		return nil, nil
	}
	if !c.platformInfo.UnderRosetta {
		return nil, nil
	}
	return []Finding{{
		ID:       c.ID(),
		Severity: SeverityBlocker,
		Title:    "Running under Rosetta translation",
		Detail: "This is the amd64 build of Locorum running on an Apple Silicon (arm64) Mac via Rosetta. " +
			"Docker volume performance and container compatibility will be unreliable.",
		Remediation: "Quit Locorum and reinstall the arm64 build for native performance.",
		HelpURL:     "https://docs.locorum.dev/install/macos",
	}}, nil
}
