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

// OpenDirectory opens the specified directory in the system's file explorer.
func OpenDirectory(path string) error {
	switch runtime.GOOS {
	case "windows":
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
