package observe

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeGHClient struct {
	responses map[string]string // args (space-joined) -> JSON body
	errs      map[string]error  // args (space-joined, prefix match) -> error
}

func (f *fakeGHClient) JSON(ctx context.Context, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	for prefix, err := range f.errs {
		if strings.HasPrefix(key, prefix) {
			return nil, err
		}
	}
	for prefix, body := range f.responses {
		if strings.HasPrefix(key, prefix) {
			return []byte(body), nil
		}
	}
	return nil, errors.New("fakeGHClient: no response configured for " + key)
}

func TestObserve_Pull_ComputesChecksAndMergeable(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/pulls/44":                  `{"head":{"sha":"abc123"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/abc123/check-runs": `{"check_runs":[{"id":1,"name":"build","conclusion":"success"},{"id":2,"name":"lint","conclusion":null,"status":"in_progress"}]}`,
		"repos/acme/widgets/commits/abc123/status":     `{"statuses":[]}`,
	}}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/pull/44", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.ResourceKind != "pull" || result.Revision != "abc123" || result.MergeableState != "clean" {
		t.Errorf("result = %+v", result)
	}
	if result.ChecksStatus != "PENDING" {
		t.Errorf("checks_status = %q, want PENDING (one run still in progress)", result.ChecksStatus)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/44" {
		t.Errorf("pr_url = %q", result.PRURL)
	}
}

func TestObserve_Pull_MergeableStateMapping(t *testing.T) {
	tests := []struct {
		apiValue string
		want     string
	}{
		{"mergeable", "clean"},
		{"conflicting", "dirty"},
		{"blocked", "blocked"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.apiValue, func(t *testing.T) {
			client := &fakeGHClient{responses: map[string]string{
				"repos/acme/widgets/pulls/1":                 `{"head":{"sha":"sha1"},"mergeable_state":"` + tt.apiValue + `"}`,
				"repos/acme/widgets/commits/sha1/check-runs": `{"check_runs":[]}`,
				"repos/acme/widgets/commits/sha1/status":     `{"statuses":[]}`,
			}}
			result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/pull/1", GHClient: client})
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if result.MergeableState != tt.want {
				t.Errorf("mergeable_state = %q, want %q", result.MergeableState, tt.want)
			}
		})
	}
}

func TestObserve_Pull_ChecksFetchFailurePropagates(t *testing.T) {
	client := &fakeGHClient{
		responses: map[string]string{"repos/acme/widgets/pulls/1": `{"head":{"sha":"sha1"},"mergeable_state":"clean"}`},
		errs:      map[string]error{"repos/acme/widgets/commits/sha1/check-runs": errors.New("HTTP 500")},
	}
	_, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/pull/1", GHClient: client})
	if err == nil {
		t.Fatal("expected a checks-rollup fetch failure to propagate")
	}
}

func TestObserve_Issue_NoLinkedPR(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7": `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?":   `[]`,
		"graphql":                     `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[]}}}}}`,
	}}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.ResourceKind != "issue" || result.IssueStatus != "PENDING" || result.ChecksStatus != "NULL" || result.MergeableState != "NULL" {
		t.Errorf("result = %+v", result)
	}
	if result.Revision != "issue:2026-01-01T00:00:00Z" {
		t.Errorf("revision = %q", result.Revision)
	}
	if result.PRURL != "" {
		t.Errorf("pr_url = %q, want empty", result.PRURL)
	}
}

func TestObserve_Issue_ClosedIssueIsSuccess(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7": `{"state":"closed","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?":   `[]`,
		"graphql":                     `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[]}}}}}`,
	}}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.IssueStatus != "SUCCESS" {
		t.Errorf("issue_status = %q, want SUCCESS", result.IssueStatus)
	}
}

