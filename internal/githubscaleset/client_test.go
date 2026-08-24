package githubscaleset

import (
	"testing"
)

func TestNewClient_NilAuth(t *testing.T) {
	_, err := NewClient("https://github.com/walnuts1018/test", nil)
	if err == nil {
		t.Fatal("expected error when auth is nil, got nil")
	}
	expectedMsg := "GitHub App auth is required"
	if err.Error() != expectedMsg {
		t.Fatalf("expected error %q, got %q", expectedMsg, err.Error())
	}
}
