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
	if status.Installed && status.CATrusted {
		return nil, nil
	}

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
