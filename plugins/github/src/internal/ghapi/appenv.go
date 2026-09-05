package ghapi

import (
	"fmt"
	"os"
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
	EnvBaseURL        = "GITHUB_WATCHER_APP_BASE_URL"
	EnvCachePath      = "GITHUB_WATCHER_APP_CACHE_PATH"
)

// AppFromEnv builds an App client from the shared env-based GitHub App
// configuration, or returns a nil client (not an error) when
// EnvAppID is unset — the caller's ambient-`gh`-auth fallback applies
// unchanged. defaultCachePath is used when EnvCachePath is unset; passing
// the same value github-watcher's own default resolves to lets both
// binaries mint or reuse one shared installation token.
func AppFromEnv(defaultCachePath string) (*App, error) {
	appID := os.Getenv(EnvAppID)
	if appID == "" {
		return nil, nil
	}
	privateKeyPath := os.Getenv(EnvPrivateKeyPath)
	if privateKeyPath == "" {
		return nil, fmt.Errorf("%s is set but %s is not", EnvAppID, EnvPrivateKeyPath)
	}
	installationID := os.Getenv(EnvInstallationID)
	owner := os.Getenv(EnvOwner)
	repo := os.Getenv(EnvRepo)
	if installationID == "" && (owner == "" || repo == "") {
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
		CachePath:      cachePath,
		BaseURL:        os.Getenv(EnvBaseURL),
	}, nil
}
