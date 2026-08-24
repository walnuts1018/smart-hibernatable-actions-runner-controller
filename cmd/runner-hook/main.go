package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/walnuts1018/smart-hibernatable-actions-runner-controller/internal/hook"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Fail-safe wrapper for job hooks
	defer func() {
		if r := recover(); r != nil {
			logger.Error("recovered from panic in runner-hook", "panic", r)
			// Do not crash the runner on hook failures
			os.Exit(0)
		}
	}()

	ctx := context.Background()

	// 1. Check argv[0] for symlink execution
	execName := filepath.Base(os.Args[0])
	switch execName {
	case "job-started":
		if err := hook.RunJobStarted(ctx, logger); err != nil {
			logger.Error("error in job-started hook", "error", err)
		}
		return
	case "job-completed":
		if err := hook.RunJobCompleted(ctx, logger); err != nil {
			logger.Error("error in job-completed hook", "error", err)
		}
		return
	}

	// 2. Check subcommand
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s [install <dir> | job-started | job-completed]\n", os.Args[0])
		os.Exit(1)
	}

	cmd := os.Args[1]
	switch cmd {
	case "install":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: %s install <destination_dir>\n", os.Args[0])
			os.Exit(1)
		}
		targetDir := os.Args[2]
		if err := hook.Install(targetDir); err != nil {
			logger.Error("failed to install runner-hook", "targetDir", targetDir, "error", err)
			os.Exit(1)
		}
		logger.Info("runner-hook installed successfully", "targetDir", targetDir)

	case "job-started":
		if err := hook.RunJobStarted(ctx, logger); err != nil {
			logger.Error("error in job-started hook", "error", err)
		}

	case "job-completed":
		if err := hook.RunJobCompleted(ctx, logger); err != nil {
			logger.Error("error in job-completed hook", "error", err)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}
