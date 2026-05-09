package utils

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNoTerminal is returned by OpenInTerminal when no platform terminal
// launcher could be detected. Callers branch on errors.Is to fall back
// to copying the command to the clipboard with a "no terminal found —
// command copied" toast.
var ErrNoTerminal = errors.New("no terminal emulator found")

// OpenInTerminal launches the platform's preferred terminal and runs
// the given argv inside it.
//
// There is no portable way to "pre-type the command without executing"
// — Terminal.app, wt.exe, kitty, et al. all take a command and run it.
// Callers that want the user to inspect or edit the command first
// should use the clipboard fallback path (catch ErrNoTerminal *or* an
// explicit toast on success that mentions the command copied).
//
// Per platform:
//
//   - Linux: $TERMINAL → x-terminal-emulator → kitty → alacritty → foot
//     → wezterm → ghostty → gnome-terminal → konsole → xfce4-terminal →
//     xterm.
//   - macOS: Terminal.app via osascript. (iTerm precedence is a
//     follow-up — see UX.md §13.)
//   - Windows: wt.exe if on PATH, else `cmd /c start cmd /k <cmd>`.
//   - WSL (Linux running inside Windows): wt.exe through wsl.exe -- so
//     the spawned terminal lives on the Windows side.
//
// The returned process is detached (Start, not Run) so the caller never
// blocks waiting for the user to close the terminal.
func OpenInTerminal(argv []string) error {
	if len(argv) == 0 {
		return errors.New("OpenInTerminal: empty argv")
	}
	switch runtime.GOOS {
	case "darwin":
		return openInTerminalMac(argv)
	case "windows":
		return openInTerminalWindows(argv)
	default:
		if isWSL() {
			return openInTerminalWSL(argv)
		}
		return openInTerminalLinux(argv)
	}
}

func openInTerminalMac(argv []string) error {
	// osascript needs the command as a single double-quoted string.
	// Escape embedded quotes and backslashes so a path containing them
	// doesn't break out of the script.
	full := shellQuote(argv)
	script := `tell application "Terminal" to do script "` + escapeAppleScript(full) + `"`
	cmd := exec.Command("osascript", "-e", script)
	HideConsole(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("osascript: %w", err)
	}
	return nil
}

func openInTerminalWindows(argv []string) error {
	if _, err := exec.LookPath("wt.exe"); err == nil {
		args := append([]string{}, argv...)
		c := exec.Command("wt.exe", args...)
		HideConsole(c)
		return c.Start()
	}
	full := shellQuote(argv)
	c := exec.Command("cmd.exe", "/C", "start", "cmd.exe", "/K", full)
	HideConsole(c)
	return c.Start()
}

func openInTerminalWSL(argv []string) error {
	distro := os.Getenv("WSL_DISTRO_NAME")
	if distro == "" {
		// Fall back to the Linux launcher set inside WSL — better than
		// silently no-oping when the env var is missing.
		return openInTerminalLinux(argv)
	}
	if _, err := exec.LookPath("wt.exe"); err == nil {
		args := append([]string{"wsl.exe", "-d", distro, "--"}, argv...)
		c := exec.Command("wt.exe", args...)
		HideConsole(c)
		return c.Start()
	}
	full := shellQuote(argv)
	c := exec.Command("cmd.exe", "/C", "start", "wsl.exe", "-d", distro, "--", full)
	HideConsole(c)
	return c.Start()
}

func openInTerminalLinux(argv []string) error {
	type termCandidate struct {
		name   string
		prefix []string
	}
	terminals := []termCandidate{
		{"kitty", nil},
		{"alacritty", []string{"-e"}},
		{"foot", []string{"-e"}},
		{"wezterm", []string{"start", "--"}},
		{"ghostty", []string{"-e"}},
		{"x-terminal-emulator", []string{"-e"}},
		{"gnome-terminal", []string{"--"}},
		{"konsole", []string{"-e"}},
		{"xfce4-terminal", []string{"-e"}},
		{"xterm", []string{"-e"}},
	}
	if envTerm := os.Getenv("TERMINAL"); envTerm != "" {
		if _, err := exec.LookPath(envTerm); err == nil {
			prefix := []string{"-e"}
			for _, t := range terminals {
				if t.name == envTerm {
					prefix = t.prefix
					break
				}
			}
			args := append(append([]string{}, prefix...), argv...)
			c := exec.Command(envTerm, args...)
			HideConsole(c)
			return c.Start()
		}
	}
	for _, t := range terminals {
		if _, err := exec.LookPath(t.name); err == nil {
			args := append(append([]string{}, t.prefix...), argv...)
			c := exec.Command(t.name, args...)
			HideConsole(c)
			return c.Start()
		}
	}
	return ErrNoTerminal
}

// shellQuote joins argv into a single command string suitable for
// passing through cmd /K or osascript do-script. Each token is wrapped
// in double quotes when it contains whitespace or shell metacharacters;
// embedded double quotes and backslashes are escaped.
func shellQuote(argv []string) string {
	var b strings.Builder
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		if needsQuoting(a) {
			b.WriteByte('"')
			for _, r := range a {
				if r == '"' || r == '\\' {
					b.WriteByte('\\')
				}
				b.WriteRune(r)
			}
			b.WriteByte('"')
		} else {
			b.WriteString(a)
		}
	}
	return b.String()
}

func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '"' || r == '\\' || r == '$' || r == '`' {
			return true
		}
	}
	return false
}

// escapeAppleScript escapes embedded backslashes and double quotes for
// inclusion inside a tell-application-do-script literal.
func escapeAppleScript(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
