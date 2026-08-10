package github

import (
	"encoding/json"
	"strings"
	"testing"
)

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
