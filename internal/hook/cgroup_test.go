package hook

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindPodCgroup(t *testing.T) {
	tmpDir := t.TempDir()
	podUID := "12345678-abcd-1234-abcd-1234567890ab"

	// Mock systemd burstable cgroup path:
	// kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod12345678_abcd_1234_abcd_1234567890ab.slice
	podDir := filepath.Join(tmpDir, "kubepods.slice", "kubepods-burstable.slice", "kubepods-burstable-pod12345678_abcd_1234_abcd_1234567890ab.slice")
	if err := os.MkdirAll(podDir, 0755); err != nil {
		t.Fatalf("failed to create pod dir: %v", err)
	}

	// Create cpu.stat file
	cpuStatContent := "usage_usec 123456\nuser_usec 100000\nsystem_usec 23456\n"
	if err := os.WriteFile(filepath.Join(podDir, "cpu.stat"), []byte(cpuStatContent), 0644); err != nil {
		t.Fatalf("failed to write cpu.stat: %v", err)
	}

	found := FindPodCgroup(tmpDir, podUID, "/sys/fs/cgroup")
	if found != podDir {
		t.Errorf("expected %s, got %s", podDir, found)
	}

	// Test fallback when UID not found
	fallback := FindPodCgroup(tmpDir, "non-existent-uid", "/sys/fs/cgroup")
	if fallback != "/sys/fs/cgroup" {
		t.Errorf("expected fallback /sys/fs/cgroup, got %s", fallback)
	}
}

func TestReadCPUUsageUsec(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. cgroup v2
	cpuStatContent := "usage_usec 987654321\nuser_usec 500000\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "cpu.stat"), []byte(cpuStatContent), 0644); err != nil {
		t.Fatalf("failed to write cpu.stat: %v", err)
	}

	usage, err := ReadCPUUsageUsec(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error reading cpu usage: %v", err)
	}
	if usage != 987654321 {
		t.Errorf("expected 987654321, got %d", usage)
	}

	// 2. cgroup v1 fallback
	tmpDirV1 := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDirV1, "cpuacct.usage"), []byte("5000000000\n"), 0644); err != nil {
		t.Fatalf("failed to write cpuacct.usage: %v", err)
	}

	usageV1, err := ReadCPUUsageUsec(tmpDirV1)
	if err != nil {
		t.Fatalf("unexpected error reading cpuacct.usage: %v", err)
	}
	if usageV1 != 5000000 { // 5000000000 ns / 1000 = 5000000 usec
		t.Errorf("expected 5000000 usec, got %d", usageV1)
	}
}

func TestReadMemoryPeakBytes(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. memory.peak
	if err := os.WriteFile(filepath.Join(tmpDir, "memory.peak"), []byte("2147483648\n"), 0644); err != nil {
		t.Fatalf("failed to write memory.peak: %v", err)
	}

	peak, err := ReadMemoryPeakBytes(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if peak != 2147483648 {
		t.Errorf("expected 2147483648, got %d", peak)
	}

	// 2. fallback to memory.current
	tmpDirCurrent := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDirCurrent, "memory.current"), []byte("1073741824\n"), 0644); err != nil {
		t.Fatalf("failed to write memory.current: %v", err)
	}

	current, err := ReadMemoryPeakBytes(tmpDirCurrent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if current != 1073741824 {
		t.Errorf("expected 1073741824, got %d", current)
	}
}
