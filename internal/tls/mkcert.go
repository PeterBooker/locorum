package tls

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PeterBooker/locorum/internal/utils"
)

// Mkcert issues per-site and per-service certs by shelling out to the mkcert
// CLI (https://github.com/FiloSottile/mkcert). Detection is cached; the UI
// can poll Available() every frame without performance concerns.
type Mkcert struct {
	certDir string

	mu           sync.Mutex
	binary       string
	cachedAt     time.Time
	cachedStatus Status
}

const mkcertCacheTTL = 30 * time.Second

// NewMkcert constructs a provider that stores certs under certDir
// (typically ~/.locorum/certs).
func NewMkcert(certDir string) *Mkcert {
	return &Mkcert{certDir: certDir}
}

func (m *Mkcert) Available(ctx context.Context) (Status, error) {
	m.mu.Lock()
	if !m.cachedAt.IsZero() && time.Since(m.cachedAt) < mkcertCacheTTL {
		s := m.cachedStatus
		m.mu.Unlock()
		return s, nil
	}
	m.mu.Unlock()

	s := m.detect(ctx)

	m.mu.Lock()
	m.cachedStatus = s
	m.cachedAt = time.Now()
	if s.Installed {
		if bin, err := exec.LookPath("mkcert"); err == nil {
			m.binary = bin
		}
	} else {
		m.binary = ""
	}
	m.mu.Unlock()

	return s, nil
}

func (m *Mkcert) detect(ctx context.Context) Status {
	if _, err := exec.LookPath("mkcert"); err != nil {
		return Status{
			Message: "mkcert not found on PATH. Install: https://github.com/FiloSottile/mkcert",
		}
	}

	cmd := exec.CommandContext(ctx, "mkcert", "-CAROOT")
	utils.HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return Status{
			Installed: true,
			Message:   "mkcert -CAROOT failed: " + err.Error(),
		}
	}
	caRoot := strings.TrimSpace(string(out))

	rootCA := filepath.Join(caRoot, "rootCA.pem")
	if _, err := os.Stat(rootCA); err != nil {
		return Status{
			Installed: true,
			CARoot:    caRoot,
			Message:   "Run `mkcert -install` to enable trusted HTTPS for your sites.",
		}
	}

	return Status{
		Installed: true,
		CARoot:    caRoot,
		CATrusted: true,
		Message:   "mkcert ready",
	}
}

// Issue generates (or reuses) a cert covering spec.Hostnames. Idempotent: if
// the existing cert at ~/.locorum/certs/<spec.Name>/cert.pem already covers
// every requested SAN, it is returned unchanged. Otherwise mkcert is invoked,
// the new cert and key are written to a sibling temp dir, then atomically
// moved into place so a watching router never reads a half-written file.
func (m *Mkcert) Issue(ctx context.Context, spec CertSpec) (CertPath, error) {
	if len(spec.Hostnames) == 0 {
		return CertPath{}, fmt.Errorf("at least one hostname required")
	}
	if !validCertName(spec.Name) {
		return CertPath{}, fmt.Errorf("invalid cert name %q", spec.Name)
	}

	status, err := m.Available(ctx)
	if err != nil {
		return CertPath{}, err
	}
	if !status.Installed {
		return CertPath{}, fmt.Errorf("mkcert is not installed")
	}
	if !status.CATrusted {
		// Refusing to issue is deliberate: a bare `mkcert <hosts>` call
		// would silently create the CA without installing it, leaving the
		// user with an issued-but-untrusted cert and no clear next step.
		return CertPath{}, fmt.Errorf("mkcert local CA not installed; run `mkcert -install`")
	}

	m.mu.Lock()
	bin := m.binary
	m.mu.Unlock()
	if bin == "" {
		return CertPath{}, fmt.Errorf("mkcert binary unresolved")
	}

	targetDir := filepath.Join(m.certDir, spec.Name)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return CertPath{}, fmt.Errorf("create cert dir: %w", err)
	}

	certFile := filepath.Join(targetDir, "cert.pem")
	keyFile := filepath.Join(targetDir, "key.pem")

	if covered, _ := certCovers(certFile, spec.Hostnames); covered {
		return CertPath{CertFile: certFile, KeyFile: keyFile}, nil
	}

	if err := os.MkdirAll(m.certDir, 0o755); err != nil {
		return CertPath{}, fmt.Errorf("create certs root: %w", err)
	}
	tmpDir, err := os.MkdirTemp(m.certDir, ".issue-"+spec.Name+"-")
	if err != nil {
		return CertPath{}, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpCert := filepath.Join(tmpDir, "cert.pem")
	tmpKey := filepath.Join(tmpDir, "key.pem")

	args := []string{"-cert-file", tmpCert, "-key-file", tmpKey}
	args = append(args, spec.Hostnames...)
	issueCmd := exec.CommandContext(ctx, bin, args...)
	utils.HideConsole(issueCmd)
	if out, err := issueCmd.CombinedOutput(); err != nil {
		return CertPath{}, fmt.Errorf("mkcert: %w; output: %s", err, strings.TrimSpace(string(out)))
	}

	if err := os.Rename(tmpCert, certFile); err != nil {
		return CertPath{}, fmt.Errorf("install cert: %w", err)
	}
	if err := os.Rename(tmpKey, keyFile); err != nil {
		return CertPath{}, fmt.Errorf("install key: %w", err)
	}

	slog.Info("issued cert", "name", spec.Name, "hosts", spec.Hostnames)
	return CertPath{CertFile: certFile, KeyFile: keyFile}, nil
}

func (m *Mkcert) Remove(_ context.Context, name string) error {
	if !validCertName(name) {
		return fmt.Errorf("invalid cert name %q", name)
	}
	if err := os.RemoveAll(filepath.Join(m.certDir, name)); err != nil {
		return fmt.Errorf("remove cert dir: %w", err)
	}
	return nil
}

// validCertName rejects path-traversal-prone names. Cert names are derived
// from site slugs and a small fixed set of service identifiers, so the
// charset is intentionally narrow.
func validCertName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// certCovers reports whether the PEM cert at path covers every requested
// hostname (case-insensitive, with single-label wildcard SAN support).
func certCovers(path string, requested []string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false, fmt.Errorf("not a PEM file: %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}

	have := make([]string, len(cert.DNSNames))
	for i, n := range cert.DNSNames {
		have[i] = strings.ToLower(n)
	}
	for _, r := range requested {
		if !sanMatches(have, strings.ToLower(r)) {
			return false, nil
		}
	}
	return true, nil
}

// sanMatches reports whether want is satisfied by any of the SANs in have.
// Comparison is case-insensitive (RFC 6125). A wildcard SAN like
// "*.example.com" matches exactly one DNS label of want.
func sanMatches(have []string, want string) bool {
	want = strings.ToLower(want)
	for _, h := range have {
		h = strings.ToLower(h)
		if h == want {
			return true
		}
		if strings.HasPrefix(h, "*.") {
			suffix := h[1:] // ".example.com"
			if strings.HasSuffix(want, suffix) {
				rest := strings.TrimSuffix(want, suffix)
				if rest != "" && !strings.Contains(rest, ".") {
					return true
				}
			}
		}
	}
	return false
}
