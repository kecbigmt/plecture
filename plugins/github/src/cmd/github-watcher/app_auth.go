package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/apptoken"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/githubapp"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/watcher"
)

// defaultAppSkew matches gh-app-token's own default, so a token minted here
// and one minted by gh_app_guard for the same installation refresh at the
// same point in the token's lifetime, even though each mints/reuses
// independently.
const defaultAppSkew = 5 * time.Minute

// Env, not flags: gh-api runs as a process separate from serve and can't
// receive its flags. Unset GITHUB_WATCHER_APP_ID means no App auth
// configured — ambient `gh auth` stays unchanged.
const (
	envAppID          = "GITHUB_WATCHER_APP_ID"
	envInstallationID = "GITHUB_WATCHER_APP_INSTALLATION_ID"
	envOwner          = "GITHUB_WATCHER_APP_OWNER"
	envRepo           = "GITHUB_WATCHER_APP_REPO"
	envPrivateKeyPath = "GITHUB_WATCHER_APP_PRIVATE_KEY_PATH"
	envBaseURL        = "GITHUB_WATCHER_APP_BASE_URL"
	envCachePath      = "GITHUB_WATCHER_APP_CACHE_PATH"
)

// appAuthConfig is validated enough to attempt a mint; the mint itself can
// still fail on a bad key or installation (see tokenFunc).
type appAuthConfig struct {
	appID, installationID, owner, repo, privateKeyPath, baseURL, cachePath string
}

// appAuthFromEnv fails loud on a partial App-auth configuration (an id with
// no key, or no way to identify an installation) rather than deferring the
// error to the first mint attempt.
func appAuthFromEnv(dataDir string) (*appAuthConfig, error) {
	appID := os.Getenv(envAppID)
	if appID == "" {
		return nil, nil
	}
	privateKeyPath := os.Getenv(envPrivateKeyPath)
	if privateKeyPath == "" {
		return nil, fmt.Errorf("%s is set but %s is not", envAppID, envPrivateKeyPath)
	}
	installationID := os.Getenv(envInstallationID)
	owner := os.Getenv(envOwner)
	repo := os.Getenv(envRepo)
	if installationID == "" && (owner == "" || repo == "") {
		return nil, fmt.Errorf("%s is set but none of %s, or both %s and %s, are set", envAppID, envInstallationID, envOwner, envRepo)
	}
	baseURL := os.Getenv(envBaseURL)
	if baseURL == "" {
		baseURL = githubapp.DefaultBaseURL
	}
	cachePath := os.Getenv(envCachePath)
	if cachePath == "" {
		// Shares ghAPIDataDir's directory so serve and gh-api locate the same
		// token cache the same way they already locate the same rate budget.
		cachePath = filepath.Join(dataDir, ".app-token-cache.json")
	}
	return &appAuthConfig{
		appID:          appID,
		installationID: installationID,
		owner:          owner,
		repo:           repo,
		privateKeyPath: privateKeyPath,
		baseURL:        baseURL,
		cachePath:      cachePath,
	}, nil
}

// tokenFunc loads the private key eagerly so an unreadable key fails here
// once, not on the first mint attempt buried inside a poll tick.
func (c *appAuthConfig) tokenFunc() (func() (string, error), error) {
	key, err := githubapp.LoadPrivateKey(c.privateKeyPath)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	cache := apptoken.NewCache(c.cachePath)
	return func() (string, error) {
		return githubapp.Token(cache, defaultAppSkew, client, c.baseURL, c.appID, key, c.installationID, c.owner, c.repo)
	}, nil
}

// appAuthTokenFunc, unlike configureAppAuth, does not mint eagerly: a
// one-shot gh-api call already fails loud the moment the returned func's
// mint fails, with no separate startup phase to guard.
func appAuthTokenFunc(dataDir string) (func() (string, error), error) {
	cfg, err := appAuthFromEnv(dataDir)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	return cfg.tokenFunc()
}

// configureAppAuth mints once, synchronously, before serve starts polling:
// an unreadable key or a rejected mint fails startup here, surfacing as a
// restart-policy crash loop instead of a daemon that stays process-healthy
// while every subsequent fetch dies silently.
func configureAppAuth(poller *watcher.Poller, dataDir string) error {
	cfg, err := appAuthFromEnv(dataDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		return nil
	}
	tokenFn, err := cfg.tokenFunc()
	if err != nil {
		return err
	}
	if _, err := tokenFn(); err != nil {
		return fmt.Errorf("mint installation token: %w", err)
	}
	poller.TokenFunc = tokenFn
	return nil
}
