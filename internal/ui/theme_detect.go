package ui

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/PeterBooker/locorum/internal/utils"
)

// DetectSystemTheme inspects the host OS for a light/dark preference.
// Falls back to ThemeDark if the preference can't be determined.
func DetectSystemTheme() ThemeMode {
	switch runtime.GOOS {
	case "darwin":
		return detectMacOS()
	case "windows":
		return detectWindows()
	default:
		return detectLinux()
	}
}

func detectLinux() ThemeMode {
	// Try xdg-desktop-portal first (Wayland-friendly, vendor-neutral).
	out, err := exec.Command("gdbus", "call", "--session",
		"--dest", "org.freedesktop.portal.Desktop",
		"--object-path", "/org/freedesktop/portal/desktop",
		"--method", "org.freedesktop.portal.Settings.Read",
		"org.freedesktop.appearance", "color-scheme",
	).Output()
	if err == nil {
		s := string(out)
		// uint32 1 = prefer dark, 2 = prefer light.
		if strings.Contains(s, "uint32 1") {
			return ThemeDark
		}
		if strings.Contains(s, "uint32 2") {
			return ThemeLight
		}
	}

	// Fall back to GNOME's gsettings.
	out, err = exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
	if err == nil {
		s := strings.TrimSpace(string(out))
		if strings.Contains(s, "dark") {
			return ThemeDark
		}
		if strings.Contains(s, "light") || strings.Contains(s, "default") {
			return ThemeLight
		}
	}

	return ThemeDark
}

func detectMacOS() ThemeMode {
	out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err == nil && strings.TrimSpace(string(out)) == "Dark" {
		return ThemeDark
	}
	return ThemeLight
}

func detectWindows() ThemeMode {
	cmd := exec.Command("reg", "query",
		"HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Themes\\Personalize",
		"/v", "AppsUseLightTheme",
	)
	utils.HideConsole(cmd)
	out, err := cmd.Output()
	if err == nil {
		// Output contains "AppsUseLightTheme    REG_DWORD    0x0" (dark) or 0x1 (light)
		s := string(out)
		if strings.Contains(s, "0x0") {
			return ThemeDark
		}
		if strings.Contains(s, "0x1") {
			return ThemeLight
		}
	}
	return ThemeDark
}
