// Package observe implements the GitHub resource's observation contract:
// turning a GitHub issue or pull request identifier into the state
// resources/github.toml's [state_schema] declares — resource kind, check
// rollup, issue completion, revision, the linked pull request (for an issue
// resource), and mergeability.
//
// This is independent of the workspace provider's Setup/Cleanup (package
// worktree): that owns *session* workspace acquisition, this owns the
// resource's observable state, callable standalone via `plect resource
// status` outside any one session.
package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/ghapi"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/github"
	"github.com/kecbigmt/plecture/plugins/github/src/internal/procexec"
)

// Result is the observed state, one field per resources/github.toml
// [state_schema] property.
type Result struct {
	ResourceKind   string // "pull" | "issue" | "unknown"
	ChecksStatus   string // "SUCCESS" | "PENDING" | "FAILURE" | "NULL"
	IssueStatus    string // "SUCCESS" | "PENDING" | "NULL"
	Revision       string
	PRURL          string
	MergeableState string // GitHub's own vocabulary, or "NULL" / "unknown"
	ReviewDecision string // "APPROVED" | "CHANGES_REQUESTED" | "REVIEW_REQUIRED" | "NULL"
}

// Options are the inputs Observe needs.
type Options struct {
	// ResourceID is the GitHub issue or pull request URL being observed.
	ResourceID string
	// WorkspaceDirPath is the observing task instance's session workspace
	// directory, when one exists. Empty for a standalone `plect resource
	// status` call, which has no owning session.
	WorkspaceDirPath string
	// GHClient fetches from the GitHub REST/GraphQL API. Defaults to
	// ghapi.Direct().
	GHClient github.GHClient
	// WorkspaceDirBranch returns the branch checked out at a workspace
	// directory, or "" when it cannot be determined. Defaults to `git
	// symbolic-ref --short HEAD`.
	WorkspaceDirBranch func(ctx context.Context, workspaceDirPath string) string
}

// Observe fetches and classifies a GitHub resource's current state.
func Observe(ctx context.Context, opts Options) (*Result, error) {
	client := opts.GHClient
	if client == nil {
		client = ghapi.Direct()
	}

	parsed, err := github.ParseURL(opts.ResourceID)
	if err != nil {
		return &Result{ResourceKind: "unknown", ChecksStatus: "NULL", IssueStatus: "NULL", MergeableState: "NULL", ReviewDecision: "NULL"}, nil
	}

	if parsed.Type == github.URLTypePR {
		return observePull(ctx, client, parsed)
	}
	return observeIssue(ctx, client, parsed, opts)
}

func observePull(ctx context.Context, client github.GHClient, parsed *github.ParsedURL) (*Result, error) {
	head, err := fetchPRHeadMergeable(ctx, client, parsed.Owner, parsed.Repo, parsed.Number)
	if err != nil {
		return nil, err
	}
	checks, err := checksRollup(ctx, client, parsed.Owner, parsed.Repo, head.sha)
	if err != nil {
		return nil, err
	}
	return &Result{
		ResourceKind:   "pull",
		ChecksStatus:   checks,
		IssueStatus:    "NULL",
		Revision:       head.sha,
		PRURL:          parsed.URL(),
		MergeableState: head.mergeableState,
		ReviewDecision: fetchReviewDecision(ctx, client, parsed.Owner, parsed.Repo, parsed.Number),
	}, nil
}

