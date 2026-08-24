package logger

import (
	"log/slog"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		levelStr string
		typeStr  string
	}{
		{name: "info json", levelStr: "info", typeStr: "json"},
		{name: "debug text", levelStr: "debug", typeStr: "text"},
		{name: "warn invalid type", levelStr: "warn", typeStr: "unknown"},
		{name: "error invalid level", levelStr: "unknown", typeStr: "json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New(tt.levelStr, tt.typeStr)
			if l == nil {
				t.Fatalf("expected non-nil logger")
			}
		})
	}
}

func TestSetup(t *testing.T) {
	l := Setup("info", "json")
	if l == nil {
		t.Fatalf("expected non-nil logger")
	}
	if slog.Default() != l {
		t.Fatalf("expected slog.Default() to match returned logger")
	}
}
