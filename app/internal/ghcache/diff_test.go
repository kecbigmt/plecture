package ghcache

import "testing"

func TestDiffIssue_NilOld(t *testing.T) {
	new := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open"}}
	changes := DiffIssue("sess", nil, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes for nil old, got %d", len(changes))
	}
}

func TestDiffIssue_StateChange(t *testing.T) {
	old := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 1}}
	new := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "closed", OwnerRepo: "owner/repo", Number: 1}}
	changes := DiffIssue("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != ChangeState {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeState)
	}
	if changes[0].SessionName != "sess" {
		t.Errorf("SessionName = %q, want %q", changes[0].SessionName, "sess")
	}
	if changes[0].URL != "https://github.com/owner/repo/issues/1" {
		t.Errorf("URL = %q, want issue URL", changes[0].URL)
	}
}

func TestDiffIssue_NewComments(t *testing.T) {
	old := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open", CommentCount: 2}}
	new := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open", CommentCount: 5}}
	changes := DiffIssue("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != ChangeNewComments {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeNewComments)
	}
}

func TestDiffIssue_NoChange(t *testing.T) {
	old := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open", CommentCount: 3}}
	new := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open", CommentCount: 3}}
	changes := DiffIssue("sess", old, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

func TestDiffIssue_LinkedPR_CIChange(t *testing.T) {
	old := &IssueCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 1},
		LinkedPR: &PRCacheEntry{
			CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 10},
			ChecksStatus:   "PENDING",
		},
	}
	new := &IssueCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 1},
		LinkedPR: &PRCacheEntry{
			CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 10},
			ChecksStatus:   "FAILURE",
			ChecksDetail:   "1/3 failed: lint",
		},
	}
	changes := DiffIssue("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeCIStatus {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeCIStatus)
	}
	// Change from linked PR should have PR URL, not issue URL
	if changes[0].URL != "https://github.com/owner/repo/pull/10" {
		t.Errorf("URL = %q, want PR URL https://github.com/owner/repo/pull/10", changes[0].URL)
	}
}

func TestDiffIssue_LinkedPR_FirstDiscovery(t *testing.T) {
	old := &IssueCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		// No LinkedPR in old (PR didn't exist yet)
	}
	new := &IssueCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		LinkedPR: &PRCacheEntry{
			CacheEntryBase: CacheEntryBase{State: "open"},
			ChecksStatus:   "PENDING",
		},
	}
	// First PR discovery: DiffPR with nil old returns nil, so no changes
	changes := DiffIssue("sess", old, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes on first PR discovery, got %d: %+v", len(changes), changes)
	}
}

func TestDiffIssue_LinkedPR_NoLinkedPR(t *testing.T) {
	old := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open"}}
	new := &IssueCacheEntry{CacheEntryBase: CacheEntryBase{State: "open"}}
	changes := DiffIssue("sess", old, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

func TestDiffPR_NilOld(t *testing.T) {
	new := &PRCacheEntry{CacheEntryBase: CacheEntryBase{State: "open"}}
	changes := DiffPR("sess", nil, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes for nil old, got %d", len(changes))
	}
}

func TestDiffPR_CIStatusChange(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		ChecksStatus:   "PENDING",
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		ChecksStatus:   "FAILURE",
		ChecksDetail:   "1/3 failed: lint",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != ChangeCIStatus {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeCIStatus)
	}
	if changes[0].URL != "https://github.com/owner/repo/pull/5" {
		t.Errorf("URL = %q, want PR URL", changes[0].URL)
	}
}

func TestDiffPR_ReviewDecisionChange(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		ReviewDecision: "",
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		ReviewDecision: "CHANGES_REQUESTED",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != ChangeReviewDecision {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeReviewDecision)
	}
}

func TestDiffPR_NewReviewComments(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		ReviewCount:    1,
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		ReviewCount:    3,
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Type != ChangeNewReviews {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeNewReviews)
	}
}

func TestDiffPR_MultipleChanges(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", CommentCount: 1},
		ChecksStatus:   "PENDING",
		ReviewCount:    0,
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", CommentCount: 3},
		ChecksStatus:   "FAILURE",
		ChecksDetail:   "1/2 failed: ci",
		ReviewCount:    2,
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(changes), changes)
	}

	types := map[ChangeType]bool{}
	for _, c := range changes {
		types[c.Type] = true
	}
	for _, expected := range []ChangeType{ChangeNewComments, ChangeCIStatus, ChangeNewReviews} {
		if !types[expected] {
			t.Errorf("missing change type %q", expected)
		}
	}
}

