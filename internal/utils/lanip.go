package utils

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// ErrNoLANIP is returned by DetectLANIPv4 when no usable IPv4 address
// can be picked. Callers surface this with a clear UI message rather
// than guessing an interface.
var ErrNoLANIP = errors.New("no usable LAN IPv4 address found")

// dockerBridgePrefixes is the conventional naming convention Docker
// uses for its bridge interfaces. We exclude any address bound to one
// of these so a LAN IP is never picked from a container-internal
// network. The list is intentionally narrow — adding random patterns
// here risks excluding a real WAN interface on someone's box.
var dockerBridgePrefixes = []string{"docker", "br-", "lxcbr", "virbr", "vmnet", "veth"}

// LANDeps is the dependency set DetectLANIPv4 uses, kept as an
// interface so tests can stub Interfaces() and ResolveOutbound() with
// deterministic data. Production code calls DetectLANIPv4 directly,
// which uses the real network stack.
type LANDeps struct {
	// Interfaces returns the list of host interfaces. Defaults to
	// net.Interfaces.
	Interfaces func() ([]net.Interface, error)

	// Addrs returns the addresses for an interface. Defaults to
	// (*net.Interface).Addrs.
	Addrs func(iface *net.Interface) ([]net.Addr, error)

	// OutboundIP returns the source address used to reach a public
	// destination. Defaults to dialing UDP to 1.1.1.1:80 (no packets
	// sent) and reading LocalAddr. Used as a tie-breaker when
	// multiple usable interfaces are present.
	OutboundIP func() (net.IP, error)
}

// DetectLANIPv4 picks the host's primary outbound IPv4 address. The
// rule, in order:
//
//  1. Try to find the source address used to reach a public destination
//     by opening a UDP socket to 1.1.1.1:80 (no packets are actually
//     sent — Dial just consults the routing table). If that address is
//     a usable RFC1918 / private LAN address (and not a Docker bridge,
//     loopback, or link-local), return it.
//  2. Otherwise, walk every up, non-loopback, non-virtual interface
//     and return the first private IPv4 found.
//  3. Otherwise, return ErrNoLANIP. Callers should surface a clear
//     "set lan.ip_override" hint rather than guessing.
//
// The function is intentionally pure-detection: it does not consult
// config or do any I/O beyond opening a UDP socket. Callers are
// responsible for honouring KeyLanIPOverride before falling back here.
func DetectLANIPv4() (net.IP, error) {
	return detectLANIPv4(LANDeps{})
}

// detectLANIPv4 is the testable form of DetectLANIPv4 — defaults are
// applied for any nil dep so production callers can pass a zero
// LANDeps and get the real network stack.
func detectLANIPv4(deps LANDeps) (net.IP, error) {
	if deps.Interfaces == nil {
		deps.Interfaces = net.Interfaces
	}
	if deps.Addrs == nil {
		deps.Addrs = func(iface *net.Interface) ([]net.Addr, error) { return iface.Addrs() }
	}
	if deps.OutboundIP == nil {
		deps.OutboundIP = outboundIPv4
	}

	bridgeIPs, err := dockerBridgeIPs(deps)
	if err != nil {
		// Probing interfaces failed entirely — proceed without a
		// bridge filter rather than refusing to detect at all. This
		// keeps DetectLANIPv4 usable on minimal CI boxes that report
		// no interfaces; the OutboundIP path still validates the
		// returned address is not link-local / loopback.
		bridgeIPs = nil
	}

	if ip, err := deps.OutboundIP(); err == nil && ip != nil {
		if v4 := ip.To4(); v4 != nil && isUsableLAN(v4, bridgeIPs) {
			return v4, nil
		}
	}

	// Fallback walk.
	ifaces, err := deps.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isDockerBridge(iface.Name) {
			continue
		}
		addrs, err := deps.Addrs(&iface)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			v4 := ipnet.IP.To4()
			if v4 == nil {
				continue
			}
			if isUsableLAN(v4, bridgeIPs) {
				return v4, nil
			}
		}
	}
	return nil, ErrNoLANIP
}

// outboundIPv4 opens a connectionless UDP socket to a public address
// and reads the resulting LocalAddr. No traffic is sent — Dial only
// consults the routing table. The destination port is arbitrary; 80 is
// chosen because it is universally reachable from any rule set that
// permits outbound HTTP.
func outboundIPv4() (net.IP, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.Dial("udp4", "1.1.1.1:80")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr == nil || addr.IP == nil {
		return nil, errors.New("local addr was not UDP")
	}
	return addr.IP.To4(), nil
}

// dockerBridgeIPs collects every IPv4 currently bound to a recognised
// container-bridge interface. Used so the OutboundIP path can reject
// addresses that "look right" but actually belong to docker0 or a
// per-stack bridge.
func dockerBridgeIPs(deps LANDeps) (map[string]struct{}, error) {
	ifaces, err := deps.Interfaces()
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	for _, iface := range ifaces {
		if !isDockerBridge(iface.Name) {
			continue
		}
		addrs, err := deps.Addrs(&iface)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v4 := ipnet.IP.To4(); v4 != nil {
				out[v4.String()] = struct{}{}
			}
		}
	}
	return out, nil
}

func isDockerBridge(name string) bool {
	for _, p := range dockerBridgePrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// isUsableLAN rejects addresses that would never be reachable from
// another device on the same Wi-Fi: loopback, link-local, multicast,
// unspecified, and any address bound to a container bridge.
//
// Addresses outside RFC1918 are still accepted on the assumption that
// the user is on a network using non-private space (carrier-grade NAT
// pop-ups, lab networks, public Wi-Fi assigning a routable address).
// The cost of being wrong is a non-working LAN URL, not a security
// boundary — every site is still gated by the per-site opt-in.
func isUsableLAN(ip net.IP, bridgeIPs map[string]struct{}) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	if ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if _, isBridge := bridgeIPs[ip.String()]; isBridge {
		return false
	}
	return true
}