func TestObserve_Issue_FetchFailurePropagates(t *testing.T) {
	client := &fakeGHClient{errs: map[string]error{"repos/acme/widgets/issues/7": errors.New("HTTP 404")}}
	_, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err == nil {
		t.Fatal("expected the issue fetch failure to propagate (unlike provider setup, observe does not tolerate it)")
	}
}

func TestObserve_Issue_LinkedPRViaBranch(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7":                       `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?head=acme%3Aissue%2F7":    `[{"html_url":"https://github.com/acme/widgets/pull/9"}]`,
		"graphql -F owner=acme -F repo=widgets -F number=7": `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[]}}}}}`,
		"repos/acme/widgets/pulls/9":                        `{"head":{"sha":"def456"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/def456/check-runs":      `{"check_runs":[]}`,
		"repos/acme/widgets/commits/def456/status":          `{"statuses":[]}`,
	}}
	result, err := Observe(context.Background(), Options{
		ResourceID:       "https://github.com/acme/widgets/issues/7",
		WorkspaceDirPath: "/roots/wt/issue-7",
		WorkspaceDirBranch: func(ctx context.Context, workspaceDir string) string {
			if workspaceDir != "/roots/wt/issue-7" {
				t.Fatalf("WorkspaceDirBranch called with %q", workspaceDir)
			}
			return "issue/7"
		},
		GHClient: client,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/9" || result.Revision != "def456" || result.ChecksStatus != "NULL" {
		t.Errorf("result = %+v", result)
	}
}

// A '+' in the branch name must not be decoded as a space by GitHub's query parser.
func TestObserve_Issue_LinkedPRViaBranch_EncodesSpecialCharacters(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7":                                       `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?head=acme%3Aissue%2F7%2Bclaude&state=all": `[{"html_url":"https://github.com/acme/widgets/pull/9"}]`,
		"graphql -F owner=acme -F repo=widgets -F number=7":                 `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[]}}}}}`,
		"repos/acme/widgets/pulls/9":                                        `{"head":{"sha":"def456"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/def456/check-runs":                      `{"check_runs":[]}`,
		"repos/acme/widgets/commits/def456/status":                          `{"statuses":[]}`,
	}}
	result, err := Observe(context.Background(), Options{
		ResourceID:       "https://github.com/acme/widgets/issues/7",
		WorkspaceDirPath: "/roots/wt/issue-7",
		WorkspaceDirBranch: func(ctx context.Context, workspaceDir string) string {
			return "issue/7+claude"
		},
		GHClient: client,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/9" || result.Revision != "def456" {
		t.Errorf("result = %+v, want the branch pull request resolved despite the '+' in the branch name", result)
	}
}

func TestObserve_Issue_BranchLookupFailureFallsBackToGraphQL(t *testing.T) {
	client := &fakeGHClient{
		responses: map[string]string{
			"repos/acme/widgets/issues/7": `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
			"graphql": `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[
				{"url":"https://github.com/acme/widgets/pull/12","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z"},
				{"url":"https://github.com/acme/widgets/pull/11","state":"CLOSED","updatedAt":"2026-01-01T00:00:00Z"}
			]}}}}}`,
			"repos/acme/widgets/pulls/12":                  `{"head":{"sha":"ghi789"},"mergeable_state":"dirty"}`,
			"repos/acme/widgets/commits/ghi789/check-runs": `{"check_runs":[]}`,
			"repos/acme/widgets/commits/ghi789/status":     `{"statuses":[]}`,
		},
		errs: map[string]error{"repos/acme/widgets/pulls?": errors.New("HTTP 422")},
	}
	result, err := Observe(context.Background(), Options{
		ResourceID:         "https://github.com/acme/widgets/issues/7",
		WorkspaceDirPath:   "/roots/wt/issue-7",
		WorkspaceDirBranch: func(ctx context.Context, workspaceDir string) string { return "issue/7" },
		GHClient:           client,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/12" {
		t.Errorf("pr_url = %q, want the OPEN PR preferred over the CLOSED one", result.PRURL)
	}
	if result.MergeableState != "dirty" {
		t.Errorf("mergeable_state = %q", result.MergeableState)
	}
}

