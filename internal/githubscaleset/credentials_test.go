package githubscaleset

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestParseGitHubAppAuth(t *testing.T) {
	// Generate dummy RSA private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate rsa key: %v", err)
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	data := map[string][]byte{
		"github_app_id":              []byte("12345"),
		"github_app_installation_id": []byte("67890"),
		"github_app_private_key":     privPEM,
	}

	auth, err := ParseGitHubAppAuth(data)
	if err != nil {
		t.Fatalf("unexpected error parsing github app auth: %v", err)
	}

	if auth.ClientID != "12345" {
		t.Errorf("expected ClientID 12345, got %s", auth.ClientID)
	}

	if auth.InstallationID != 67890 {
		t.Errorf("expected InstallationID 67890, got %d", auth.InstallationID)
	}

	if auth.PrivateKey != string(privPEM) {
		t.Errorf("expected PrivateKey matches")
	}
}
