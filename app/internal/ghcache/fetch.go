package ghcache

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Fetcher retrieves GitHub data using the gh CLI.
type Fetcher struct {
	// GhCommand is the gh CLI path. Defaults to "gh" if empty.
	GhCommand string
	// NowFunc overrides time.Now for testing. If nil, time.Now is used.
	NowFunc func() time.Time
}

// NewFetcher creates a Fetcher with defaults.
func NewFetcher() *Fetcher {
	return &Fetcher{GhCommand: "gh"}
}

// Now returns the current time, using NowFunc if set.
func (f *Fetcher) Now() time.Time {
	if f.NowFunc != nil {
		return f.NowFunc()
	}
	return time.Now()
}

type ghIssueResult struct {
	Title    string      `json:"title"`
	State    string      `json:"state"`
	Comments []ghComment `json:"comments"`
}

type ghComment struct {
	ID int64 `json:"databaseId"`
}

type ghPRResult struct {
	Title             string          `json:"title"`
	State             string          `json:"state"`
	ReviewDecision    string          `json:"reviewDecision"`
	Mergeable         string          `json:"mergeable"`
	Comments          []ghComment     `json:"comments"`
	Reviews           []ghReview      `json:"reviews"`
	StatusCheckRollup []ghStatusCheck `json:"statusCheckRollup"`
	HeadRefOid        string          `json:"headRefOid"`
	Commits           []ghCommit      `json:"commits"`
}

type ghCommit struct {
	OID             string `json:"oid"`
	MessageHeadline string `json:"messageHeadline"`
}

type ghReview struct {
	ID int64 `json:"databaseId"`
}

type ghStatusCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// FetchIssue fetches GitHub Issue data via gh CLI.
func (f *Fetcher) FetchIssue(ownerRepo string, number int) (*IssueCacheEntry, error) {
	ghCmd := f.ghCommand()
	out, err := exec.Command(ghCmd, "issue", "view", fmt.Sprintf("%d", number),
		"--repo", ownerRepo,
		"--json", "title,state,comments",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh issue view failed for %s#%d: %w", ownerRepo, number, err)
	}

	var result ghIssueResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse gh issue output: %w", err)
	}

	var lastCommentID int64
	if len(result.Comments) > 0 {
		lastCommentID = result.Comments[len(result.Comments)-1].ID
	}

	return &IssueCacheEntry{
		CacheEntryBase: CacheEntryBase{
			OwnerRepo:     ownerRepo,
			Number:        number,
			Title:         result.Title,
			State:         normalizeState(result.State, false),
			CommentCount:  len(result.Comments),
			LastCommentID: lastCommentID,
			FetchedAt:     f.Now(),
		},
	}, nil
}

// FetchPR fetches GitHub PR data via gh CLI.
func (f *Fetcher) FetchPR(ownerRepo string, number int) (*PRCacheEntry, error) {
	ghCmd := f.ghCommand()
	out, err := exec.Command(ghCmd, "pr", "view", fmt.Sprintf("%d", number),
		"--repo", ownerRepo,
		"--json", "title,state,reviewDecision,mergeable,comments,reviews,statusCheckRollup,headRefOid,commits",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr view failed for %s#%d: %w", ownerRepo, number, err)
	}

	var result ghPRResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse gh pr output: %w", err)
	}

	var lastCommentID int64
	if len(result.Comments) > 0 {
		lastCommentID = result.Comments[len(result.Comments)-1].ID
	}

	var lastReviewCommentID int64
	if len(result.Reviews) > 0 {
		lastReviewCommentID = result.Reviews[len(result.Reviews)-1].ID
	}

	checksStatus, checksDetail := summarizeChecks(result.StatusCheckRollup)

	return &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{
			OwnerRepo:     ownerRepo,
			Number:        number,
			Title:         result.Title,
			State:         normalizeState(result.State, true),
			CommentCount:  len(result.Comments),
			LastCommentID: lastCommentID,
			FetchedAt:     f.Now(),
		},
		Mergeable:           result.Mergeable,
		ReviewDecision:      result.ReviewDecision,
		ChecksStatus:        checksStatus,
		ChecksDetail:        checksDetail,
		ReviewCount:         len(result.Reviews),
		LastReviewID:        lastReviewCommentID,
		HeadSHA:             result.HeadRefOid,
		CommitCount:         len(result.Commits),
		LatestCommitSubject: latestCommitSubject(result.Commits),
	}, nil
}

