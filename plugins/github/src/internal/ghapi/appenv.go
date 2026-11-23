package ghapi

import (
	"fmt"
	"os"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/tokencommand"
)

// The env var names github-watcher serve/gh-api already read for its
// deployment-level GitHub App auth (see cmd/github-watcher/app_auth.go).
// query-pulls reuses them rather than inventing a second credential
// surface: one App installation is configured once per deployment, not
// once per binary that calls the GitHub API on its behalf.
const (
	EnvAppID          = "GITHUB_WATCHER_APP_ID"
	EnvInstallationID = "GITHUB_WATCHER_APP_INSTALLATION_ID"
	EnvOwner          = "GITHUB_WATCHER_APP_OWNER"
	EnvRepo           = "GITHUB_WATCHER_APP_REPO"
	EnvPrivateKeyPath = "GITHUB_WATCHER_APP_PRIVATE_KEY_PATH"
	EnvTokenCommand   = "GITHUB_WATCHER_APP_TOKEN_COMMAND"
	EnvBaseURL        = "GITHUB_WATCHER_APP_BASE_URL"
	EnvCachePath      = "GITHUB_WATCHER_APP_CACHE_PATH"
)

// AppFromEnv builds an App client from the shared env-based GitHub App
// configuration, or returns a nil client (not an error) when
// EnvAppID is unset — the caller's ambient-`gh`-auth fallback applies
// unchanged. Key-path auth uses defaultCachePath when EnvCachePath is unset.
func AppFromEnv(defaultCachePath string) (*App, error) {
	appID := os.Getenv(EnvAppID)
	privateKeyPath := os.Getenv(EnvPrivateKeyPath)
	commandJSON := os.Getenv(EnvTokenCommand)
	if appID == "" && privateKeyPath == "" && commandJSON == "" {
		return nil, nil
	}
	if (privateKeyPath == "") == (commandJSON == "") {
		return nil, fmt.Errorf("exactly one of %s and %s is required", EnvPrivateKeyPath, EnvTokenCommand)
	}
	var command []string
	if commandJSON != "" {
		var err error
		command, err = tokencommand.Parse(commandJSON)
		if err != nil {
			return nil, fmt.Errorf("%s (alternative: %s): %w", EnvTokenCommand, EnvPrivateKeyPath, err)
		}
	}
	if privateKeyPath != "" && appID == "" {
		return nil, fmt.Errorf("%s requires %s", EnvPrivateKeyPath, EnvAppID)
	}
	installationID := os.Getenv(EnvInstallationID)
	owner := os.Getenv(EnvOwner)
	repo := os.Getenv(EnvRepo)
	if privateKeyPath != "" && installationID == "" && (owner == "" || repo == "") {
		return nil, fmt.Errorf("%s is set but none of %s, or both %s and %s, are set", EnvAppID, EnvInstallationID, EnvOwner, EnvRepo)
	}
	cachePath := os.Getenv(EnvCachePath)
	if cachePath == "" {
		cachePath = defaultCachePath
	}
	return &App{
		AppID:          appID,
		InstallationID: installationID,
		Owner:          owner,
		Repo:           repo,
		PrivateKeyPath: privateKeyPath,
		TokenCommand:   command,
		CachePath:      cachePath,
		BaseURL:        os.Getenv(EnvBaseURL),
	}, nil
}
