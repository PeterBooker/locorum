// Package fake is an in-memory tls.Provider for unit tests. Never
// invokes mkcert; Issue writes "fake-cert"/"fake-key" file contents.
package fake

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/PeterBooker/locorum/internal/tls"
)

type Provider struct {
	mu sync.Mutex

	// Root is the temp dir for cert files; populated lazily on first Issue.
	Root string

	// AvailableStatus is what Available returns. Zero = Installed+CATrusted.
	AvailableStatus tls.Status

	// AvailableErr forces Available to error.
	AvailableErr error

	// CapabilitiesValue is what Capabilities returns. Zero value means
	// "no extra trust stores to surface" — tests that need the Java or
	// Firefox-on-Windows code paths set it explicitly.
	CapabilitiesValue tls.Capabilities

	// IssueErr forces the next Issue to error, then clears.
	IssueErr error

	// RootCAErr forces RootCAPath to error. Persistent — does not
	// auto-clear, so a single test can assert repeated failure.
	RootCAErr error

	// Issued tracks live certs by name → hostnames.
	Issued map[string][]string

	// Removed records every name passed to Remove.
	Removed []string
}

func New() *Provider {
	return &Provider{
		AvailableStatus: tls.Status{
			Installed: true,
			CATrusted: true,
			CARoot:    "/fake/ca/root",
			Message:   "fake mkcert ready",
		},
		Issued: map[string][]string{},
	}
}

func (p *Provider) Available(_ context.Context) (tls.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.AvailableErr != nil {
		return tls.Status{}, p.AvailableErr
	}
	return p.AvailableStatus, nil
}

func (p *Provider) Capabilities(_ context.Context) tls.Capabilities {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.CapabilitiesValue
}

func (p *Provider) Issue(_ context.Context, spec tls.CertSpec) (tls.CertPath, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.IssueErr != nil {
		err := p.IssueErr
		p.IssueErr = nil
		return tls.CertPath{}, err
	}
	if spec.Name == "" {
		return tls.CertPath{}, errors.New("fake.Issue: empty Name")
	}
	if p.Root == "" {
		dir, err := os.MkdirTemp("", "locorum-fake-tls-*")
		if err != nil {
			return tls.CertPath{}, err
		}
		p.Root = dir
	}
	dir := filepath.Join(p.Root, spec.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.CertPath{}, err
	}
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, []byte("fake-cert"), 0o600); err != nil {
		return tls.CertPath{}, err
	}
	if err := os.WriteFile(keyFile, []byte("fake-key"), 0o600); err != nil {
		return tls.CertPath{}, err
	}
	hosts := append([]string(nil), spec.Hostnames...)
	sort.Strings(hosts)
	p.Issued[spec.Name] = hosts
	return tls.CertPath{CertFile: certFile, KeyFile: keyFile}, nil
}

func (p *Provider) Remove(_ context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.Issued, name)
	p.Removed = append(p.Removed, name)
	if p.Root != "" {
		_ = os.RemoveAll(filepath.Join(p.Root, name))
	}
	return nil
}

// RootCAPath returns a fixture path under the temp Root, creating an
// empty rootCA.pem so callers can exercise file-serving code paths
// without a real mkcert install. RootCAErr forces an error path.
func (p *Provider) RootCAPath(_ context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.RootCAErr != nil {
		return "", p.RootCAErr
	}
	if p.Root == "" {
		dir, err := os.MkdirTemp("", "locorum-fake-tls-*")
		if err != nil {
			return "", err
		}
		p.Root = dir
	}
	caDir := filepath.Join(p.Root, "ca")
	if err := os.MkdirAll(caDir, 0o700); err != nil {
		return "", err
	}
	rootCA := filepath.Join(caDir, "rootCA.pem")
	if _, err := os.Stat(rootCA); os.IsNotExist(err) {
		if err := os.WriteFile(rootCA, []byte("fake-root-ca"), 0o644); err != nil {
			return "", err
		}
	}
	return rootCA, nil
}

// Cleanup removes the on-disk temp tree; pair with t.Cleanup.
func (p *Provider) Cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.Root != "" {
		_ = os.RemoveAll(p.Root)
		p.Root = ""
	}
}
