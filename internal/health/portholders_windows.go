//go:build windows

package health

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// portHolderLookupTimeout caps the PowerShell shell-out. The Get-NetTCPConnection
// + Get-Process pipeline is fast on a typical Windows host but starting
// powershell.exe itself costs ~200 ms cold; a 5-second cap is generous.
const portHolderLookupTimeout = 5 * time.Second

// lookupPortHolders shells out to PowerShell to enumerate processes
// holding `port` on Windows. The pipeline returns process name + PID +
// command-line for each listener, which is enough to point the user at
// the offending application without requiring admin privileges.
//
// Both return paths are nil-error: the caller treats this as
// informational data, not a fault to log.
func lookupPortHolders(ctx context.Context, port int) (string, error) {
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		return "PowerShell is not on PATH. Open an admin terminal and run " +
			"`Get-NetTCPConnection -LocalPort " + strconv.Itoa(port) + "` to identify the holder.", nil
	}

	cctx, cancel := context.WithTimeout(ctx, portHolderLookupTimeout)
	defer cancel()

	// -NoProfile shaves 100-200 ms off cold-start; -NonInteractive
	// prevents an accidental Read-Host prompt from blocking forever.
	// The script is hand-built (not %d formatted) to avoid any prompt
	// injection from the port number.
	script := "$conns = Get-NetTCPConnection -LocalPort " + strconv.Itoa(port) +
		" -State Listen -ErrorAction SilentlyContinue; " +
		"if ($null -eq $conns) { 'No process is listening on port " + strconv.Itoa(port) + ".'; return }; " +
		"$conns | ForEach-Object { " +
		"$p = Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue; " +
		"$name = if ($p) { $p.ProcessName } else { '<unknown>' }; " +
		"'{0,-22} {1,-7} {2,-6} {3}' -f $_.LocalAddress, $_.LocalPort, $_.OwningProcess, $name " +
		"}"

	cmd := exec.CommandContext(cctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	out, _ := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))

	if cctx.Err() != nil {
		return "Get-NetTCPConnection timed out after " + portHolderLookupTimeout.String() +
			". Run the command manually from PowerShell.", nil
	}
	if text == "" {
		return "No process is listening on port " + strconv.Itoa(port) + ".", nil
	}
	header := "ADDRESS                PORT    PID    PROCESS\n"
	return header + text, nil
}
