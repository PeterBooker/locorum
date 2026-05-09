package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// EnsureDir checks if a directory exists at the given path. If it does not exist, it creates the directory.
func EnsureDir(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("failed to create directory %q: %w", path, err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to access directory %q: %w", path, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("path %q exists but is not a directory", path)
	}

	return nil
}

// DeleteDirFiles deletes all files in the specified directory.
func DeleteDirFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %q: %w", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("removing file %q: %w", path, err)
		}
	}

	return nil
}

// DeleteFile deletes a file.
func DeleteFile(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing file %q: %w", path, err)
	}

	return nil
}

// GetUserHomeDir returns the user's home directory. On Windows, it handles WSL paths correctly.
func GetUserHomeDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting user home directory: %w", err)
	}

	if runtime.GOOS == "windows" && HasWSL() {
		cmd := exec.Command("wsl", "wslpath", "-w", "$HOME")
		HideConsole(cmd)
		out, err := cmd.Output()
		if err != nil {
			return homeDir, fmt.Errorf("wslpath failed: %w", err)
		}

		homeDir = strings.TrimSpace(string(out))
	}

	return homeDir, nil
}

// OpenDirectory opens the specified directory in the system's file explorer.
func OpenDirectory(path string) error {
	switch runtime.GOOS {
	case "windows":
		if HasWSL() && strings.Contains(path, "\\wsl") {
			cmd := exec.Command("cmd.exe", "/C", "start", "", path)
			HideConsole(cmd)
			return cmd.Start()
		}

		cmd := exec.Command("explorer.exe", path)
		HideConsole(cmd)
		return cmd.Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		// Check if running Linux in a WSL2 environment.
		if isWSL() {
			// Convert to WSL path (handles C:\ and \\wsl$\…)
			out, err := exec.Command("wslpath", "-w", path).Output()
			if err != nil {
				return fmt.Errorf("wslpath failed: %w", err)
			}

			wslPath := strings.TrimSpace(string(out))

			return exec.Command("cmd.exe", "/C", "start", "", wslPath).Start()
		}

		// Native Linux environment. Prefer a known file-manager binary
		// because `xdg-open <dir>` silently no-ops when no MIME handler
		// is registered for inode/directory (common on minimal/tiling
		// setups like Hyprland without a full GNOME or KDE session).
		// Honour $FILE_MANAGER first if it points at something on PATH.
		fileManagers := []string{
			"nautilus", "dolphin", "nemo", "thunar", "pcmanfm", "caja",
		}
		if envFM := os.Getenv("FILE_MANAGER"); envFM != "" {
			if _, err := exec.LookPath(envFM); err == nil {
				return exec.Command(envFM, path).Start()
			}
		}
		for _, fm := range fileManagers {
			if _, err := exec.LookPath(fm); err == nil {
				return exec.Command(fm, path).Start()
			}
		}
		return exec.Command("xdg-open", path).Start()
	}
}

// isWSL returns true if running inside WSL2.
func isWSL() bool {
	if _, ok := os.LookupEnv("WSL_DISTRO_NAME"); ok {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(data), []byte("microsoft"))
}

// OpenURL opens the given URL in the user's default browser.
func OpenURL(url string) error {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("cmd.exe", "/C", "start", "", url)
		HideConsole(cmd)
		return cmd.Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		if isWSL() {
			return exec.Command("cmd.exe", "/C", "start", "", url).Start()
		}
		return exec.Command("xdg-open", url).Start()
	}
}

// OpenPath opens a local file in the user's default editor / viewer for
// that file type. Distinct from OpenURL because the WSL path needs a
// wslpath translation before cmd.exe can hand it off.
func OpenPath(path string) error {
	switch runtime.GOOS {
	case "windows":
		cmd := exec.Command("cmd.exe", "/C", "start", "", path)
		HideConsole(cmd)
		return cmd.Start()
	case "darwin":
		return exec.Command("open", path).Start()
	default:
		if isWSL() {
			out, err := exec.Command("wslpath", "-w", path).Output()
			if err != nil {
				return fmt.Errorf("wslpath: %w", err)
			}
			win := strings.TrimSpace(string(out))
			return exec.Command("cmd.exe", "/C", "start", "", win).Start()
		}
		return exec.Command("xdg-open", path).Start()
	}
}

// OpenTerminalWithCommand opens a terminal emulator running the given command.
func OpenTerminalWithCommand(args ...string) error {
	fullCmd := strings.Join(args, " ")
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "Terminal" to do script %q`, fullCmd)
		return exec.Command("osascript", "-e", script).Start()
	case "windows":
		return exec.Command("cmd.exe", "/C", "start", "cmd.exe", "/K", fullCmd).Start()
	default:
		if isWSL() {
			distro := os.Getenv("WSL_DISTRO_NAME")
			if _, err := exec.LookPath("wt.exe"); err == nil {
				cmdArgs := append([]string{"wsl.exe", "-d", distro, "--"}, args...)
				return exec.Command("wt.exe", cmdArgs...).Start()
			}
			return exec.Command("cmd.exe", "/C", "start", "wsl.exe", "-d", distro, "--", fullCmd).Start()
		}
		// Native Linux: try common terminal emulators. The `prefix` is the
		// arg sequence that must come before the command — empty for
		// terminals that take the program as positional args (kitty), a
		// flag like "-e" for most others, and a multi-token "start --"
		// for wezterm.
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
		// Honour $TERMINAL first if it points at a known emulator.
		if envTerm := os.Getenv("TERMINAL"); envTerm != "" {
			if _, err := exec.LookPath(envTerm); err == nil {
				prefix := []string{"-e"}
				for _, t := range terminals {
					if t.name == envTerm {
						prefix = t.prefix
						break
					}
				}
				cmdArgs := append(append([]string{}, prefix...), args...)
				return exec.Command(envTerm, cmdArgs...).Start()
			}
		}
		for _, t := range terminals {
			if _, err := exec.LookPath(t.name); err == nil {
				cmdArgs := append(append([]string{}, t.prefix...), args...)
				return exec.Command(t.name, cmdArgs...).Start()
			}
		}
		return errors.New("no terminal emulator found")
	}
}

// CopyDir recursively copies the directory at src to dst.
func CopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o777)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o666)
	})
}

// HasWSL returns true if WSL is available (Windows only).
func HasWSL() bool {
	cmd := exec.Command("wsl.exe", "echo", "wsl-test")
	HideConsole(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return false
	}

	return strings.TrimSpace(out.String()) == "wsl-test"
}

// PickDirectoryInWSL opens a directory picker dialog inside WSL using zenity.
// Returns the selected Linux path (e.g., /home/user/projects).
func PickDirectoryInWSL() (string, error) {
	cmd := exec.Command("wsl.exe", "zenity", "--file-selection", "--directory")
	HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("WSL directory picker failed (is zenity installed in WSL?): %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", errors.New("no directory selected")
	}
	return dir, nil
}
