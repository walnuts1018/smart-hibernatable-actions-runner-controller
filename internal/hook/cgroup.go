package hook

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// FindPodCgroup searches for the cgroup v2 directory of a specific Pod UID.
// It searches under cgroupRoot (typically /host/sys/fs/cgroup) up to maxDepth.
// If not found or if cgroupRoot is empty, it falls back to fallbackDir (e.g. /sys/fs/cgroup).
func FindPodCgroup(cgroupRoot, podUID, fallbackDir string) string {
	if fallbackDir == "" {
		fallbackDir = "/sys/fs/cgroup"
	}

	if cgroupRoot == "" || podUID == "" {
		return fallbackDir
	}

	// Normalize UID candidates:
	// 1. "12345678-1234-1234-1234-123456789abc"
	// 2. "12345678_1234_1234_1234_123456789abc" (systemd slice naming)
	// 3. "12345678123412341234123456789abc"
	rawUID := strings.ToLower(podUID)
	underscoreUID := strings.ReplaceAll(rawUID, "-", "_")
	compactUID := strings.ReplaceAll(rawUID, "-", "")

	var matchedPath string
	const maxDepth = 5

	_ = filepath.WalkDir(cgroupRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(cgroupRoot, path)
		if err != nil {
			return nil
		}
		depth := strings.Count(rel, string(os.PathSeparator))
		if rel == "." {
			depth = 0
		}
		if depth > maxDepth {
			return fs.SkipDir
		}

		base := strings.ToLower(d.Name())
		if strings.Contains(base, rawUID) || strings.Contains(base, underscoreUID) || strings.Contains(base, compactUID) {
			// Check if this directory looks like a valid cgroup directory
			if hasCgroupStats(path) {
				matchedPath = path
				return fs.SkipAll
			}
		}

		return nil
	})

	if matchedPath != "" {
		return matchedPath
	}

	return fallbackDir
}

func hasCgroupStats(dir string) bool {
	// cgroup v2
	if _, err := os.Stat(filepath.Join(dir, "cpu.stat")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "memory.current")); err == nil {
		return true
	}
	// cgroup v1
	if _, err := os.Stat(filepath.Join(dir, "cpuacct.usage")); err == nil {
		return true
	}
	return false
}

// ReadCPUUsageUsec reads CPU usage in microseconds from cgroup.
func ReadCPUUsageUsec(cgroupPath string) (uint64, error) {
	// 1. Try cgroup v2 cpu.stat
	cpuStatPath := filepath.Join(cgroupPath, "cpu.stat")
	if file, err := os.Open(cpuStatPath); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "usage_usec" {
				val, parseErr := strconv.ParseUint(fields[1], 10, 64)
				if parseErr == nil {
					return val, nil
				}
			}
		}
	}

	// 2. Try cgroup v1 cpuacct.usage (nanoseconds)
	cpuacctPath := filepath.Join(cgroupPath, "cpuacct.usage")
	if data, err := os.ReadFile(cpuacctPath); err == nil {
		val, parseErr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if parseErr == nil {
			return val / 1000, nil // Convert ns to usec
		}
	}

	return 0, fmt.Errorf("no cpu usage found in %s", cgroupPath)
}

// ReadMemoryPeakBytes reads the peak (or current) memory usage in bytes from cgroup.
func ReadMemoryPeakBytes(cgroupPath string) (uint64, error) {
	// 1. Try cgroup v2 memory.peak
	peakPath := filepath.Join(cgroupPath, "memory.peak")
	if data, err := os.ReadFile(peakPath); err == nil {
		val, parseErr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if parseErr == nil && val > 0 {
			return val, nil
		}
	}

	// 2. Try cgroup v2 memory.current
	currentPath := filepath.Join(cgroupPath, "memory.current")
	if data, err := os.ReadFile(currentPath); err == nil {
		val, parseErr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if parseErr == nil {
			return val, nil
		}
	}

	// 3. Try cgroup v1 memory.max_usage_in_bytes
	maxUsagePath := filepath.Join(cgroupPath, "memory.max_usage_in_bytes")
	if data, err := os.ReadFile(maxUsagePath); err == nil {
		val, parseErr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if parseErr == nil {
			return val, nil
		}
	}

	// 4. Try cgroup v1 memory.usage_in_bytes
	usagePath := filepath.Join(cgroupPath, "memory.usage_in_bytes")
	if data, err := os.ReadFile(usagePath); err == nil {
		val, parseErr := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if parseErr == nil {
			return val, nil
		}
	}

	return 0, fmt.Errorf("no memory usage found in %s", cgroupPath)
}
