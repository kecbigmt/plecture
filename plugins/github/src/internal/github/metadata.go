package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/tokencommand"
)

// GHClient is the gh-api call surface metadata fetching needs. *ghapi.Client
// satisfies it directly; tests substitute a fake.
type GHClient interface {
	JSON(ctx context.Context, args ...string) ([]byte, error)
}

// PullMeta is the pull request metadata Setup needs: the branch to acquire,
// plus the resource facts a workflow's templates may want to display.
type PullMeta struct {
	HeadRef string
	Title   string
	State   string // lowercase, GitHub's own vocabulary (open/closed)
}

// FetchPullMeta fetches a pull request's head branch, title, and state in one
// REST call — the branch name is derived from the same response setup
// already needs for title/state, rather than a separate `gh pr view` call
// just for the branch. A fetch failure propagates: unlike an issue, a pull
// request resource with no reachable metadata has no branch to acquire.
func FetchPullMeta(ctx context.Context, client GHClient, ownerRepo string, number int) (*PullMeta, error) {
	raw, err := client.JSON(ctx, fmt.Sprintf("repos/%s/pulls/%d", ownerRepo, number))
	if err != nil {
		return nil, fmt.Errorf("fetch pull request metadata: %w", err)
	}
	var meta struct {
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Title string `json:"title"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse pull request metadata: %w", err)
	}
	return &PullMeta{HeadRef: meta.Head.Ref, Title: meta.Title, State: strings.ToLower(meta.State)}, nil
}

// IssueMeta is the issue metadata Setup surfaces as outputs.
type IssueMeta struct {
	Title string
	State string // lowercase; zero value when the issue could not be fetched
}

// FetchIssueMeta fetches an issue's title and state. A fetch failure — the
// issue doesn't exist yet, or the API is unreachable — is tolerated as a
// zero IssueMeta rather than an error: branch work can start before the
// issue exists, matching production's degrade-and-continue behavior. A
// failed token command is not tolerated, since it is a broken credential
// source rather than an absent issue.
func FetchIssueMeta(ctx context.Context, client GHClient, ownerRepo string, number int) (IssueMeta, error) {
	raw, err := client.JSON(ctx, fmt.Sprintf("repos/%s/issues/%d", ownerRepo, number))
	var commandErr *tokencommand.Error
	if errors.As(err, &commandErr) {
		return IssueMeta{}, err
	}
	if err != nil {
		return IssueMeta{}, nil
	}
	var meta struct {
		Title string `json:"title"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return IssueMeta{}, nil
	}
	return IssueMeta{Title: meta.Title, State: strings.ToLower(meta.State)}, nil
}