// ghPRListResult is a single element from `gh pr list --json ...`.
type ghPRListResult struct {
	Number            int             `json:"number"`
	Title             string          `json:"title"`
	State             string          `json:"state"`
	ReviewDecision    string          `json:"reviewDecision"`
	Mergeable         string          `json:"mergeable"`
	Comments          []ghComment     `json:"comments"`
	Reviews           []ghReview      `json:"reviews"`
	StatusCheckRollup []ghStatusCheck `json:"statusCheckRollup"`
	HeadRefOid        string          `json:"headRefOid"`
	Commits           []ghCommit      `json:"commits"`
}

// FetchPRByBranch finds an open PR whose head branch matches the given branch name.
// Returns nil, nil if no matching PR is found.
func (f *Fetcher) FetchPRByBranch(ownerRepo, branch string) (*PRCacheEntry, error) {
	ghCmd := f.ghCommand()
	out, err := exec.Command(ghCmd, "pr", "list",
		"--head", branch,
		"--repo", ownerRepo,
		"--json", "number,title,state,reviewDecision,mergeable,comments,reviews,statusCheckRollup,headRefOid,commits",
		"--limit", "1",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("gh pr list failed for %s (branch %s): %w", ownerRepo, branch, err)
	}

	var results []ghPRListResult
	if err := json.Unmarshal(out, &results); err != nil {
		return nil, fmt.Errorf("failed to parse gh pr list output: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	r := results[0]

	var lastCommentID int64
	if len(r.Comments) > 0 {
		lastCommentID = r.Comments[len(r.Comments)-1].ID
	}

	var lastReviewID int64
	if len(r.Reviews) > 0 {
		lastReviewID = r.Reviews[len(r.Reviews)-1].ID
	}

	checksStatus, checksDetail := summarizeChecks(r.StatusCheckRollup)

	return &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{
			OwnerRepo:     ownerRepo,
			Number:        r.Number,
			Title:         r.Title,
			State:         normalizeState(r.State, true),
			CommentCount:  len(r.Comments),
			LastCommentID: lastCommentID,
			FetchedAt:     f.Now(),
		},
		Mergeable:           r.Mergeable,
		ReviewDecision:      r.ReviewDecision,
		ChecksStatus:        checksStatus,
		ChecksDetail:        checksDetail,
		ReviewCount:         len(r.Reviews),
		LastReviewID:        lastReviewID,
		HeadSHA:             r.HeadRefOid,
		CommitCount:         len(r.Commits),
		LatestCommitSubject: latestCommitSubject(r.Commits),
	}, nil
}

func latestCommitSubject(commits []ghCommit) string {
	if len(commits) == 0 {
		return ""
	}
	return commits[len(commits)-1].MessageHeadline
}

func (f *Fetcher) ghCommand() string {
	if f.GhCommand != "" {
		return f.GhCommand
	}
	return "gh"
}

// normalizeState converts GitHub API state to lowercase.
// For PRs, "MERGED" is a separate state; for issues it's just open/closed.
func normalizeState(state string, isPR bool) string {
	s := strings.ToLower(state)
	if isPR && s == "merged" {
		return "merged"
	}
	return s
}

// summarizeChecks returns an overall status and detail string from status checks.
func summarizeChecks(checks []ghStatusCheck) (status, detail string) {
	if len(checks) == 0 {
		return "", ""
	}

	var failed, pending, passed int
	var failedNames []string
	for _, c := range checks {
		conclusion := strings.ToLower(c.Conclusion)
		st := strings.ToLower(c.Status)
		state := strings.ToLower(c.State)

		if conclusion == "failure" || conclusion == "timed_out" || state == "failure" || state == "error" {
			failed++
			failedNames = append(failedNames, c.Name)
		} else if conclusion == "success" || state == "success" {
			passed++
		} else if st == "in_progress" || st == "queued" || st == "pending" || state == "pending" {
			pending++
		} else if conclusion == "" && st == "" && state == "" {
			pending++
		} else {
			passed++ // neutral, skipped, etc.
		}
	}

	switch {
	case failed > 0:
		status = "FAILURE"
		detail = fmt.Sprintf("%d/%d failed: %s", failed, len(checks), strings.Join(failedNames, ", "))
	case pending > 0:
		status = "PENDING"
		detail = fmt.Sprintf("%d/%d pending", pending, len(checks))
	default:
		status = "SUCCESS"
		detail = fmt.Sprintf("%d/%d passed", passed, len(checks))
	}
	return
}
