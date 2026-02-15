package utils

import (
	"bytes"
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
		if err := os.MkdirAll(path, 0755); err != nil {
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

// ExtractAssetsToDisk extracts files from the given filesystem to the specified target path.
func ExtractAssetsToDisk(fsys fs.FS, sourcePath, targetPath string) error {
	return fs.WalkDir(fsys, sourcePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}

		// Prepare target file path
		relPath, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return err
		}
		targetFilePath := filepath.Join(targetPath, relPath)

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(targetFilePath), 0755); err != nil {
			return err
		}

		return os.WriteFile(targetFilePath, data, 0644)
	})
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
		out, err := exec.Command("wsl", "wslpath", "-w", "$HOME").Output()
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
			return exec.Command("cmd.exe", "/C", "start", "", path).Start()
		}

		return exec.Command("explorer.exe", path).Start()
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

		// Native Linux environment.
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
		return exec.Command("cmd.exe", "/C", "start", "", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		if isWSL() {
			return exec.Command("cmd.exe", "/C", "start", "", url).Start()
		}
		return exec.Command("xdg-open", url).Start()
	}
}

// OpenTerminalWithCommand opens a terminal emulator running the given command.
func OpenTerminalWithCommand(args ...string) error {
	fullCmd := strings.Join(args, " ")
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`tell application "Terminal" to do script "%s"`, fullCmd)
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
		// Native Linux: try common terminal emulators.
		terminals := []struct {
			name string
			flag string
		}{
			{"x-terminal-emulator", "-e"},
			{"gnome-terminal", "--"},
			{"konsole", "-e"},
			{"xfce4-terminal", "-e"},
			{"xterm", "-e"},
		}
		for _, t := range terminals {
			if _, err := exec.LookPath(t.name); err == nil {
				cmdArgs := append([]string{t.flag}, args...)
				return exec.Command(t.name, cmdArgs...).Start()
			}
		}
		return fmt.Errorf("no terminal emulator found")
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
			return os.MkdirAll(target, 0777)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0666)
	})
}

// HasWSL returns true if WSL is available (Windows only).
func HasWSL() bool {
	cmd := exec.Command("wsl.exe", "echo", "wsl-test")
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
	out, err := exec.Command("wsl.exe", "zenity", "--file-selection", "--directory").Output()
	if err != nil {
		return "", fmt.Errorf("WSL directory picker failed (is zenity installed in WSL?): %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("no directory selected")
	}
	return dir, nil
}
