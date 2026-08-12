package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cradel-dev/cradel/plugins/github-provider/internal/procexec"
)

// projectItemResponse is the GraphQL response structure for resolving a project item.
type projectItemResponse struct {
	Data struct {
		Node struct {
			Type    string `json:"type"`
			Content struct {
				Number     int    `json:"number"`
				URL        string `json:"url"`
				Repository struct {
					NameWithOwner string `json:"nameWithOwner"`
				} `json:"repository"`
			} `json:"content"`
		} `json:"node"`
	} `json:"data"`
}

// ResolveProjectItemID resolves a GitHub Projects v2 item ID (PVTI_xxx) to a
// ParsedURL by querying the item's content via `gh api graphql`.
// This requires a token with repo permissions (the standard gh CLI token).
// ctx bounds the `gh` invocation: a cancelled or expired ctx terminates the
// process and ResolveProjectItemID returns its error.
func ResolveProjectItemID(ctx context.Context, itemID string) (*ParsedURL, error) {
	const query = `query($id: ID!) {
		node(id: $id) {
			... on ProjectV2Item {
				type
				content {
					... on Issue {
						number
						url
						repository { nameWithOwner }
					}
					... on PullRequest {
						number
						url
						repository { nameWithOwner }
					}
				}
			}
		}
	}`

	out, stderr, err := procexec.Default.Run(ctx, "", false, "gh", "api", "graphql",
		"-f", fmt.Sprintf("query=%s", query),
		"-f", fmt.Sprintf("id=%s", itemID),
	)
	if err != nil {
		if len(stderr) > 0 {
			return nil, fmt.Errorf("failed to resolve project item %s: %s", itemID, string(stderr))
		}
		return nil, fmt.Errorf("failed to resolve project item %s: %w", itemID, err)
	}

	var resp projectItemResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse GraphQL response for %s: %w", itemID, err)
	}

	return parseProjectItemResponse(itemID, &resp)
}

// parseProjectItemResponse converts a GraphQL response into a ParsedURL.
func parseProjectItemResponse(itemID string, resp *projectItemResponse) (*ParsedURL, error) {
	node := resp.Data.Node

	var urlType URLType
	switch node.Type {
	case "PULL_REQUEST":
		urlType = URLTypePR
	case "ISSUE":
		urlType = URLTypeIssue
	case "DRAFT_ISSUE":
		return nil, fmt.Errorf("project item %s is a draft issue and cannot be used with sennit create", itemID)
	default:
		return nil, fmt.Errorf("project item %s has unknown type: %s", itemID, node.Type)
	}

	if node.Content.URL == "" {
		return nil, fmt.Errorf("project item %s has no linked content (type: %s)", itemID, node.Type)
	}

	parts := strings.SplitN(node.Content.Repository.NameWithOwner, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repository name: %s", node.Content.Repository.NameWithOwner)
	}

	return &ParsedURL{
		Type:      urlType,
		Owner:     parts[0],
		Repo:      parts[1],
		OwnerRepo: node.Content.Repository.NameWithOwner,
		Number:    node.Content.Number,
	}, nil
}
