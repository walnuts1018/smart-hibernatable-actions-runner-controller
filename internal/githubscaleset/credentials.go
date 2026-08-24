package githubscaleset

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/actions/scaleset"
	"github.com/golang-jwt/jwt/v4"
)

// ParseGitHubAppAuth extracts and validates GitHubAppAuth from a secret map containing GitHub App credentials.
func ParseGitHubAppAuth(data map[string][]byte) (*scaleset.GitHubAppAuth, error) {
	appID, hasAppID := data["github_app_id"]
	clientID, hasClientID := data["github_app_client_id"]

	if hasAppID && hasClientID {
		return nil, errors.New("github_app_id and github_app_client_id are mutually exclusive")
	}
	if !hasAppID && !hasClientID {
		return nil, errors.New("missing GitHub App issuer in secret: expected github_app_id or github_app_client_id")
	}

	var issuerBytes []byte
	if hasAppID {
		issuerBytes = appID
	} else {
		issuerBytes = clientID
	}

	issuer := strings.TrimSpace(string(issuerBytes))
	if issuer == "" {
		return nil, errors.New("github_app_id or github_app_client_id must not be empty")
	}

	installIDBytes, ok := data["github_app_installation_id"]
	if !ok {
		return nil, errors.New("missing github_app_installation_id in secret")
	}

	installIDText := strings.TrimSpace(string(installIDBytes))
	installID, err := strconv.ParseInt(installIDText, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid github_app_installation_id: %w", err)
	}
	if installID <= 0 {
		return nil, fmt.Errorf("github_app_installation_id must be positive: %d", installID)
	}

	privateKeyBytes, ok := data["github_app_private_key"]
	if !ok {
		return nil, errors.New("missing github_app_private_key in secret")
	}

	privateKeyBytes = bytes.TrimSpace(privateKeyBytes)
	if len(privateKeyBytes) == 0 {
		return nil, errors.New("github_app_private_key must not be empty")
	}

	if _, err := jwt.ParseRSAPrivateKeyFromPEM(privateKeyBytes); err != nil {
		return nil, fmt.Errorf("invalid RSA private key in github_app_private_key: %w", err)
	}

	auth := &scaleset.GitHubAppAuth{
		ClientID:       issuer,
		InstallationID: installID,
		PrivateKey:     string(privateKeyBytes),
	}

	if err := auth.Validate(); err != nil {
		return nil, fmt.Errorf("invalid GitHub App credentials: %w", err)
	}

	return auth, nil
}
