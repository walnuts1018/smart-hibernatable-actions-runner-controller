package runner

import (
	"strings"
	"testing"
)

func TestGenerateRunnerName(t *testing.T) {
	// Normal prefix
	name1 := GenerateRunnerName("my-scale-set")
	if !strings.HasPrefix(name1, "my-scale-set-") {
		t.Errorf("expected name to start with 'my-scale-set-', got %q", name1)
	}

	// Long prefix (>48 characters)
	longPrefix := "this-is-a-very-long-scale-set-name-that-exceeds-forty-eight-characters-limit"
	name2 := GenerateRunnerName(longPrefix)
	if !strings.HasPrefix(name2, longPrefix[:48]+"-") {
		t.Errorf("expected name to truncate prefix to 48 chars, got %q", name2)
	}

	// Uniqueness
	nameA := GenerateRunnerName("scale-set")
	nameB := GenerateRunnerName("scale-set")
	if nameA == nameB {
		t.Errorf("expected generated runner names to be unique, got both %q", nameA)
	}
}

func TestJitSecretName(t *testing.T) {
	runnerName := "scale-set-12345"
	secretName := JitSecretName(runnerName)
	expected := "scale-set-12345-jit"
	if secretName != expected {
		t.Errorf("expected %q, got %q", expected, secretName)
	}
}
