package tls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestValidCertName(t *testing.T) {
	valid := []string{"site-foo", "svc-mail", "abc123", "FOO_bar", "a"}
	invalid := []string{"", ".", "..", "../escape", "with spaces", "with/slash", "with$"}

	for _, n := range valid {
		if !validCertName(n) {
			t.Errorf("expected %q valid", n)
		}
	}
	for _, n := range invalid {
		if validCertName(n) {
			t.Errorf("expected %q invalid", n)
		}
	}
}

func TestSanMatches(t *testing.T) {
	tests := []struct {
		name string
		have []string
		want string
		ok   bool
	}{
		{"exact match", []string{"foo.example"}, "foo.example", true},
		{"no match", []string{"foo.example"}, "bar.example", false},
		{"wildcard one label", []string{"*.example"}, "foo.example", true},
		{"wildcard rejects two labels", []string{"*.example"}, "foo.bar.example", false},
		{"wildcard does not match bare", []string{"*.example"}, "example", false},
		{"mixed list with wildcard", []string{"foo.example", "*.foo.example"}, "bar.foo.example", true},
		{"case insensitive", []string{"FOO.example"}, "foo.example", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanMatches(tt.have, tt.want); got != tt.ok {
				t.Errorf("sanMatches(%v, %q) = %v, want %v", tt.have, tt.want, got, tt.ok)
			}
		})
	}
}

func TestCertCovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cert.pem")

	writeTestCert(t, path, []string{"foo.localhost", "bar.localhost"})

	t.Run("covers exact subset", func(t *testing.T) {
		ok, err := certCovers(path, []string{"foo.localhost"})
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Error("expected cover")
		}
	})

	t.Run("does not cover missing host", func(t *testing.T) {
		ok, err := certCovers(path, []string{"baz.localhost"})
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Error("expected miss")
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		_, err := certCovers(filepath.Join(dir, "nope"), []string{"x"})
		if err == nil {
			t.Error("expected error for missing file")
		}
	})
}

func TestMkcertAvailable(t *testing.T) {
	m := NewMkcert(t.TempDir())
	status, err := m.Available(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if _, lookErr := exec.LookPath("mkcert"); lookErr != nil {
		if status.Installed {
			t.Error("expected Installed=false when mkcert missing")
		}
		if status.Message == "" {
			t.Error("expected guidance message when mkcert missing")
		}
		return
	}

	if !status.Installed {
		t.Error("expected Installed=true when mkcert is on PATH")
	}
	if status.CARoot == "" {
		t.Error("expected non-empty CARoot")
	}
}

func TestMkcertIssue_Idempotent(t *testing.T) {
	if _, err := exec.LookPath("mkcert"); err != nil {
		t.Skip("mkcert not on PATH")
	}
	m := NewMkcert(t.TempDir())
	status, _ := m.Available(context.Background())
	if !status.CATrusted {
		t.Skip("mkcert -install has not been run on this host")
	}

	ctx := context.Background()
	spec := CertSpec{Name: "locorumtest", Hostnames: []string{"locorum-test.localhost"}}

	first, err := m.Issue(ctx, spec)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := os.Stat(first.CertFile); err != nil {
		t.Fatalf("cert missing: %v", err)
	}

	// Idempotent.
	second, err := m.Issue(ctx, spec)
	if err != nil {
		t.Fatalf("re-Issue: %v", err)
	}
	if first.CertFile != second.CertFile {
		t.Errorf("idempotent issue moved cert: %v vs %v", first, second)
	}

	// Adding hostname triggers re-issue.
	spec.Hostnames = append(spec.Hostnames, "another.localhost")
	if _, err := m.Issue(ctx, spec); err != nil {
		t.Fatal(err)
	}
	covered, err := certCovers(first.CertFile, spec.Hostnames)
	if err != nil {
		t.Fatal(err)
	}
	if !covered {
		t.Error("re-issued cert should cover both hostnames")
	}

	// Cleanup.
	if err := m.Remove(ctx, spec.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.CertFile); !os.IsNotExist(err) {
		t.Errorf("cert should be removed: %v", err)
	}
}

// writeTestCert generates a self-signed cert covering the given DNS names
// and writes the cert PEM to path. Used for certCovers tests; never reaches
// any trust store.
func writeTestCert(t *testing.T, path string, dnsNames []string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     dnsNames,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		t.Fatal(err)
	}
}