func TestRollupChecks(t *testing.T) {
	tests := []struct {
		name    string
		entries []checkEntry
		want    string
	}{
		{"no entries", nil, "NULL"},
		{"all success", []checkEntry{{conclusion: "SUCCESS"}, {state: "SUCCESS"}}, "SUCCESS"},
		{"neutral and skipped count as success", []checkEntry{{conclusion: "NEUTRAL"}, {conclusion: "SKIPPED"}}, "SUCCESS"},
		{"one pending wins over success", []checkEntry{{conclusion: "SUCCESS"}, {state: "PENDING"}}, "PENDING"},
		{"one failure wins over pending", []checkEntry{{state: "PENDING"}, {conclusion: "FAILURE"}}, "FAILURE"},
		{"error state counts as failure", []checkEntry{{state: "ERROR"}}, "FAILURE"},
		{"cancelled conclusion counts as failure", []checkEntry{{conclusion: "CANCELLED"}}, "FAILURE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rollupChecks(tt.entries); got != tt.want {
				t.Errorf("rollupChecks(%v) = %q, want %q", tt.entries, got, tt.want)
			}
		})
	}
}

func TestObserve_Pull_ReviewDecision(t *testing.T) {
	tests := []struct {
		name    string
		graphql string
		want    string
	}{
		{
			name:    "approved",
			graphql: `{"data":{"repository":{"pullRequest":{"reviewDecision":"APPROVED"}}}}`,
			want:    "APPROVED",
		},
		{
			name:    "changes requested",
			graphql: `{"data":{"repository":{"pullRequest":{"reviewDecision":"CHANGES_REQUESTED"}}}}`,
			want:    "CHANGES_REQUESTED",
		},
		{
			name:    "no reviewer is required, so GitHub reports none",
			graphql: `{"data":{"repository":{"pullRequest":{"reviewDecision":null}}}}`,
			want:    "NULL",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeGHClient{responses: map[string]string{
				"repos/acme/widgets/pulls/44":                  `{"head":{"sha":"abc123"},"mergeable_state":"clean"}`,
				"repos/acme/widgets/commits/abc123/check-runs": `{"check_runs":[]}`,
				"repos/acme/widgets/commits/abc123/status":     `{"statuses":[]}`,
				"graphql": tt.graphql,
			}}
			result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/pull/44", GHClient: client})
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if result.ReviewDecision != tt.want {
				t.Errorf("review_decision = %q, want %q", result.ReviewDecision, tt.want)
			}
		})
	}
}

// The review decision is an extra GraphQL read on top of the REST reads the
// rest of the observation needs, so a failure there must not cost the caller
// the checks/mergeability state it came for.
func TestObserve_Pull_ReviewDecisionFetchFailureIsTolerated(t *testing.T) {
	client := &fakeGHClient{
		responses: map[string]string{
			"repos/acme/widgets/pulls/44":                  `{"head":{"sha":"abc123"},"mergeable_state":"clean"}`,
			"repos/acme/widgets/commits/abc123/check-runs": `{"check_runs":[{"conclusion":"success"}]}`,
			"repos/acme/widgets/commits/abc123/status":     `{"statuses":[]}`,
		},
		errs: map[string]error{"graphql": errors.New("HTTP 502")},
	}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/pull/44", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.ChecksStatus != "SUCCESS" {
		t.Errorf("checks_status = %q, want the observation to survive", result.ChecksStatus)
	}
	if result.ReviewDecision != "NULL" {
		t.Errorf("review_decision = %q, want NULL on a failed fetch", result.ReviewDecision)
	}
}

