// Package pullquery implements the pull resource observer's two query
// means: a complete search snapshot and a webhook-driven appearance
// stream. Both filter on one Inputs shape and produce one Item shape,
// carrying identity and appearance context only — never an observed
// resource-state fact.
package pullquery

import (
	"fmt"
	"strings"
)

// Inputs selects which pull requests match: State is "open", "closed", or
// "all"; Draft must equal a matching pull request's own draft flag exactly.
type Inputs struct {
	Repositories []string
	Labels       []string
	State        string
	Draft        bool
}

// Item is one query result: identity plus appearance context. Owner and
// Repository are optional; Resource is the only required property.
type Item struct {
	Resource   string `json:"resource"`
	Owner      string `json:"owner,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// ItemSchemaRequired and ItemSchemaProperties list Item's properties for
// the self-test that checks them against pull.toml's state_schema.
var (
	ItemSchemaRequired   = []string{"resource"}
	ItemSchemaProperties = []string{"resource", "owner", "repository"}
)

// validStates matches GitHub's REST list-pull-requests `state` values, so
// Poll can pass a validated value straight through.
var validStates = map[string]bool{"open": true, "closed": true, "all": true}

func ValidateState(state string) error {
	if !validStates[state] {
		return fmt.Errorf("invalid state %q: want \"open\", \"closed\", or \"all\"", state)
	}
	return nil
}

// PullFact is one pull request's identity plus the fields Matches filters
// on, independent of whether it came from a REST list-pulls page or a
// webhook delivery.
type PullFact struct {
	URL    string
	Owner  string
	Repo   string
	State  string
	Draft  bool
	Labels []string
}

func (p PullFact) Item() Item {
	return Item{Resource: p.URL, Owner: p.Owner, Repository: p.Repo}
}

// Matches requires every requested label present, matching GitHub's own
// multi-label search AND semantics.
func Matches(pr PullFact, in Inputs) bool {
	if len(in.Repositories) > 0 && !contains(in.Repositories, pr.Owner+"/"+pr.Repo) {
		return false
	}
	if in.State != "" && in.State != "all" && pr.State != in.State {
		return false
	}
	if pr.Draft != in.Draft {
		return false
	}
	for _, want := range in.Labels {
		if !contains(pr.Labels, want) {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// splitOwnerRepo splits an "owner/repo" string, rejecting anything that
// isn't exactly two non-empty segments.
func splitOwnerRepo(ownerRepo string) (owner, repo string, ok bool) {
	parts := strings.Split(ownerRepo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
