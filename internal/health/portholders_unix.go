//go:build !windows

package health

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// portHolderLookupTimeout caps the lsof shell-out. lsof typically returns
// in well under 100 ms even when many connections exist; a longer hang is
// almost always lsof waiting on a pipe / FIFO and we'd rather surface an
// empty result than freeze the action button.
const portHolderLookupTimeout = 3 * time.Second

// lookupPortHolders shells out to lsof to enumerate processes holding
// `port`. Returns the human-readable lsof header + matching rows on
// success; on failure returns a remediation-shaped string the UI can
// surface to the user (e.g. "lsof not found on PATH"). Both return paths
// are nil-error: the caller treats this as informational data, not a
// fault to log.
//
// On WSL hosts the result reflects only Linux-side processes; if a
// Windows-side service is holding the port (IIS, Skype, etc.) it will
// not appear here. We add a one-line note in that case so the user
// knows where else to look.
func lookupPortHolders(ctx context.Context, port int) (string, error) {
	bin, err := exec.LookPath("lsof")
	if err != nil {
		return "lsof was not found on PATH. Install lsof, or use `ss -ltnp 'sport = :" +
			strconv.Itoa(port) + "'` from a privileged shell to identify the holder.", nil
	}

	cctx, cancel := context.WithTimeout(ctx, portHolderLookupTimeout)
	defer cancel()

	// -nP suppresses DNS + service-name resolution (faster, deterministic).
	// -iTCP:<port> matches IPv4+IPv6 TCP sockets bound to the port.
	// -sTCP:LISTEN narrows to listening sockets — established outbound
	// connections to a remote port:80 are not relevant here.
	cmd := exec.CommandContext(cctx, bin, "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN")
	out, runErr := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))

	if cctx.Err() != nil {
		return "lsof timed out after " + portHolderLookupTimeout.String() +
			". Run `lsof -nP -iTCP:" + strconv.Itoa(port) + " -sTCP:LISTEN` manually.", nil
	}
	// lsof exits 1 with no output when nothing matches; that's a successful
	// "no listener" answer in our model. Anything else with output is the
	// usual "rows of processes" case.
	if text == "" {
		if runErr != nil {
			return fmt.Sprintf("No listener visible to lsof on port %d. Permission may be required (try `sudo lsof -nP -iTCP:%d -sTCP:LISTEN`).", port, port), nil
		}
		return fmt.Sprintf("No process is listening on port %d.", port), nil
	}
	return text, nil
}
