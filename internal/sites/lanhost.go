package sites

import (
	"net"
	"strings"

	"github.com/PeterBooker/locorum/internal/config"
)

// LANHost returns the canonical "<slug>.<ip-dashed>.<domain>" hostname
// used to reach a LAN-enabled site from devices on the same Wi-Fi.
//
// The dashed form (192-168-1-42) is preferred over dotted because the
// resulting hostname unambiguously parses as
// `<slug>.<ip-dashed>.<sslip-suffix>` even when the slug contains a
// digit-shaped label. sslip.io supports both forms; we prefer the
// dashed form for readability and to sidestep any future sub-label
// parsing change at the resolver.
//
// Returns "" when ip is nil or not IPv4, or when slug or domain is
// empty — callers must handle the empty hostname as "no LAN access
// configured" rather than appending a malformed entry to ExtraHosts.
func LANHost(slug string, ip net.IP, domain string) string {
	slug = strings.TrimSpace(slug)
	domain = strings.TrimSpace(domain)
	if slug == "" || domain == "" || ip == nil {
		return ""
	}
	v4 := ip.To4()
	if v4 == nil {
		return ""
	}
	return slug + "." + dashedIPv4(v4) + "." + domain
}

// LANWildcardHost returns "*.<lanhost>" for sites running multisite in
// subdomain mode, or "" otherwise. The wildcard SAN matches exactly one
// DNS label per RFC 6125, which is precisely what WordPress multisite
// subdomains need ("subsiteA.<lanhost>", "subsiteB.<lanhost>") — the
// LAN hostname itself is never a deeper sub-domain of another LAN
// hostname, so single-label coverage is sufficient.
func LANWildcardHost(slug string, ip net.IP, domain string, multisite string) string {
	if multisite != "subdomain" {
		return ""
	}
	host := LANHost(slug, ip, domain)
	if host == "" {
		return ""
	}
	return "*." + host
}

// dashedIPv4 stringifies an IPv4 with dashes instead of dots. Caller is
// responsible for passing a non-nil v4-form net.IP (To4() applied).
func dashedIPv4(ip net.IP) string {
	return strings.ReplaceAll(ip.String(), ".", "-")
}

// effectiveLanDomain returns the domain to use for LAN hostnames,
// honouring the runtime config override and falling back to the
// documented sslip.io default.
func effectiveLanDomain(cfg *config.Config) string {
	if cfg == nil {
		return config.DefaultLanDomain
	}
	return cfg.LanDomain()
}
