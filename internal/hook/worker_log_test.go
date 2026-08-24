package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractJobDisplayName(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. JSON JobDisplayName
	log1 := filepath.Join(tmpDir, "Worker_1.log")
	content1 := `[2026-08-24 12:00:00Z INFO Worker] Job message:
{
  "jobId": "12345",
  "jobDisplayName": "Build and Test (linux-amd64)",
  "requestId": 1
}`
	if err := os.WriteFile(log1, []byte(content1), 0644); err != nil {
		t.Fatalf("failed to write log1: %v", err)
	}

	name1 := ExtractJobDisplayName(log1, "fallback")
	if name1 != "Build and Test (linux-amd64)" {
		t.Errorf("expected 'Build and Test (linux-amd64)', got %q", name1)
	}

	// 2. Starting line pattern
	log2 := filepath.Join(tmpDir, "Worker_2.log")
	content2 := `[2026-08-24 12:00:00Z INFO StepsRunner] Starting: E2E Integration Tests`
	if err := os.WriteFile(log2, []byte(content2), 0644); err != nil {
		t.Fatalf("failed to write log2: %v", err)
	}

	name2 := ExtractJobDisplayName(log2, "fallback")
	if name2 != "E2E Integration Tests" {
		t.Errorf("expected 'E2E Integration Tests', got %q", name2)
	}

	// 3. Fallback when log not found or empty
	name3 := ExtractJobDisplayName("", "my-fallback-job")
	if name3 != "my-fallback-job" {
		t.Errorf("expected 'my-fallback-job', got %q", name3)
	}
}
