package logger

import (
	"log/slog"
	"os"
	"strings"

	"github.com/go-logr/logr"
	console "github.com/phsym/console-slog"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
)

type LogType string

const (
	LogTypeJSON LogType = "json"
	LogTypeText LogType = "text"
)

// New creates a new *slog.Logger based on log level and type strings.
func New(logLevelStr, logTypeStr string) *slog.Logger {
	var logLevel slog.Level
	switch strings.ToLower(logLevelStr) {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		slog.Warn("Invalid log level, use default level: info")
		logLevel = slog.LevelInfo
	}

	var logType LogType
	switch strings.ToLower(logTypeStr) {
	case "json":
		logType = LogTypeJSON
	case "text":
		logType = LogTypeText
	default:
		slog.Warn("Invalid log type, use default type: json")
		logType = LogTypeJSON
	}

	var handler slog.Handler
	switch logType {
	case LogTypeText:
		handler = console.NewHandler(os.Stdout, &console.HandlerOptions{
			Level:     logLevel,
			AddSource: logLevel == slog.LevelDebug,
		})
	case LogTypeJSON:
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			Level:     logLevel,
			AddSource: logLevel == slog.LevelDebug,
		})
	}

	return slog.New(handler)
}

// Setup initializes the default slog logger, connects controller-runtime and klog to the slog handler, and returns the slog.Logger.
func Setup(logLevelStr, logTypeStr string) *slog.Logger {
	l := New(logLevelStr, logTypeStr)
	slog.SetDefault(l)
	logrLogger := logr.FromSlogHandler(l.Handler())
	klog.SetLogger(logrLogger)
	ctrl.SetLogger(logrLogger)
	return l
}
