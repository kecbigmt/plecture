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

func TestObserve_UnknownResourceKind(t *testing.T) {
	result, err := Observe(context.Background(), Options{ResourceID: "https://gitlab.com/acme/widgets/issues/1"})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.ResourceKind != "unknown" || result.ChecksStatus != "NULL" || result.IssueStatus != "NULL" || result.MergeableState != "NULL" {
		t.Errorf("result = %+v", result)
	}
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
	if result.ResourceKind != "pull" || result.Revision != "abc123" || result.MergeableState != "clean" || result.IssueStatus != "NULL" {
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
		"repos/acme/widgets/issues/7":                  `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		"repos/acme/widgets/pulls?head=acme:issue/7":   `[{"html_url":"https://github.com/acme/widgets/pull/9"}]`,
		"repos/acme/widgets/pulls/9":                   `{"head":{"sha":"def456"},"mergeable_state":"clean"}`,
		"repos/acme/widgets/commits/def456/check-runs": `{"check_runs":[]}`,
		"repos/acme/widgets/commits/def456/status":     `{"statuses":[]}`,
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
