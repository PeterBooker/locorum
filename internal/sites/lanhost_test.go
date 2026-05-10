package sites

import (
	"net"
	"testing"
)

func TestLANHost(t *testing.T) {
	cases := []struct {
		name   string
		slug   string
		ip     string
		domain string
		want   string
	}{
		{"basic 192.168", "myblog", "192.168.1.42", "sslip.io", "myblog.192-168-1-42.sslip.io"},
		{"basic 10.x", "shop", "10.0.0.7", "sslip.io", "shop.10-0-0-7.sslip.io"},
		{"hyphenated slug", "my-store", "192.168.1.1", "sslip.io", "my-store.192-168-1-1.sslip.io"},
		{"alt domain", "myblog", "172.16.5.5", "nip.io", "myblog.172-16-5-5.nip.io"},
		{"empty slug", "", "192.168.1.1", "sslip.io", ""},
		{"empty domain", "myblog", "192.168.1.1", "", ""},
		{"trims slug whitespace", " myblog ", "192.168.1.1", "sslip.io", "myblog.192-168-1-1.sslip.io"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			got := LANHost(tc.slug, ip, tc.domain)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestLANHost_NilIP(t *testing.T) {
	if got := LANHost("myblog", nil, "sslip.io"); got != "" {
		t.Errorf("nil ip should produce empty host, got %q", got)
	}
}

func TestLANHost_IPv6Rejected(t *testing.T) {
	ip := net.ParseIP("fe80::1")
	if got := LANHost("myblog", ip, "sslip.io"); got != "" {
		t.Errorf("IPv6 should produce empty host, got %q", got)
	}
}

func TestLANWildcardHost(t *testing.T) {
	ip := net.ParseIP("192.168.1.42")
	cases := []struct {
		multisite string
		want      string
	}{
		{"subdomain", "*.myblog.192-168-1-42.sslip.io"},
		{"subdirectory", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.multisite, func(t *testing.T) {
			got := LANWildcardHost("myblog", ip, "sslip.io", tc.multisite)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestLANWildcardHost_EmptyHostShortCircuits(t *testing.T) {
	if got := LANWildcardHost("myblog", nil, "sslip.io", "subdomain"); got != "" {
		t.Errorf("got %q, expected empty", got)
	}
}

func TestDashedIPv4(t *testing.T) {
	cases := map[string]string{
		"192.168.1.42": "192-168-1-42",
		"10.0.0.1":     "10-0-0-1",
		"172.16.0.0":   "172-16-0-0",
	}
	for in, want := range cases {
		ip := net.ParseIP(in).To4()
		if got := dashedIPv4(ip); got != want {
			t.Errorf("dashedIPv4(%s) = %q, want %q", in, got, want)
		}
	}
}