// observeIssue implements the issue-linked-PR tracking documented on
// resources/github.toml's `observe` hook: an issue-keyed session tracks its
// linked PR once one exists, so checks_status becomes the PR's rollup and
// revision its head SHA — a chain gated on checks SUCCESS can never fire
// before the linked PR is up and green.
func observeIssue(ctx context.Context, client github.GHClient, parsed *github.ParsedURL, opts Options) (*Result, error) {
	raw, err := client.JSON(ctx, fmt.Sprintf("repos/%s/%s/issues/%d", parsed.Owner, parsed.Repo, parsed.Number))
	if err != nil {
		return nil, fmt.Errorf("fetch issue: %w", err)
	}
	var meta struct {
		State     string `json:"state"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse issue: %w", err)
	}

	issueStatus := "PENDING"
	if strings.EqualFold(meta.State, "closed") {
		issueStatus = "SUCCESS"
	}
	result := &Result{
		ResourceKind:   "issue",
		ChecksStatus:   "NULL",
		IssueStatus:    issueStatus,
		Revision:       "issue:" + meta.UpdatedAt,
		MergeableState: "NULL",
		ReviewDecision: "NULL",
	}

	prURL, err := discoverLinkedPR(ctx, client, parsed, opts)
	if err != nil {
		return nil, err
	}
	if prURL == "" {
		return result, nil
	}
	prNumber, ok := prNumberFromURL(prURL)
	if !ok {
		return result, nil
	}

	// Once a linked PR is found, its fetch is no longer best-effort: a
	// discovery success with a follow-up fetch failure must fail the whole
	// observation rather than silently keep the issue-only state.
	head, err := fetchPRHeadMergeable(ctx, client, parsed.Owner, parsed.Repo, prNumber)
	if err != nil {
		return nil, err
	}
	checks, err := checksRollup(ctx, client, parsed.Owner, parsed.Repo, head.sha)
	if err != nil {
		return nil, err
	}
	result.PRURL = prURL
	result.Revision = head.sha
	result.ChecksStatus = checks
	result.MergeableState = head.mergeableState
	result.ReviewDecision = fetchReviewDecision(ctx, client, parsed.Owner, parsed.Repo, prNumber)
	return result, nil
}

const reviewDecisionQuery = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    pullRequest(number:$number){ reviewDecision }
  }
}`

// fetchReviewDecision reads GitHub's own aggregate review verdict, which only
// GraphQL exposes — deriving it from the REST reviews list would ignore the
// branch protection rules that decide how many approvals actually count. It
// is the one read here that is not required for the checks/mergeability state
// the rest of the observation exists to produce, so a failure degrades to
// "NULL" instead of failing the whole observation.
func fetchReviewDecision(ctx context.Context, client github.GHClient, owner, repo string, number int) string {
	raw, err := client.JSON(ctx, "graphql",
		"-F", "owner="+owner,
		"-F", "repo="+repo,
		"-F", fmt.Sprintf("number=%d", number),
		"-f", "query="+reviewDecisionQuery,
	)
	if err != nil {
		return "NULL"
	}
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewDecision string `json:"reviewDecision"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return "NULL"
	}
	if resp.Data.Repository.PullRequest.ReviewDecision == "" {
		return "NULL"
	}
	return resp.Data.Repository.PullRequest.ReviewDecision
}

// discoverLinkedPR finds the PR that closes an issue. The issue's own
// closing-PR references bound the answer, and the session's checked out
// branch picks among them: a session that builds several branches in one
// worktree (the stacked-PR pattern) leaves HEAD on a later branch whose PR
// closes a different issue, so the branch alone would report that PR's head
// as this issue's revision and make every judge recorded on the real PR read
// as stale. The branch still decides when the issue has no closing reference
// at all — a PR whose body never named the issue is invisible to GitHub's
// relation — and when several references compete.
//
// A reference lookup that fails is therefore not the same as one that
// returns nothing: with a branch pull request in hand and no way to check it
// against the issue, reporting it would resurrect exactly the wrong-revision
// failure above, and degrading to the issue-only state would move the
// revision off the reviewed head just as destructively, so the observation
// fails and the last observed values stand. With no branch pull request
// there is nothing the references could contradict, and the failure still
// degrades to "no linked PR yet".
func discoverLinkedPR(ctx context.Context, client github.GHClient, parsed *github.ParsedURL, opts Options) (string, error) {
	refs, refsErr := closingPRRefs(ctx, client, parsed)
	branchPR := branchPullRequest(ctx, client, parsed, opts)
	if refsErr != nil {
		if branchPR == "" {
			return "", nil
		}
		return "", fmt.Errorf("fetch closing pull request references: %w", refsErr)
	}
	if branchPR != "" && (len(refs) == 0 || containsPR(refs, branchPR)) {
		return branchPR, nil
	}
	return preferOpenPR(refs), nil
}

func branchPullRequest(ctx context.Context, client github.GHClient, parsed *github.ParsedURL, opts Options) string {
	branchOf := opts.WorkspaceDirBranch
	if branchOf == nil {
		branchOf = defaultWorkspaceDirBranch
	}
	branch := branchOf(ctx, opts.WorkspaceDirPath)
	if branch == "" {
		return ""
	}
	path := fmt.Sprintf("repos/%s/%s/pulls?head=%s:%s&state=all", parsed.Owner, parsed.Repo, parsed.Owner, branch)
	raw, err := client.JSON(ctx, path)
	if err != nil {
		return ""
	}
	var prs []struct {
		HTMLURL string `json:"html_url"`
	}
	if json.Unmarshal(raw, &prs) != nil || len(prs) == 0 {
		return ""
	}
	return prs[0].HTMLURL
}

func containsPR(refs []closingPRRef, url string) bool {
	for _, r := range refs {
		if r.URL == url {
			return true
		}
	}
	return false
}

const closingPRsQuery = `query($owner:String!,$repo:String!,$number:Int!){
  repository(owner:$owner,name:$repo){
    issue(number:$number){
      closedByPullRequestsReferences(first:10,includeClosedPrs:true){
        nodes{url state updatedAt}
      }
    }
  }
}`

// closingPRRef is one pull request GitHub reports as closing the issue.
type closingPRRef struct {
	URL       string `json:"url"`
	State     string `json:"state"`
	UpdatedAt string `json:"updatedAt"`
}

func closingPRRefs(ctx context.Context, client github.GHClient, parsed *github.ParsedURL) ([]closingPRRef, error) {
	raw, err := client.JSON(ctx, "graphql",
		"-F", "owner="+parsed.Owner,
		"-F", "repo="+parsed.Repo,
		"-F", fmt.Sprintf("number=%d", parsed.Number),
		"-f", "query="+closingPRsQuery,
	)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Repository struct {
				Issue struct {
					ClosedByPullRequestsReferences struct {
						Nodes []closingPRRef `json:"nodes"`
					} `json:"closedByPullRequestsReferences"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse closing pull request references: %w", err)
	}
	return resp.Data.Repository.Issue.ClosedByPullRequestsReferences.Nodes, nil
}

func preferOpenPR(refs []closingPRRef) string {
	if len(refs) == 0 {
		return ""
	}
	// Prefer the first OPEN reference; ISO 8601 timestamps sort correctly as
	// plain strings, so the most recently updated otherwise wins.
	latest := refs[0]
	for _, n := range refs {
		if strings.EqualFold(n.State, "OPEN") {
			return n.URL
		}
		if n.UpdatedAt > latest.UpdatedAt {
			latest = n
		}
	}
	return latest.URL
}

var prNumberRE = regexp.MustCompile(`/pull/(\d+)`)

func prNumberFromURL(prURL string) (int, bool) {
	m := prNumberRE.FindStringSubmatch(prURL)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

func defaultWorkspaceDirBranch(ctx context.Context, workspaceDirPath string) string {
	if workspaceDirPath == "" {
		return ""
	}
	if _, err := os.Stat(workspaceDirPath); err != nil {
		return ""
	}
	out, _, err := procexec.Default.Run(ctx, workspaceDirPath, false, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type prHeadMergeable struct {
	sha            string
	mergeableState string
}

// fetchPRHeadMergeable fetches a pull request's head SHA and mergeable
// state. mergeable_state normally already carries GitHub's REST vocabulary
// ("clean"/"dirty"/"unstable"/...); the mergeable/conflicting mapping below
// guards against the GraphQL-only enum values leaking in through a future
// endpoint change, matching production's defensive mapping.
func fetchPRHeadMergeable(ctx context.Context, client github.GHClient, owner, repo string, number int) (*prHeadMergeable, error) {
	raw, err := client.JSON(ctx, fmt.Sprintf("repos/%s/%s/pulls/%d", owner, repo, number))
	if err != nil {
		return nil, fmt.Errorf("fetch pull request: %w", err)
	}
	var resp struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
		MergeableState string `json:"mergeable_state"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse pull request: %w", err)
	}
	m := strings.ToLower(resp.MergeableState)
	if m == "" {
		m = "unknown"
	}
	switch m {
	case "mergeable":
		m = "clean"
	case "conflicting":
		m = "dirty"
	}
	return &prHeadMergeable{sha: resp.Head.SHA, mergeableState: m}, nil
}

// checkEntry is one check-run or combined-status entry, normalized to the
// two fields rollupChecks classifies on. A check-run still running carries
// its status in state (conclusion is empty until it finishes); a combined
// status entry only ever has state.
type checkEntry struct {
	conclusion string // upper-case
	state      string // upper-case
}

var (
	failureConclusions = map[string]bool{"FAILURE": true, "TIMED_OUT": true, "CANCELLED": true, "ACTION_REQUIRED": true, "STARTUP_FAILURE": true, "STALE": true}
	successConclusions = map[string]bool{"SUCCESS": true, "SKIPPED": true, "NEUTRAL": true}
	failureStates      = map[string]bool{"FAILURE": true, "ERROR": true}
)

// rollupChecks mirrors the old GraphQL statusCheckRollup verdict from two
// REST sources (check-runs and combined status): any failing entry fails the
// whole rollup, else any still-pending entry pends it, else it succeeds. No
// entries at all is the "NULL" sentinel — a resource with no CI configured,
// distinct from a rollup that is merely pending.
func rollupChecks(entries []checkEntry) string {
	if len(entries) == 0 {
		return "NULL"
	}
	sawPending := false
	for _, e := range entries {
		switch {
		case failureConclusions[e.conclusion] || failureStates[e.state]:
			return "FAILURE"
		case successConclusions[e.conclusion] || e.state == "SUCCESS":
			// counts toward success; nothing to record
		default:
			sawPending = true
		}
	}
	if sawPending {
		return "PENDING"
	}
	return "SUCCESS"
}

func checksRollup(ctx context.Context, client github.GHClient, owner, repo, sha string) (string, error) {
	runs, err := fetchCheckRuns(ctx, client, owner, repo, sha)
	if err != nil {
		return "", err
	}
	statuses, err := fetchCombinedStatus(ctx, client, owner, repo, sha)
	if err != nil {
		return "", err
	}
	return rollupChecks(append(runs, statuses...)), nil
}

// checkRun is one check-run as the REST endpoint reports it, carrying the
// identity and ordering fields latestCheckRunPerName needs on top of the
// classification fields.
type checkRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
}

// latestCheckRunPerName keeps only the newest run for each check name. A
// commit accumulates every check-run ever created for it, so a workflow run
// superseded by a force-push or by concurrency cancellation leaves CANCELLED
// runs behind forever; rolling those up would pin the commit to FAILURE for
// the rest of its life, while gh and the GitHub UI report the re-run's
// verdict. Run ids are monotonic, so they break ties when two runs of the
// same name share a start time.
func latestCheckRunPerName(runs []checkRun) []checkEntry {
	latest := make(map[string]checkRun, len(runs))
	order := make([]string, 0, len(runs))
	for _, r := range runs {
		prev, seen := latest[r.Name]
		if !seen {
			order = append(order, r.Name)
			latest[r.Name] = r
			continue
		}
		if r.StartedAt > prev.StartedAt || (r.StartedAt == prev.StartedAt && r.ID > prev.ID) {
			latest[r.Name] = r
		}
	}
	entries := make([]checkEntry, 0, len(order))
	for _, name := range order {
		r := latest[name]
		if r.Conclusion != "" {
			entries = append(entries, checkEntry{conclusion: strings.ToUpper(r.Conclusion)})
		} else {
			entries = append(entries, checkEntry{state: strings.ToUpper(r.Status)})
		}
	}
	return entries
}

// fetchCheckRuns reads one page of check-runs. Production paginates this
// endpoint; a resource whose check suite exceeds one page (GitHub's default
// is 30) is a known gap here, left for a follow-up if it proves to matter in
// practice rather than adding untested pagination now.
func fetchCheckRuns(ctx context.Context, client github.GHClient, owner, repo, sha string) ([]checkEntry, error) {
	raw, err := client.JSON(ctx, fmt.Sprintf("repos/%s/%s/commits/%s/check-runs", owner, repo, sha))
	if err != nil {
		return nil, fmt.Errorf("fetch check runs: %w", err)
	}
	var resp struct {
		CheckRuns []checkRun `json:"check_runs"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse check runs: %w", err)
	}
	return latestCheckRunPerName(resp.CheckRuns), nil
}

func fetchCombinedStatus(ctx context.Context, client github.GHClient, owner, repo, sha string) ([]checkEntry, error) {
	raw, err := client.JSON(ctx, fmt.Sprintf("repos/%s/%s/commits/%s/status", owner, repo, sha))
	if err != nil {
		return nil, fmt.Errorf("fetch combined status: %w", err)
	}
	var resp struct {
		Statuses []struct {
			State string `json:"state"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("parse combined status: %w", err)
	}
	entries := make([]checkEntry, 0, len(resp.Statuses))
	for _, s := range resp.Statuses {
		entries = append(entries, checkEntry{state: strings.ToUpper(s.State)})
	}
	return entries, nil
}
