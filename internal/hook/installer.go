package hook

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Install copies the current executable into targetDir and sets up symlinks for job hooks.
func Install(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory %s: %w", targetDir, err)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get current executable path: %w", err)
	}

	destBinary := filepath.Join(targetDir, "runner-hook")
	if err := copyExecutable(execPath, destBinary); err != nil {
		return fmt.Errorf("failed to copy executable to %s: %w", destBinary, err)
	}

	// Create symlinks (or copies as fallback)
	links := []string{"job-started", "job-completed"}
	for _, link := range links {
		linkPath := filepath.Join(targetDir, link)
		_ = os.Remove(linkPath)
		if symErr := os.Symlink("runner-hook", linkPath); symErr != nil {
			// Fallback to copying binary if symlink fails
			if cpErr := copyExecutable(execPath, linkPath); cpErr != nil {
				return fmt.Errorf("failed to create hook binary %s: %w", linkPath, cpErr)
			}
		}
	}

	return nil
}

func copyExecutable(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	_ = os.Remove(dst)
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return os.Chmod(dst, 0755)
}
