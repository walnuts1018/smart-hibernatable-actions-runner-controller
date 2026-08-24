package hook

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	jobDisplayNameRegex = regexp.MustCompile(`(?i)"jobDisplayName"\s*:\s*"([^"]+)"`)
	startingRegex       = regexp.MustCompile(`Starting:\s*(.+)`)
)

// FindLatestWorkerLog searches common diagnostics directories for the latest Worker_*.log file.
func FindLatestWorkerLog() string {
	candidateDirs := []string{
		"/home/runner/_diag",
		"/actions-runner/_diag",
		"/runner/_diag",
		"_diag",
	}

	if runnerWs := os.Getenv("RUNNER_WORKSPACE"); runnerWs != "" {
		candidateDirs = append([]string{
			filepath.Join(runnerWs, "..", "_diag"),
			filepath.Join(strings.TrimSuffix(runnerWs, "/_work"), "_diag"),
		}, candidateDirs...)
	}

	var latestPath string
	var latestModTime int64

	for _, d := range candidateDirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, "Worker_") && strings.HasSuffix(name, ".log") {
				fullPath := filepath.Join(d, name)
				info, statErr := entry.Info()
				if statErr != nil {
					continue
				}
				modTime := info.ModTime().UnixNano()
				if modTime > latestModTime {
					latestModTime = modTime
					latestPath = fullPath
				}
			}
		}
		if latestPath != "" {
			return latestPath
		}
	}

	return latestPath
}

// ExtractJobDisplayName attempts to extract the job display name from a Worker_*.log file.
// If the log is missing or display name is not found, fallback is used.
func ExtractJobDisplayName(workerLogPath, fallback string) string {
	if workerLogPath == "" {
		if fallback != "" {
			return fallback
		}
		return DefaultUnknownValue
	}

	file, err := os.Open(workerLogPath)
	if err != nil {
		if fallback != "" {
			return fallback
		}
		return DefaultUnknownValue
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()

		// 1. Match "jobDisplayName": "..."
		if match := jobDisplayNameRegex.FindStringSubmatch(line); len(match) > 1 {
			name := strings.TrimSpace(match[1])
			if name != "" {
				return name
			}
		}

		// 2. Match "Starting: ..."
		if match := startingRegex.FindStringSubmatch(line); len(match) > 1 {
			name := strings.TrimSpace(match[1])
			if name != "" {
				return name
			}
		}
	}

	if fallback != "" {
		return fallback
	}
	return DefaultUnknownValue
}
