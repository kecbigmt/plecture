package observe

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

func TestObserve_Issue_LinkedPRViaClosingReferencePrefersOpen(t *testing.T) {
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
	}
	result, err := Observe(context.Background(), Options{
		ResourceID: "https://github.com/acme/widgets/issues/7",
		GHClient:   client,
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

func TestObserve_Issue_ClosingReferenceLookupFailureDegradesToIssueOnlyState(t *testing.T) {
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

func linkedPRFixture(discoveryKey, discovery string) *fakeGHClient {
	return &fakeGHClient{responses: map[string]string{
		"repos/acme/widgets/issues/7":                    `{"state":"open","updated_at":"2026-01-01T00:00:00Z"}`,
		discoveryKey:                                     discovery,
		"repos/acme/widgets/pulls/12":                    `{"head":{"sha":"stacked1"},"mergeable_state":"blocked"}`,
		"repos/acme/widgets/commits/stacked1/check-runs": `{"check_runs":[{"id":1,"name":"build","conclusion":"success"}]}`,
		"repos/acme/widgets/commits/stacked1/status":     `{"statuses":[]}`,
	}}
}

func TestObserve_Issue_LinkedPRTargetingANonDefaultBranchThatNamesTheIssue(t *testing.T) {
	client := linkedPRFixture("graphql", `{"data":{"repository":{
		"issue":{
			"closedByPullRequestsReferences":{"nodes":[]},
			"timelineItems":{"nodes":[
				{"source":{}},
				{"source":{"number":12,"url":"https://github.com/acme/widgets/pull/12","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z","body":"Stacked on #11.\n\nCloses #7","repository":{"nameWithOwner":"acme/widgets"}}}
			]}
		},
		"pullRequests":{"nodes":[]}
	}}}`)
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/12" {
		t.Fatalf("pr_url = %q, want the stacked PR that names the issue", result.PRURL)
	}
	if result.Revision != "stacked1" || result.ChecksStatus != "SUCCESS" || result.MergeableState != "blocked" {
		t.Errorf("result = %+v, want revision, checks and mergeability from the stacked PR", result)
	}
}

func TestObserve_Issue_LinkedPROnTheIssueBranchWithoutNamingTheIssue(t *testing.T) {
	client := linkedPRFixture("graphql -F owner=acme -F repo=widgets -F number=7 -f branch=issue/7", `{"data":{"repository":{
		"issue":{"closedByPullRequestsReferences":{"nodes":[]},"timelineItems":{"nodes":[]}},
		"pullRequests":{"nodes":[
			{"number":13,"url":"https://github.com/acme/widgets/pull/13","state":"OPEN","updatedAt":"2026-01-03T00:00:00Z","isCrossRepository":true},
			{"number":12,"url":"https://github.com/acme/widgets/pull/12","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z","isCrossRepository":false}
		]}
	}}}`)
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "https://github.com/acme/widgets/pull/12" || result.Revision != "stacked1" {
		t.Errorf("result = %+v, want the same-repository PR on the issue branch, not the fork's", result)
	}
}

func TestObserve_Issue_CrossReferenceThatDoesNotCloseTheIssueIsNotLinked(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		repo   string
		reason string
	}{
		{"mention without a closing keyword", "Related to #7.", "acme/widgets", "a mere mention is not a link"},
		{"closes a different issue", "Closes #70", "acme/widgets", "#70 is not #7"},
		{"closes the same number in another repository", "Closes other/widgets#7", "acme/widgets", "other/widgets#7 is not this issue"},
		{"PR in another repository", "Closes acme/widgets#7", "acme/gadgets", "its number is not a pull request of this repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := linkedPRFixture("graphql", `{"data":{"repository":{
				"issue":{
					"closedByPullRequestsReferences":{"nodes":[]},
					"timelineItems":{"nodes":[
						{"source":{"number":12,"url":"https://github.com/`+tt.repo+`/pull/12","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z","body":"`+tt.body+`","repository":{"nameWithOwner":"`+tt.repo+`"}}}
					]}
				},
				"pullRequests":{"nodes":[]}
			}}}`)
			result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if result.PRURL != "" {
				t.Errorf("pr_url = %q, want none: %s", result.PRURL, tt.reason)
			}
		})
	}
}

func TestNamesIssueAsClosed(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{"Closes #7", true},
		{"closes #7.", true},
		{"Fixes: #7", true},
		{"This PR resolved acme/widgets#7", true},
		{"FIXED ACME/Widgets#7", true},
		{"Close https://github.com/acme/widgets/issues/7", true},
		{"Summary\n\n- Resolves #7\n", true},
		{"Closes #70", false},
		{"Closes #7a", false},
		{"Closes https://github.com/acme/widgets/issues/70", false},
		{"Closes other/widgets#7", false},
		{"See #7", false},
		{"Unclosed #7", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			if got := namesIssueAsClosed(tt.body, "acme", "widgets", 7); got != tt.want {
				t.Errorf("namesIssueAsClosed(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}

func TestChooseLinkedPR(t *testing.T) {
	pr := func(number int, state, updatedAt string, source linkSource) linkCandidate {
		return linkCandidate{URL: fmt.Sprintf("https://github.com/acme/widgets/pull/%d", number), Number: number, State: state, UpdatedAt: updatedAt, source: source}
	}
	tests := []struct {
		name       string
		candidates []linkCandidate
		want       int // 0 means no PR
	}{
		{"no candidates", nil, 0},
		{"single closing reference", []linkCandidate{pr(5, "MERGED", "2026-01-01T00:00:00Z", linkClosingReference)}, 5},
		{"an open PR beats a more recently updated closed one", []linkCandidate{
			pr(5, "CLOSED", "2026-01-09T00:00:00Z", linkClosingReference),
			pr(6, "OPEN", "2026-01-01T00:00:00Z", linkNamesIssue),
		}, 6},
		{"among open PRs a closing reference beats one that only names the issue", []linkCandidate{
			pr(6, "OPEN", "2026-01-09T00:00:00Z", linkNamesIssue),
			pr(5, "OPEN", "2026-01-01T00:00:00Z", linkClosingReference),
		}, 5},
		{"naming the issue beats only sharing the issue branch", []linkCandidate{
			pr(6, "OPEN", "2026-01-09T00:00:00Z", linkIssueBranch),
			pr(5, "OPEN", "2026-01-01T00:00:00Z", linkNamesIssue),
		}, 5},
		{"within one source the most recently updated wins", []linkCandidate{
			pr(5, "OPEN", "2026-01-01T00:00:00Z", linkNamesIssue),
			pr(6, "OPEN", "2026-01-02T00:00:00Z", linkNamesIssue),
		}, 6},
		{"a tie on everything else goes to the higher number", []linkCandidate{
			pr(6, "OPEN", "2026-01-01T00:00:00Z", linkNamesIssue),
			pr(7, "OPEN", "2026-01-01T00:00:00Z", linkNamesIssue),
		}, 7},
		{"with no open PR the source still ranks first", []linkCandidate{
			pr(5, "MERGED", "2026-01-01T00:00:00Z", linkClosingReference),
			pr(6, "CLOSED", "2026-01-02T00:00:00Z", linkNamesIssue),
		}, 5},
		{"with no open PR one source falls back to the most recently updated", []linkCandidate{
			pr(5, "MERGED", "2026-01-02T00:00:00Z", linkClosingReference),
			pr(6, "CLOSED", "2026-01-01T00:00:00Z", linkClosingReference),
		}, 5},
		{"a PR found by several sources ranks by its strongest", []linkCandidate{
			pr(6, "OPEN", "2026-01-09T00:00:00Z", linkIssueBranch),
			pr(5, "OPEN", "2026-01-01T00:00:00Z", linkNamesIssue),
			pr(6, "OPEN", "2026-01-09T00:00:00Z", linkClosingReference),
		}, 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chooseLinkedPR(tt.candidates)
			want := 0
			if got != nil {
				want = got.Number
			}
			if want != tt.want {
				t.Errorf("chooseLinkedPR = PR %d, want PR %d", want, tt.want)
			}
			reversed := append([]linkCandidate(nil), tt.candidates...)
			slices.Reverse(reversed)
			if again := chooseLinkedPR(reversed); (again == nil) != (got == nil) || (again != nil && again.Number != got.Number) {
				t.Errorf("chooseLinkedPR depends on candidate order")
			}
		})
	}
}

const (
	firstPageKey         = "graphql -F owner=acme -F repo=widgets -F number=7 -f branch=issue/7 -F closing=true -F timeline=true -F branches=true -f query="
	timelinePageTwoKey   = "graphql -F owner=acme -F repo=widgets -F number=7 -f branch=issue/7 -F closing=false -F timeline=true -F branches=false -f timelineAfter=T1 -f query="
	branchesPageTwoKey   = "graphql -F owner=acme -F repo=widgets -F number=7 -f branch=issue/7 -F closing=false -F timeline=false -F branches=true -f branchAfter=B1 -f query="
	staleClosedCandidate = `{"number":5,"url":"https://github.com/acme/widgets/pull/5","state":"CLOSED","updatedAt":"2026-01-01T00:00:00Z","body":"Closes #7","repository":{"nameWithOwner":"acme/widgets"},"isCrossRepository":false}`
)

func TestObserve_Issue_LinkedPRDiscoveryReadsEveryPage(t *testing.T) {
	tests := []struct {
		name     string
		firstTL  string
		firstBR  string
		pageKey  string
		pageBody string
	}{
		{
			name:     "cross references",
			firstTL:  `{"pageInfo":{"hasNextPage":true,"endCursor":"T1"},"nodes":[{"source":` + staleClosedCandidate + `}]}`,
			firstBR:  `{"pageInfo":{"hasNextPage":false},"nodes":[]}`,
			pageKey:  timelinePageTwoKey,
			pageBody: `{"data":{"repository":{"issue":{"timelineItems":{"pageInfo":{"hasNextPage":false},"nodes":[{"source":{"number":12,"url":"https://github.com/acme/widgets/pull/12","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z","body":"Closes #7","repository":{"nameWithOwner":"acme/widgets"}}}]}}}}}`,
		},
		{
			name:     "issue branch pull requests",
			firstTL:  `{"pageInfo":{"hasNextPage":false},"nodes":[]}`,
			firstBR:  `{"pageInfo":{"hasNextPage":true,"endCursor":"B1"},"nodes":[` + staleClosedCandidate + `]}`,
			pageKey:  branchesPageTwoKey,
			pageBody: `{"data":{"repository":{"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[{"number":12,"url":"https://github.com/acme/widgets/pull/12","state":"OPEN","updatedAt":"2026-01-02T00:00:00Z","isCrossRepository":false}]}}}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := linkedPRFixture(firstPageKey, `{"data":{"repository":{
				"issue":{"closedByPullRequestsReferences":{"pageInfo":{"hasNextPage":false},"nodes":[]},"timelineItems":`+tt.firstTL+`},
				"pullRequests":`+tt.firstBR+`
			}}}`)
			client.responses[tt.pageKey] = tt.pageBody
			result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
			if err != nil {
				t.Fatalf("Observe: %v", err)
			}
			if result.PRURL != "https://github.com/acme/widgets/pull/12" {
				t.Errorf("pr_url = %q, want the open PR from the second page over the closed one on the first", result.PRURL)
			}
		})
	}
}

func TestObserve_Issue_LaterPageFailureDegradesToIssueOnlyState(t *testing.T) {
	client := linkedPRFixture(firstPageKey, `{"data":{"repository":{
		"issue":{"closedByPullRequestsReferences":{"pageInfo":{"hasNextPage":false},"nodes":[]},
			"timelineItems":{"pageInfo":{"hasNextPage":true,"endCursor":"T1"},"nodes":[{"source":`+staleClosedCandidate+`}]}},
		"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[]}
	}}}`)
	client.errs = map[string]error{timelinePageTwoKey: errors.New("HTTP 502")}
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "" || result.Revision != "issue:2026-01-01T00:00:00Z" {
		t.Errorf("result = %+v, want the issue-only state rather than a choice among a partial candidate set", result)
	}
}

func TestObserve_Issue_NonAdvancingCursorDegradesToIssueOnlyState(t *testing.T) {
	client := linkedPRFixture(firstPageKey, `{"data":{"repository":{
		"issue":{"closedByPullRequestsReferences":{"pageInfo":{"hasNextPage":false},"nodes":[]},
			"timelineItems":{"pageInfo":{"hasNextPage":true,"endCursor":""},"nodes":[{"source":`+staleClosedCandidate+`}]}},
		"pullRequests":{"pageInfo":{"hasNextPage":false},"nodes":[]}
	}}}`)
	result, err := Observe(context.Background(), Options{ResourceID: "https://github.com/acme/widgets/issues/7", GHClient: client})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if result.PRURL != "" {
		t.Errorf("pr_url = %q, want none: a page that cannot be followed leaves the candidate set partial", result.PRURL)
	}
}
