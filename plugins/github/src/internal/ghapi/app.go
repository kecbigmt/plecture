package ghapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/apptoken"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/githubapp"
)

// defaultAppSkew matches gh-app-token's own default, so a metadata fetch and
// a git operation refresh at the same point in a token's lifetime even
// though each mints/reuses independently.
const defaultAppSkew = 5 * time.Minute

// App calls the GitHub REST API directly over HTTP, authenticated as a
// GitHub App installation instead of through `gh`. It mints (or reuses a
// cached, unexpired) installation token via the same apptoken.Cache the
// git credential helper's App auth path uses (see cmd/gh-app-token), so
// pointing both at the same CachePath makes them share one token rather
// than each minting its own — while neither depends on the other's timing,
// since apptoken.Cache mints again on its own the moment its holder's copy
// is stale.
type App struct {
	AppID          string
	InstallationID string // optional; resolved from Owner/Repo when empty
	Owner          string
	Repo           string
	PrivateKeyPath string
	CachePath      string
	BaseURL        string        // defaults to githubapp.DefaultBaseURL
	Skew           time.Duration // defaults to defaultAppSkew
	HTTPClient     *http.Client  // defaults to a client with a 30s timeout
}

// JSON fetches one REST endpoint via GET — the only shape
// official.github.worktree's metadata fetch needs — and returns its JSON
// body.
func (a *App) JSON(ctx context.Context, args ...string) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("github app client supports exactly one REST path argument, got %d", len(args))
	}

	key, err := githubapp.LoadPrivateKey(a.PrivateKeyPath)
	if err != nil {
		return nil, err
	}
	client := a.httpClient()
	baseURL := a.baseURL()
	token, err := githubapp.Token(apptoken.NewCache(a.CachePath), a.skew(), client, baseURL, a.AppID, key, a.InstallationID, a.Owner, a.Repo)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(args[0], "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build github api request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github api %s: %w", args[0], err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read github api response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github api %s: %s", args[0], githubapp.APIErrorMessage(resp.StatusCode, body))
	}
	return body, nil
}

func (a *App) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (a *App) baseURL() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return githubapp.DefaultBaseURL
}

func (a *App) skew() time.Duration {
	if a.Skew != 0 {
		return a.Skew
	}
	return defaultAppSkew
}
