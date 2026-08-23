package githubscaleset

import (
	"fmt"
	"strconv"

	"github.com/actions/scaleset"
)

// ParseGitHubAppAuth extracts GitHubAppAuth from a secret map containing GitHub App credentials.
func ParseGitHubAppAuth(data map[string][]byte) (*scaleset.GitHubAppAuth, error) {
	appIDBytes, ok := data["github_app_id"]
	if !ok {
		appIDBytes, ok = data["github_app_client_id"]
	}
	if !ok {
		return nil, fmt.Errorf("missing github_app_id in secret")
	}

	installIDBytes, ok := data["github_app_installation_id"]
	if !ok {
		return nil, fmt.Errorf("missing github_app_installation_id in secret")
	}

	privateKeyBytes, ok := data["github_app_private_key"]
	if !ok {
		return nil, fmt.Errorf("missing github_app_private_key in secret")
	}

	installID, err := strconv.ParseInt(string(installIDBytes), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid github_app_installation_id: %w", err)
	}

	auth := &scaleset.GitHubAppAuth{
		ClientID:       string(appIDBytes),
		InstallationID: installID,
		PrivateKey:     string(privateKeyBytes),
	}

	if err := auth.Validate(); err != nil {
		return nil, fmt.Errorf("invalid GitHub App credentials: %w", err)
	}

	return auth, nil
}
