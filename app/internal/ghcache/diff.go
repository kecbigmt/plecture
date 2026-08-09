package ghcache

import "fmt"

// ChangeType identifies the kind of change detected.
type ChangeType string

const (
	ChangeState            ChangeType = "state"
	ChangeNewComments      ChangeType = "new_comments"
	ChangeCIStatus         ChangeType = "ci_status"
	ChangeReviewDecision   ChangeType = "review_decision"
	ChangeNewReviews       ChangeType = "new_review_comments"
	ChangeNewCommits       ChangeType = "new_commits"
	ChangeConflict         ChangeType = "conflict"
	ChangeConflictResolved ChangeType = "conflict_resolved"
)

// Change represents a detected difference between two cache entries.
type Change struct {
	SessionName string
	Type        ChangeType
	Summary     string
	// URL is the URL of the resource where the change occurred.
	// When set, hooks should use this instead of the session URL.
	URL string
}

// DiffIssue compares two issue cache entries and returns detected changes.
// Returns nil if old is nil (first sync, no comparison).
// If the issue has a LinkedPR, PR-level changes are also detected.
func DiffIssue(sessionName string, old, new *IssueCacheEntry) []Change {
	if old == nil {
		return nil
	}

	issueURL := fmt.Sprintf("https://github.com/%s/issues/%d", new.OwnerRepo, new.Number)

	changes := diffBase(sessionName, &old.CacheEntryBase, &new.CacheEntryBase)
	// Set issue URL on issue-level changes
	for i := range changes {
		changes[i].URL = issueURL
	}

	// Diff linked PR if present in the new entry (PR URL is set by DiffPR)
	if new.LinkedPR != nil {
		changes = append(changes, DiffPR(sessionName, old.LinkedPR, new.LinkedPR)...)
	}

	return changes
}

// DiffPR compares two PR cache entries and returns detected changes.
// Returns nil if old is nil (first sync, no comparison).
func DiffPR(sessionName string, old, new *PRCacheEntry) []Change {
	if old == nil {
		return nil
	}

	prURL := fmt.Sprintf("https://github.com/%s/pull/%d", new.OwnerRepo, new.Number)

	changes := diffBase(sessionName, &old.CacheEntryBase, &new.CacheEntryBase)

	// CI status change (especially failures)
	if old.ChecksStatus != new.ChecksStatus {
		changes = append(changes, Change{
			SessionName: sessionName,
			Type:        ChangeCIStatus,
			Summary:     fmt.Sprintf("CI status changed: %s -> %s (%s)", old.ChecksStatus, new.ChecksStatus, new.ChecksDetail),
		})
	}

	// Review decision change
	if old.ReviewDecision != new.ReviewDecision && new.ReviewDecision != "" {
		changes = append(changes, Change{
			SessionName: sessionName,
			Type:        ChangeReviewDecision,
			Summary:     fmt.Sprintf("Review decision changed: %s -> %s", old.ReviewDecision, new.ReviewDecision),
		})
	}

	// New reviews
	if new.ReviewCount > old.ReviewCount {
		diff := new.ReviewCount - old.ReviewCount
		changes = append(changes, Change{
			SessionName: sessionName,
			Type:        ChangeNewReviews,
			Summary:     fmt.Sprintf("%d new review(s)", diff),
		})
	}

	// New commits (HEAD SHA changed)
	if old.HeadSHA != "" && new.HeadSHA != "" && old.HeadSHA != new.HeadSHA {
		commitDiff := new.CommitCount - old.CommitCount
		var summary string
		switch {
		case commitDiff > 0:
			summary = fmt.Sprintf("%d new commit(s) pushed (HEAD: %s -> %s)", commitDiff, short(old.HeadSHA), short(new.HeadSHA))
		case commitDiff < 0:
			summary = fmt.Sprintf("force-pushed: %d -> %d commits (HEAD: %s -> %s)", old.CommitCount, new.CommitCount, short(old.HeadSHA), short(new.HeadSHA))
		default:
			summary = fmt.Sprintf("force-pushed (HEAD: %s -> %s)", short(old.HeadSHA), short(new.HeadSHA))
		}
		if new.LatestCommitSubject != "" {
			summary += fmt.Sprintf(", latest: %q", new.LatestCommitSubject)
		}
		changes = append(changes, Change{
			SessionName: sessionName,
			Type:        ChangeNewCommits,
			Summary:     summary,
		})
	}

	// Conflict detection
	if old.Mergeable != new.Mergeable && new.Mergeable != "" && new.Mergeable != "UNKNOWN" {
		changeType := ChangeConflict
		if new.Mergeable == "MERGEABLE" {
			changeType = ChangeConflictResolved
		}
		changes = append(changes, Change{
			SessionName: sessionName,
			Type:        changeType,
			Summary:     fmt.Sprintf("Mergeable status changed: %s -> %s", old.Mergeable, new.Mergeable),
		})
	}

	// Set PR URL on all changes
	for i := range changes {
		changes[i].URL = prURL
	}

	return changes
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func diffBase(sessionName string, old, new *CacheEntryBase) []Change {
	var changes []Change

	// State change
	if old.State != new.State {
		changes = append(changes, Change{
			SessionName: sessionName,
			Type:        ChangeState,
			Summary:     fmt.Sprintf("State changed: %s -> %s", old.State, new.State),
		})
	}

	// New comments
	if new.CommentCount > old.CommentCount {
		diff := new.CommentCount - old.CommentCount
		changes = append(changes, Change{
			SessionName: sessionName,
			Type:        ChangeNewComments,
			Summary:     fmt.Sprintf("%d new comment(s)", diff),
		})
	}

	return changes
}
