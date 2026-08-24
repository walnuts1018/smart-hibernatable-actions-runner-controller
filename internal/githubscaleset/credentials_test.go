package githubscaleset

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
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

	tests := []struct {
		name        string
		data        map[string][]byte
		wantClient  string
		wantInstall int64
		wantErr     bool
		errContains string
	}{
		{
			name: "valid with github_app_id",
			data: map[string][]byte{
				"github_app_id":              []byte("12345"),
				"github_app_installation_id": []byte("67890"),
				"github_app_private_key":     privPEM,
			},
			wantClient:  "12345",
			wantInstall: 67890,
		},
		{
			name: "valid with github_app_client_id and trailing newlines/spaces",
			data: map[string][]byte{
				"github_app_client_id":       []byte(" Iv1.1234567890 \n"),
				"github_app_installation_id": []byte(" 67890\n"),
				"github_app_private_key":     []byte(string(privPEM) + "\n\n"),
			},
			wantClient:  "Iv1.1234567890",
			wantInstall: 67890,
		},
		{
			name: "mutually exclusive github_app_id and github_app_client_id",
			data: map[string][]byte{
				"github_app_id":              []byte("12345"),
				"github_app_client_id":       []byte("Iv1.12345"),
				"github_app_installation_id": []byte("67890"),
				"github_app_private_key":     privPEM,
			},
			wantErr:     true,
			errContains: "mutually exclusive",
		},
		{
			name: "missing issuer",
			data: map[string][]byte{
				"github_app_installation_id": []byte("67890"),
				"github_app_private_key":     privPEM,
			},
			wantErr:     true,
			errContains: "missing GitHub App issuer",
		},
		{
			name: "missing installation_id",
			data: map[string][]byte{
				"github_app_id":          []byte("12345"),
				"github_app_private_key": privPEM,
			},
			wantErr:     true,
			errContains: "missing github_app_installation_id",
		},
		{
			name: "negative installation_id",
			data: map[string][]byte{
				"github_app_id":              []byte("12345"),
				"github_app_installation_id": []byte("-123"),
				"github_app_private_key":     privPEM,
			},
			wantErr:     true,
			errContains: "must be positive",
		},
		{
			name: "zero installation_id",
			data: map[string][]byte{
				"github_app_id":              []byte("12345"),
				"github_app_installation_id": []byte("0"),
				"github_app_private_key":     privPEM,
			},
			wantErr:     true,
			errContains: "must be positive",
		},
		{
			name: "invalid non-numeric installation_id",
			data: map[string][]byte{
				"github_app_id":              []byte("12345"),
				"github_app_installation_id": []byte("abc"),
				"github_app_private_key":     privPEM,
			},
			wantErr:     true,
			errContains: "invalid github_app_installation_id",
		},
		{
			name: "invalid private key PEM",
			data: map[string][]byte{
				"github_app_id":              []byte("12345"),
				"github_app_installation_id": []byte("67890"),
				"github_app_private_key":     []byte("not-a-valid-pem-key"),
			},
			wantErr:     true,
			errContains: "invalid RSA private key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth, err := ParseGitHubAppAuth(tt.data)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errContains)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if auth.ClientID != tt.wantClient {
				t.Errorf("expected ClientID %s, got %s", tt.wantClient, auth.ClientID)
			}
			if auth.InstallationID != tt.wantInstall {
				t.Errorf("expected InstallationID %d, got %d", tt.wantInstall, auth.InstallationID)
			}
		})
	}
}
