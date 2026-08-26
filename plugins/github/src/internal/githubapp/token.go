package githubapp

import (
	"crypto/rsa"
	"net/http"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/apptoken"
)

// Token mints (or reuses a cached, unexpired) GitHub App installation access
// token: cache.Get decides whether the cached value still has more than skew
// left before expiry, and only on a miss does the closure here resolve
// installationID from owner/repo (when installationID is empty) and mint a
// fresh one. Every caller that mints an installation token — gh-app-token's
// own CLI and any other App-auth-aware caller pointed at the same cache
// file — goes through this one mint-or-reuse path instead of each
// reimplementing the owner/repo resolution and cache-miss ordering.
func Token(cache *apptoken.Cache, skew time.Duration, client *http.Client, baseURL, appID string, key *rsa.PrivateKey, installationID, owner, repo string) (string, error) {
	resolvedInstallationID := installationID
	return cache.Get(skew, func() (string, time.Time, error) {
		jwt, err := BuildJWT(appID, key, time.Now())
		if err != nil {
			return "", time.Time{}, err
		}
		if resolvedInstallationID == "" {
			id, err := ResolveInstallationID(client, baseURL, jwt, owner, repo)
			if err != nil {
				return "", time.Time{}, err
			}
			resolvedInstallationID = id
		}
		minted, err := MintInstallationToken(client, baseURL, jwt, resolvedInstallationID)
		if err != nil {
			return "", time.Time{}, err
		}
		return minted.Token, minted.ExpiresAt, nil
	})
}
