package pullquery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// VerifySignature checks GitHub's HMAC-SHA256 webhook signature
// (`X-Hub-Signature-256: sha256=<hex>`) over the raw request body. It never
// trusts the caller's own claim of a match: a missing secret or malformed
// header fails closed rather than treating "nothing to check against" as
// verified. Comparison is constant-time so a rejection never leaks how many
// leading bytes matched.
func VerifySignature(secret string, body []byte, header string) bool {
	const prefix = "sha256="
	if secret == "" || !strings.HasPrefix(header, prefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// pullRequestEvent is the subset of GitHub's "pull_request" webhook payload
// MatchPullRequestEvent needs.
type pullRequestEvent struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest struct {
		HTMLURL string      `json:"html_url"`
		State   string      `json:"state"`
		Draft   bool        `json:"draft"`
		Labels  []restLabel `json:"labels"`
	} `json:"pull_request"`
}

// MatchPullRequestEvent decodes one already-signature-verified webhook
// delivery body and reports the query item it produces, when the pull
// request's state at delivery time matches the query's shared filter. A
// delivery for a repository this query doesn't track, or whose current
// state doesn't match, produces no item and no error: subscribe only ever
// answers "did this one resource just match", never "is it now absent" —
// that authority stays with Poll.
func MatchPullRequestEvent(body []byte, in Inputs) (Item, bool, error) {
	var ev pullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return Item{}, false, fmt.Errorf("parse pull_request webhook payload: %w", err)
	}
	owner, repo, ok := splitOwnerRepo(ev.Repository.FullName)
	if !ok {
		return Item{}, false, nil
	}
	fact := PullFact{
		URL:    ev.PullRequest.HTMLURL,
		Owner:  owner,
		Repo:   repo,
		State:  ev.PullRequest.State,
		Draft:  ev.PullRequest.Draft,
		Labels: labelNames(ev.PullRequest.Labels),
	}
	if !Matches(fact, in) {
		return Item{}, false, nil
	}
	return fact.Item(), true, nil
}