func TestObserve_Issue_ReviewDecisionIsNullWithoutALinkedPR(t *testing.T) {
	client := &fakeGHClient{
		responses: map[string]string{
			"repos/acme/widgets/issues/7": `{"state":"open","updated_at":"2026-08-18T00:00:00Z"}`,
			"graphql":                     `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[]}}}}}`,
		},
	}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.ReviewDecision != "NULL" {
		t.Errorf("review_decision = %q, want NULL", result.ReviewDecision)
	}
}

func TestLatestCheckRunPerName(t *testing.T) {
	tests := []struct {
		name string
		runs []checkRun
		want []checkEntry
	}{
		{
			name: "cancelled run superseded by a later success",
			runs: []checkRun{
				{Name: "build", Conclusion: "cancelled", StartedAt: "2026-08-20T10:00:00Z", ID: 1},
				{Name: "build", Conclusion: "success", StartedAt: "2026-08-20T11:00:00Z", ID: 2},
			},
			want: []checkEntry{{conclusion: "SUCCESS"}},
		},
		{
			name: "identical start times fall back to the higher run id",
			runs: []checkRun{
				{Name: "build", Conclusion: "success", StartedAt: "2026-08-20T10:00:00Z", ID: 9},
				{Name: "build", Conclusion: "cancelled", StartedAt: "2026-08-20T10:00:00Z", ID: 4},
			},
			want: []checkEntry{{conclusion: "SUCCESS"}},
		},
		{
			name: "only cancelled runs keep the failing entry",
			runs: []checkRun{
				{Name: "build", Conclusion: "cancelled", StartedAt: "2026-08-20T10:00:00Z", ID: 1},
				{Name: "build", Conclusion: "cancelled", StartedAt: "2026-08-20T11:00:00Z", ID: 2},
			},
			want: []checkEntry{{conclusion: "CANCELLED"}},
		},
		{
			name: "an unfinished latest run reports its status",
			runs: []checkRun{
				{Name: "build", Conclusion: "cancelled", StartedAt: "2026-08-20T10:00:00Z", ID: 1},
				{Name: "build", Status: "in_progress", StartedAt: "2026-08-20T11:00:00Z", ID: 2},
			},
			want: []checkEntry{{state: "IN_PROGRESS"}},
		},
		{
			name: "distinct names are kept independently",
			runs: []checkRun{
				{Name: "build", Conclusion: "success", StartedAt: "2026-08-20T10:00:00Z", ID: 1},
				{Name: "lint", Conclusion: "failure", StartedAt: "2026-08-20T10:00:00Z", ID: 2},
			},
			want: []checkEntry{{conclusion: "SUCCESS"}, {conclusion: "FAILURE"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latestCheckRunPerName(tt.runs)
			if len(got) != len(tt.want) {
				t.Fatalf("latestCheckRunPerName() = %v, want %v", got, tt.want)
			}
			for _, w := range tt.want {
				found := false
				for _, g := range got {
					if g == w {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("latestCheckRunPerName() = %v, missing %v", got, w)
				}
			}
		})
	}
}

func TestObserve_Pull_SupersededCancelledRuns(t *testing.T) {
	tests := []struct {
		name      string
		checkRuns string
		want      string
	}{
		{
			name:      "cancelled run of a superseded workflow run does not fail the rollup",
			checkRuns: `{"check_runs":[{"id":1,"name":"build","status":"completed","conclusion":"cancelled","started_at":"2026-08-20T10:00:00Z"},{"id":2,"name":"build","status":"completed","conclusion":"success","started_at":"2026-08-20T11:00:00Z"}]}`,
			want:      "SUCCESS",
		},
		{
			name:      "latest run failing still fails the rollup",
			checkRuns: `{"check_runs":[{"id":1,"name":"build","status":"completed","conclusion":"success","started_at":"2026-08-20T10:00:00Z"},{"id":2,"name":"build","status":"completed","conclusion":"failure","started_at":"2026-08-20T11:00:00Z"}]}`,
			want:      "FAILURE",
		},
		{
			name:      "a check name with only cancelled runs still fails the rollup",
			checkRuns: `{"check_runs":[{"id":1,"name":"build","status":"completed","conclusion":"success","started_at":"2026-08-20T11:00:00Z"},{"id":2,"name":"lint","status":"completed","conclusion":"cancelled","started_at":"2026-08-20T10:00:00Z"}]}`,
			want:      "FAILURE",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeGHClient{responses: map[string]string{
				"repos/acme/widgets/pulls/44":                  `{"head":{"sha":"abc123"},"mergeable_state":"clean"}`,
				"repos/acme/widgets/commits/abc123/check-runs": tt.checkRuns,
				"repos/acme/widgets/commits/abc123/status":     `{"statuses":[]}`,
			}}
			result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/pull/44", GHClient: client})
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if result.ChecksStatus != tt.want {
				t.Errorf("checks_status = %q, want %q", result.ChecksStatus, tt.want)
			}
		})
	}
}

// A single worktree that builds several stacked branches leaves HEAD on a
// later branch than the one the observed issue's pull request was opened
// from, so the branch fact must not outrank the issue's own closing-PR
// references.
func TestObserve_Issue_BranchOnAnotherStackedPRPrefersClosingReference(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7":                    `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?head=acme%3Aissue%2F8": `[{"html_url":"https://github.com/acme/widgets/pull/20"}]`,
		"graphql -F owner=acme -F repo=widgets -F number=7": `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[
			{"url":"https://github.com/acme/widgets/pull/9","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z"}
		]}}}}}`,
		"repos/acme/widgets/pulls/9":               `{"head":{"sha":"r1"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/r1/check-runs": `{"check_runs":[]}`,
		"repos/acme/widgets/commits/r1/status":     `{"statuses":[]}`,
		"repos/acme/widgets/pulls/20":              `{"head":{"sha":"r2"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/r2/check-runs": `{"check_runs":[]}`,
		"repos/acme/widgets/commits/r2/status":     `{"statuses":[]}`,
	}}
	result, err := Observe(context.Background(), Options{
		ResourceID:         "https://github.com/acme/widgets/issues/7",
		WorkspaceDirPath:   "/roots/wt/stack",
		WorkspaceDirBranch: func(ctx context.Context, workspaceDir string) string { return "issue/8" },
		GHClient:           client,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/9" {
		t.Errorf("pr_url = %q, want the issue's own pull request", result.PRURL)
	}
	if result.Revision != "r1" {
		t.Errorf("revision = %q, want the observed issue's pull request head", result.Revision)
	}
}

// Among several closing references, the branch the session actually checked
// out identifies which pull request is this session's work.
func TestObserve_Issue_BranchPRWinsAmongClosingReferences(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7":                    `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?head=acme%3Aissue%2F7": `[{"html_url":"https://github.com/acme/widgets/pull/9"}]`,
		"graphql -F owner=acme -F repo=widgets -F number=7": `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[
			{"url":"https://github.com/acme/widgets/pull/12","state":"OPEN","updatedAt":"2026-01-03T00:00:00Z"},
			{"url":"https://github.com/acme/widgets/pull/9","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z"}
		]}}}}}`,
		"repos/acme/widgets/pulls/9":               `{"head":{"sha":"r1"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/r1/check-runs": `{"check_runs":[]}`,
		"repos/acme/widgets/commits/r1/status":     `{"statuses":[]}`,
	}}
	result, err := Observe(context.Background(), Options{
		ResourceID:         "https://github.com/acme/widgets/issues/7",
		WorkspaceDirPath:   "/roots/wt/issue-7",
		WorkspaceDirBranch: func(ctx context.Context, workspaceDir string) string { return "issue/7" },
		GHClient:           client,
	})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/9" {
		t.Errorf("pr_url = %q, want the session's own branch pull request", result.PRURL)
	}
}

// A closing-reference lookup that fails leaves the branch pull request
// unvalidated, which in a stacked worktree is exactly the case that reports
// another issue's PR head as this issue's revision.
func TestObserve_Issue_ClosingReferenceLookupFailureWithBranchPRPropagates(t *testing.T) {
	client := &fakeGHClient{
		responses: map[string]string{
			"repos/acme/widgets/issues/7":                    `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
			"repos/acme/widgets/pulls?head=acme%3Aissue%2F8": `[{"html_url":"https://github.com/acme/widgets/pull/20"}]`,
			"repos/acme/widgets/pulls/20":                    `{"head":{"sha":"r2"},"mergeable_state":"clean"}`,
			"repos/acme/widgets/commits/r2/check-runs":       `{"check_runs":[]}`,
			"repos/acme/widgets/commits/r2/status":           `{"statuses":[]}`,
		},
		errs: map[string]error{"graphql -F owner=acme -F repo=widgets -F number=7": errors.New("HTTP 502")},
	}
	_, err := Observe(context.Background(), Options{
		ResourceID:         "https://github.com/acme/widgets/issues/7",
		WorkspaceDirPath:   "/roots/wt/stack",
		WorkspaceDirBranch: func(ctx context.Context, workspaceDir string) string { return "issue/8" },
		GHClient:           client,
	})
	if err == nil {
		t.Fatal("expected an unvalidatable branch pull request to fail the observation rather than be reported as this issue's revision")
	}
}

