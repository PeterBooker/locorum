// Package tls handles certificate lifecycle for the global routing layer.
//
// The Provider interface abstracts cert issuance so the router can run with
// (mkcert) or without (HTTP-only fallback) trusted certs. Providers must be
// safe for concurrent use.
package tls

import "context"

type Provider interface {
	// Available reports whether the provider can issue certs on this machine.
	// For mkcert: checks the binary is on PATH and the local CA appears
	// installed. Cheap; callers may invoke this on every UI tick.
	Available(ctx context.Context) (Status, error)

	// Capabilities reports environment-derived gaps in trust-store
	// coverage. Used by the System Health panel to surface "you have
	// Java installed; mkcert from a GUI process didn't touch the
	// cacerts keystore" style notes after Available reports OK.
	// Best-effort: each leg short-circuits to a zero value on probe
	// failure.
	Capabilities(ctx context.Context) Capabilities

	// Issue creates or refreshes a cert covering the given hostnames and
	// returns its on-disk paths. Idempotent: regenerates only if the
	// existing cert's SANs do not match the spec (so repeated calls are
	// safe and cheap).
	Issue(ctx context.Context, spec CertSpec) (CertPath, error)

	// Remove deletes a previously-issued cert (used when a site is removed).
	// Returns nil if the cert does not exist.
	Remove(ctx context.Context, name string) error
}

// CertSpec is the desired-state input to Issue.
type CertSpec struct {
	// Name is the directory the certificate is stored under
	// (~/.locorum/certs/<Name>/{cert,key}.pem). Must be filesystem-safe.
	Name string

	// Hostnames are the SANs the certificate must cover. Order is
	// canonicalised internally before SAN comparisons, so callers don't
	// need to sort.
	Hostnames []string
}

// CertPath points at on-disk PEM files. Both files are owned by the user
// running Locorum; the router container bind-mounts them read-only.
type CertPath struct {
	CertFile string
	KeyFile  string
}

// Status reports the state of the certificate provider.
type Status struct {
	Installed bool   // provider's binary/dependency is available
	CARoot    string // path returned by `mkcert -CAROOT` (or empty)
	CATrusted bool   // best-effort: the local CA appears installed in a trust store
	Message   string // human-readable summary suitable for the UI
}

// IsZero reports whether p has no usable file paths.
func (p CertPath) IsZero() bool { return p.CertFile == "" && p.KeyFile == "" }
