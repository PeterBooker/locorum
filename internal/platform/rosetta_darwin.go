//go:build darwin

package platform

import (
	"os/exec"
	"strings"
)

// isUnderRosetta reports whether this binary is being executed under Apple's
// amd64-on-arm64 translator. Returns false on native arm64 binaries and on
// arm64 hosts running native arm64 builds.
//
// We shell out to `/usr/sbin/sysctl` rather than linking the macOS sys/sysctl
// header so the package stays cgo-free. The cost is one fork+exec at
// startup — negligible, and only on darwin.
//
// `sysctl.proc_translated` is documented in Apple's sysctl(3): 1 means the
// running process is a translated x86_64 process under Rosetta; 0 means
// native; the sysctl is missing entirely on Intel Macs (the binary cannot
// be Rosetta-translated there).
func isUnderRosetta() bool {
	out, err := exec.Command("/usr/sbin/sysctl", "-n", "sysctl.proc_translated").Output()
	if err != nil {
		// Intel Macs: the sysctl is missing → sysctl exits non-zero.
		// Treat as "not Rosetta", which is correct.
		return false
	}
	return strings.TrimSpace(string(out)) == "1"
}
