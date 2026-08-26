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

// Deployment-level GitHub App auth env vars. This is the single
// installation opted into per issue #337: one App id, one installation
// (direct or resolved from owner/repo), backing both `serve`'s poll loop
// and the `gh-api` subcommand, since a `gh-api` invocation is a separate
// process from the daemon and can't receive its flags — env is the only
// channel both share. Unset GITHUB_WATCHER_APP_ID means "no App auth
// configured", preserving today's ambient `gh auth` behavior byte-for-byte.
const (
	envAppID          = "GITHUB_WATCHER_APP_ID"
	envInstallationID = "GITHUB_WATCHER_APP_INSTALLATION_ID"
	envOwner          = "GITHUB_WATCHER_APP_OWNER"
	envRepo           = "GITHUB_WATCHER_APP_REPO"
	envPrivateKeyPath = "GITHUB_WATCHER_APP_PRIVATE_KEY_PATH"
	envBaseURL        = "GITHUB_WATCHER_APP_BASE_URL"
	envCachePath      = "GITHUB_WATCHER_APP_CACHE_PATH"
)

// appAuthConfig is one deployment-level App-auth configuration, validated
// enough to attempt a mint (the mint itself may still fail on a bad key or
// installation — see tokenFunc).
type appAuthConfig struct {
	appID, installationID, owner, repo, privateKeyPath, baseURL, cachePath string
}

// appAuthFromEnv reads appAuthConfig from the env vars above. It returns
// (nil, nil) when GITHUB_WATCHER_APP_ID is unset — the ambient-auth default
// — and a descriptive error the moment App auth is partially configured
// (an id with no key, or no way to identify an installation), so a
// misconfigured deployment fails at startup rather than mint its way into a
// confusing runtime error.
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
		// Sharing ghAPIDataDir's default keeps the token cache alongside the
		// rate-budget file, so serve and gh-api locate the same cache the
		// same way they already locate the same budget.
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

// tokenFunc loads the App private key eagerly — a missing or unreadable key
// fails right here, once, rather than silently on the first mint attempt —
// and returns a closure that mints (or reuses a cached, unexpired) token on
// every call via the shared apptoken.Cache, same as gh_app_guard and the
// worktree git credential helper.
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

// appAuthTokenFunc is `gh-api`'s entry point: build a token func from env,
// or (nil, nil) when App auth isn't configured at all. Unlike serve's
// configureAppAuth, it does not mint eagerly — a one-shot `gh-api`
// invocation fails loud on its own the moment the mint inside the returned
// func fails, with no separate startup phase to fail during.
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

// configureAppAuth is `serve`'s entry point: when App auth is configured, it
// wires poller.TokenFunc and mints once, synchronously, before serve ever
// starts polling — an unreadable key or a rejected mint fails startup here
// (main.go exits non-zero, and the service's restart policy surfaces the
// crash loop as unhealthy) instead of leaving the daemon process-healthy
// while every subsequent fetch dies. With App auth unconfigured, this is a
// no-op: poller.TokenFunc stays nil and polling keeps using ambient
// `gh auth`.
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
