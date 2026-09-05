package pullquery

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
)

// pollPageSize is the REST list-pull-requests page size Poll requests;
// GitHub's own default is 30, and a full page is Poll's only pagination
// signal since the endpoint reports no total count.
const pollPageSize = 100

// restPull is one entry from GitHub's REST "List pull requests" response,
// carrying every field Matches needs without a second per-PR fetch.
type restPull struct {
	HTMLURL string      `json:"html_url"`
	State   string      `json:"state"`
	Draft   bool        `json:"draft"`
	Labels  []restLabel `json:"labels"`
}

type restLabel struct {
	Name string `json:"name"`
}

func labelNames(labels []restLabel) []string {
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return names
}

// Poll runs the query's complete-membership means: one REST list-pulls scan
// per requested repository, paginated to exhaustion, filtered by Matches. A
// single fetch or parse failure fails the whole snapshot — the ADR's poll
// contract makes successful exit the only way to assert either presence or
// absence, so a partial result must never reach the caller.
func Poll(ctx context.Context, client github.GHClient, in Inputs) ([]Item, error) {
	if len(in.Repositories) == 0 {
		return nil, fmt.Errorf("query requires at least one repository")
	}
	if err := ValidateState(in.State); err != nil {
		return nil, err
	}

	items := make([]Item, 0)
	for _, ownerRepo := range in.Repositories {
		owner, repo, ok := splitOwnerRepo(ownerRepo)
		if !ok {
			return nil, fmt.Errorf("invalid repository %q, want \"owner/repo\"", ownerRepo)
		}
		matched, err := pollRepo(ctx, client, owner, repo, in)
		if err != nil {
			return nil, err
		}
		items = append(items, matched...)
	}
	return items, nil
}

func pollRepo(ctx context.Context, client github.GHClient, owner, repo string, in Inputs) ([]Item, error) {
	ownerRepo := owner + "/" + repo
	var items []Item
	for page := 1; ; page++ {
		path := fmt.Sprintf("repos/%s/%s/pulls?state=%s&per_page=%d&page=%d", owner, repo, in.State, pollPageSize, page)
		raw, err := client.JSON(ctx, path)
		if err != nil {
			return nil, fmt.Errorf("list pull requests for %s: %w", ownerRepo, err)
		}
		var pulls []restPull
		if err := json.Unmarshal(raw, &pulls); err != nil {
			return nil, fmt.Errorf("parse pull requests for %s: %w", ownerRepo, err)
		}
		for _, p := range pulls {
			fact := PullFact{URL: p.HTMLURL, Owner: owner, Repo: repo, State: p.State, Draft: p.Draft, Labels: labelNames(p.Labels)}
			if Matches(fact, in) {
				items = append(items, fact.Item())
			}
		}
		if len(pulls) < pollPageSize {
			return items, nil
		}
	}
}
