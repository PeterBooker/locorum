package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// EnsureDir checks if a directory exists at the given path. If it does not exist, it creates the directory.
func ensureDir(path string) error {
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

func extractAssetsToDisk(fsys fs.FS, sourcePath, targetPath string) error {
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