// Without a branch pull request there is nothing a reference lookup could
// contradict, so its failure keeps degrading to "no linked PR yet".
func TestObserve_Issue_ClosingReferenceLookupFailureWithoutBranchPRDegrades(t *testing.T) {
	client := &fakeGHClient{
		responses: map[string]string{
			"repos/acme/widgets/issues/7": `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		},
		errs: map[string]error{"graphql": errors.New("HTTP 502")},
	}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "" || result.Revision != "issue:2026-01-01T00:00:00Z" {
		t.Errorf("result = %+v, want the issue-only state", result)
	}
}

func TestDocument_PullPublishesNoIssueStatus(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/pulls/44":                  `{"head":{"sha":"abc123"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/abc123/check-runs": `{"check_runs":[]}`,
		"repos/acme/widgets/commits/abc123/status":     `{"statuses":[]}`,
		"graphql": `{"data":{"repository":{"pullRequest":{"reviewDecision":"APPROVED"}}}}`,
	}}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/pull/44", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	doc := result.Document()
	if _, ok := doc["issue_status"]; ok {
		t.Errorf("a pull request has no issue completion to report: %#v", doc)
	}
	for _, key := range []string{"resource_kind", "checks_status", "revision", "pr_url", "mergeable_state", "review_decision"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("%s missing: %#v", key, doc)
		}
	}
	if doc["resource_kind"] != "pull" {
		t.Errorf("resource_kind = %#v", doc["resource_kind"])
	}
}

func TestDocument_IssueOmitsPRURLUntilOneExists(t *testing.T) {
	client := &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7": `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?":   `[]`,
		"graphql":                     `{"data":{"repository":{"issue":{"closedByPullRequestsReferences":{"nodes":[]}}}}}`,
	}}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	doc := result.Document()
	if _, ok := doc["pr_url"]; ok {
		t.Errorf("pr_url present with no linked pull request: %#v", doc)
	}
	for _, key := range []string{"resource_kind", "issue_status", "checks_status", "revision", "mergeable_state", "review_decision"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("%s missing: %#v", key, doc)
		}
	}
}

func TestObserve_RejectsAnIdentifierNeitherObserverRecognizes(t *testing.T) {
	_, err := Observe(context.Background(), Options{ResourceID: "https://gitlab.com/acme/widgets/issues/1"})
	if err == nil {
		t.Fatal("an identifier that is neither an issue nor a pull request has no state to report, so the observation fails")
	}
}
