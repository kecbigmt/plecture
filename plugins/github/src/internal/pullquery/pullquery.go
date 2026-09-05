// Package pullquery implements the query means the GitHub plugin's pull
// resource observer declares: poll's complete search snapshot and
// subscribe's webhook-driven appearance stream. See
// docs/adr/2026-09-05-standing-session-dispatch.md, "Query contract: one
// purpose with poll and subscribe means" — both means share one Inputs
// filter and one Item identity/appearance-context shape, and neither
// publishes a resource-state fact: that stays observe's sole authority.
package pullquery

import (
	"fmt"
	"strings"
)

// Inputs is the query's shared inputs_schema: which pull requests match.
// State is one of "open", "closed", or "all"; Draft must equal a matching
// pull request's own draft flag exactly.
type Inputs struct {
	Repositories []string
	Labels       []string
	State        string
	Draft        bool
}

// Item is the query's shared item_schema: identity plus appearance context.
// Owner and Repository are optional identity decomposition; Resource is the
// query's only required property.
type Item struct {
	Resource   string `json:"resource"`
	Owner      string `json:"owner,omitempty"`
	Repository string `json:"repository,omitempty"`
}

// ItemSchemaRequired and ItemSchemaProperties mirror the ADR's
// [pull.query.item_schema] table in Go so the plugin's self-test can assert
// this contract never grows a state_schema key — pull.toml's state_schema
// stays the sole authority on a pull request's observed state, per the
// ADR's observe/query boundary rule.
var (
	ItemSchemaRequired   = []string{"resource"}
	ItemSchemaProperties = []string{"resource", "owner", "repository"}
)

// InputsSchemaProperties mirrors the ADR's [pull.query.inputs_schema]
// table, all required.
var InputsSchemaProperties = []string{"repositories", "labels", "state", "draft"}

// validStates are the query's only accepted `state` values, matching
// GitHub's own REST list-pull-requests vocabulary exactly so Poll passes it
// straight through.
var validStates = map[string]bool{"open": true, "closed": true, "all": true}

// ValidateState reports whether state is one of the query's accepted
// values.
func ValidateState(state string) error {
	if !validStates[state] {
		return fmt.Errorf("invalid state %q: want \"open\", \"closed\", or \"all\"", state)
	}
	return nil
}

// PullFact is one pull request's identity plus the fields Matches filters
// on, independent of where it came from: a REST list-pulls page for poll, or
// a webhook delivery for subscribe.
type PullFact struct {
	URL    string
	Owner  string
	Repo   string
	State  string
	Draft  bool
	Labels []string
}

// Item projects a PullFact to the query's shared item shape.
func (p PullFact) Item() Item {
	return Item{Resource: p.URL, Owner: p.Owner, Repository: p.Repo}
}

// Matches applies the query's shared filter: the pull request's repository
// must be one of the requested ones (when any are requested), its state
// must agree unless "all" was requested, its draft flag must equal the
// requested one exactly, and every requested label must be present — the
// same AND semantics GitHub's own multi-label search uses.
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
