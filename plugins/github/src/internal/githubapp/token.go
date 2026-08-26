package githubapp

import (
	"crypto/rsa"
	"net/http"
	"time"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/apptoken"
)

// Token is the single mint-or-reuse path shared by every App-auth caller
// (gh-app-token's own CLI, github-watcher's App auth) pointed at the same
// cache file, so the owner/repo resolution and cache-miss ordering exists
// once instead of being reimplemented per caller.
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
