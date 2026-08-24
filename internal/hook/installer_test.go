package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstall(t *testing.T) {
	tmpDir := t.TempDir()

	err := Install(tmpDir)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify runner-hook binary exists
	binPath := filepath.Join(tmpDir, "runner-hook")
	if info, err := os.Stat(binPath); err != nil {
		t.Errorf("expected runner-hook binary to exist: %v", err)
	} else if info.Mode()&0111 == 0 {
		t.Errorf("expected runner-hook binary to be executable, mode: %v", info.Mode())
	}

	// Verify job-started and job-completed links/files exist
	for _, hookName := range []string{"job-started", "job-completed"} {
		hookPath := filepath.Join(tmpDir, hookName)
		if _, err := os.Stat(hookPath); err != nil {
			t.Errorf("expected %s to exist: %v", hookName, err)
		}
	}
}
