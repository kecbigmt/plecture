package github

import (
	"encoding/json"
	"strings"
	"testing"
)

// resolve_test.go additions below intentionally shell out to the real gh
// binary against a nonexistent item ID: no fixture can be authoritative on
// exactly which error path gh's `api graphql` takes when unauthenticated
// versus when the ID simply doesn't resolve, so the test only pins the
// wrapping message ResolveProjectItemID adds, not gh's own output.

func TestParseProjectItemResponse(t *testing.T) {
	tests := []struct {
		name    string
		itemID  string
		json    string
		want    *ParsedURL
		wantErr string
	}{
		{
			name:   "issue content",
			itemID: "PVTI_abc",
			json: `{
				"data": {
					"node": {
						"type": "ISSUE",
						"content": {
							"number": 201,
							"url": "https://github.com/example/repo/issues/201",
							"repository": {"nameWithOwner": "acme/widgets"}
						}
					}
				}
			}`,
			want: &ParsedURL{
				Type:      URLTypeIssue,
				Owner:     "acme",
				Repo:      "widgets",
				OwnerRepo: "acme/widgets",
				Number:    201,
			},
		},
		{
			name:   "pull request content",
			itemID: "PVTI_xyz",
			json: `{
				"data": {
					"node": {
						"type": "PULL_REQUEST",
						"content": {
							"number": 50,
							"url": "https://github.com/org/repo/pull/50",
							"repository": {"nameWithOwner": "org/repo"}
						}
					}
				}
			}`,
			want: &ParsedURL{
				Type:      URLTypePR,
				Owner:     "org",
				Repo:      "repo",
				OwnerRepo: "org/repo",
				Number:    50,
			},
		},
		{
			name:   "draft issue",
			itemID: "PVTI_draft",
			json: `{
				"data": {
					"node": {
						"type": "DRAFT_ISSUE",
						"content": {}
					}
				}
			}`,
			wantErr: "draft issue",
		},
		{
			name:   "null content",
			itemID: "PVTI_null",
			json: `{
				"data": {
					"node": {
						"type": "ISSUE",
						"content": {}
					}
				}
			}`,
			wantErr: "has no linked content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp projectItemResponse
			if err := json.Unmarshal([]byte(tt.json), &resp); err != nil {
				t.Fatalf("failed to unmarshal test JSON: %v", err)
			}

			got, err := parseProjectItemResponse(tt.itemID, &resp)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if got.Owner != tt.want.Owner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.want.Owner)
			}
			if got.Repo != tt.want.Repo {
				t.Errorf("Repo = %q, want %q", got.Repo, tt.want.Repo)
			}
			if got.OwnerRepo != tt.want.OwnerRepo {
				t.Errorf("OwnerRepo = %q, want %q", got.OwnerRepo, tt.want.OwnerRepo)
			}
			if got.Number != tt.want.Number {
				t.Errorf("Number = %d, want %d", got.Number, tt.want.Number)
			}
		})
	}
}

// TestResolveProjectItemID_GhFailureWrapsError characterizes the current gap:
// a failing/hung `gh api graphql` call has no way to be cancelled today
// because ResolveProjectItemID takes no context. It also pins the
// error-wrapping message callers currently depend on.
func TestResolveProjectItemID_GhFailureWrapsError(t *testing.T) {
	_, err := ResolveProjectItemID("PVTI_this_id_does_not_exist")
	if err == nil {
		t.Fatal("expected error for a nonexistent project item, got nil")
	}
	if !strings.Contains(err.Error(), "failed to resolve project item") {
		t.Errorf("error = %v, want it to contain %q", err, "failed to resolve project item")
	}
}