func TestDiffPR_NewCommits(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		HeadSHA:        "abc1234567890",
		CommitCount:    3,
	}
	new := &PRCacheEntry{
		CacheEntryBase:      CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		HeadSHA:             "def5678901234",
		CommitCount:         5,
		LatestCommitSubject: "fix review comments",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeNewCommits {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeNewCommits)
	}
	if changes[0].URL != "https://github.com/owner/repo/pull/5" {
		t.Errorf("URL = %q, want PR URL", changes[0].URL)
	}
	want := `2 new commit(s) pushed (HEAD: abc1234 -> def5678), latest: "fix review comments"`
	if got := changes[0].Summary; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestDiffPR_ForcePushSameCount(t *testing.T) {
	// Force push or rebase: HEAD changed but commit count is the same
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		HeadSHA:        "abc1234567890",
		CommitCount:    3,
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		HeadSHA:        "def5678901234",
		CommitCount:    3,
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeNewCommits {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeNewCommits)
	}
	want := "force-pushed (HEAD: abc1234 -> def5678)"
	if got := changes[0].Summary; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestDiffPR_ForcePushSquash(t *testing.T) {
	// Squash: HEAD changed and commit count decreased
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		HeadSHA:        "abc1234567890",
		CommitCount:    5,
	}
	new := &PRCacheEntry{
		CacheEntryBase:      CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		HeadSHA:             "def5678901234",
		CommitCount:         2,
		LatestCommitSubject: "squashed commits",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeNewCommits {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeNewCommits)
	}
	want := `force-pushed: 5 -> 2 commits (HEAD: abc1234 -> def5678), latest: "squashed commits"`
	if got := changes[0].Summary; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestDiffPR_SameHead_NoChange(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		HeadSHA:        "abc1234567890",
		CommitCount:    3,
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		HeadSHA:        "abc1234567890",
		CommitCount:    3,
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d: %+v", len(changes), changes)
	}
}

func TestDiffPR_FirstSyncWithHeadSHA(t *testing.T) {
	// Old entry has no HeadSHA (upgraded from older cache format)
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		HeadSHA:        "abc1234567890",
		CommitCount:    3,
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes when old has no HeadSHA, got %d: %+v", len(changes), changes)
	}
}

func TestDiffIssue_LinkedPR_NewCommits(t *testing.T) {
	old := &IssueCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 1},
		LinkedPR: &PRCacheEntry{
			CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 10},
			HeadSHA:        "abc1234567890",
			CommitCount:    3,
		},
	}
	new := &IssueCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 1},
		LinkedPR: &PRCacheEntry{
			CacheEntryBase:      CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 10},
			HeadSHA:             "def5678901234",
			CommitCount:         5,
			LatestCommitSubject: "fix typo",
		},
	}
	changes := DiffIssue("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeNewCommits {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeNewCommits)
	}
	if changes[0].URL != "https://github.com/owner/repo/pull/10" {
		t.Errorf("URL = %q, want PR URL", changes[0].URL)
	}
}

func TestDiffPR_ConflictDetected(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		Mergeable:      "MERGEABLE",
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		Mergeable:      "CONFLICTING",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeConflict {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeConflict)
	}
	if changes[0].URL != "https://github.com/owner/repo/pull/5" {
		t.Errorf("URL = %q, want PR URL", changes[0].URL)
	}
	want := "Mergeable status changed: MERGEABLE -> CONFLICTING"
	if got := changes[0].Summary; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestDiffPR_ConflictResolved(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		Mergeable:      "CONFLICTING",
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", OwnerRepo: "owner/repo", Number: 5},
		Mergeable:      "MERGEABLE",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeConflictResolved {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeConflictResolved)
	}
	want := "Mergeable status changed: CONFLICTING -> MERGEABLE"
	if got := changes[0].Summary; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
}

func TestDiffPR_UnknownToConflicting(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		Mergeable:      "UNKNOWN",
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		Mergeable:      "CONFLICTING",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeConflict {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeConflict)
	}
}

func TestDiffPR_UnknownToMergeable_Detected(t *testing.T) {
	// UNKNOWN -> MERGEABLE is detected since MERGEABLE is a valid non-UNKNOWN state
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		Mergeable:      "UNKNOWN",
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		Mergeable:      "MERGEABLE",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeConflictResolved {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeConflictResolved)
	}
}

func TestDiffPR_ToUnknown_NoChange(t *testing.T) {
	// Transition to UNKNOWN should not trigger a change
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		Mergeable:      "MERGEABLE",
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		Mergeable:      "UNKNOWN",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes when transitioning to UNKNOWN, got %d: %+v", len(changes), changes)
	}
}

func TestDiffPR_EmptyToMergeable_Detected(t *testing.T) {
	// Old cache without mergeable field — transition to non-empty is detected
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
		Mergeable:      "MERGEABLE",
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(changes), changes)
	}
	if changes[0].Type != ChangeConflictResolved {
		t.Errorf("Type = %q, want %q", changes[0].Type, ChangeConflictResolved)
	}
}

func TestDiffPR_BothEmpty_NoChange(t *testing.T) {
	old := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
	}
	new := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open"},
	}
	changes := DiffPR("sess", old, new)
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

func TestDiffPR_NoChange(t *testing.T) {
	entry := &PRCacheEntry{
		CacheEntryBase: CacheEntryBase{State: "open", CommentCount: 1},
		ChecksStatus:   "SUCCESS",
		ReviewDecision: "APPROVED",
	}
	changes := DiffPR("sess", entry, entry)
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}
