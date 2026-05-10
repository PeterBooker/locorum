package health

import (
	"context"
	"time"

	tlspkg "github.com/PeterBooker/locorum/internal/tls"
)

// MkcertCheck warns when mkcert is missing or its local CA isn't installed
// — sites still serve over HTTPS, but browsers show an untrusted-cert
// warning until the user runs `mkcert -install`.
//
// When mkcert is fully set up, the same check looks one level deeper at
// the host's [tls.Capabilities] and emits an [SeverityInfo] Note for
// each trust store that mkcert from a GUI process couldn't have
// touched. Today: Java keystore (when JAVA_HOME / `java` is detected)
// and Firefox-on-Windows (which lazily creates its NSS DB on first
// launch). The plan owner-note is explicit: Locorum doesn't try to
// manipulate these stores from a GUI launch context — it surfaces the
// gap so the developer takes the one explicit step from a real shell.
//
// This is essentially the same data the persistent notice banner already
// surfaces (see main.go:refreshTLSNotice), but living in the health
// system means the System Health panel can show it alongside everything
// else, with the same dismiss / re-check mechanics.
type MkcertCheck struct {
	prov tlspkg.Provider

	// installer is an optional callback that performs the mkcert
	// install. When non-nil the finding gains a one-click Action.
	installer func(context.Context) error
}

// NewMkcertCheck builds the check. Pass nil for installer to skip the
// inline action button (the persistent notice banner has its own).
func NewMkcertCheck(prov tlspkg.Provider, installer func(context.Context) error) *MkcertCheck {
	return &MkcertCheck{prov: prov, installer: installer}
}

func (*MkcertCheck) ID() string             { return "mkcert-missing" }
func (*MkcertCheck) Cadence() time.Duration { return 5 * time.Minute }
func (*MkcertCheck) Budget() time.Duration  { return 2 * time.Second }

func (c *MkcertCheck) Run(ctx context.Context) ([]Finding, error) {
	if c.prov == nil {
		return nil, nil
	}
	status, err := c.prov.Available(ctx)
	if err != nil {
		return nil, err
	}
	if !status.Installed || !status.CATrusted {
		f := Finding{
			ID:          c.ID(),
			Severity:    SeverityWarn,
			Title:       "Trusted HTTPS not configured",
			Detail:      status.Message,
			Remediation: "Install mkcert and run `mkcert -install` so browsers trust Locorum-issued certificates.",
			HelpURL:     "https://github.com/FiloSottile/mkcert#installation",
		}
		if c.installer != nil {
			f.Action = &Action{
				Label:   "Set up trusted HTTPS",
				Run:     c.installer,
				Timeout: 60 * time.Second,
			}
		}
		return []Finding{f}, nil
	}

	// Trusted HTTPS is otherwise set up. Look for incomplete trust
	// store coverage — these are Info-level (browsers still trust the
	// cert; only specific developer tooling needs the extra step).
	caps := c.prov.Capabilities(ctx)
	var out []Finding
	if caps.JavaPresent {
		out = append(out, Finding{
			ID:       "mkcert-java-trust",
			Severity: SeverityInfo,
			Title:    "Java apps don't trust the Locorum CA",
			Detail: "JAVA_HOME or `java` is on PATH on this host, but mkcert from a GUI process " +
				"can't reach Java's cacerts keystore.",
			Remediation: "Re-run `mkcert -install` from a terminal where JAVA_HOME resolves; Spring Boot, Tomcat, and SBT will then trust Locorum certificates.",
			HelpURL:     "https://github.com/FiloSottile/mkcert#supported-root-stores",
		})
	}
	if caps.FirefoxOnWindows {
		out = append(out, Finding{
			ID:       "mkcert-firefox-windows",
			Severity: SeverityInfo,
			Title:    "Firefox on Windows may not trust the Locorum CA",
			Detail: "Firefox lazily creates its NSS database on first launch. If you ran " +
				"`mkcert -install` before launching Firefox, the keystore wasn't populated yet.",
			Remediation: "Open Firefox once, close it, then re-run `mkcert -install` (or use the action above).",
			HelpURL:     "https://github.com/FiloSottile/mkcert#supported-root-stores",
		})
	}
	return out, nil
}
