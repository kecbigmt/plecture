package service

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/app/internal/ghcache"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/contracts/event"
)

func TestToSet(t *testing.T) {
	tests := []struct {
		input []string
		want  map[string]bool
	}{
		{nil, nil},
		{[]string{}, nil},
		{[]string{"open"}, map[string]bool{"open": true}},
		{[]string{"open,closed"}, map[string]bool{"open": true, "closed": true}},
		{[]string{"open", "closed"}, map[string]bool{"open": true, "closed": true}},
	}

	for _, tt := range tests {
		got := toSet(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("toSet(%v) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for k := range tt.want {
			if !got[k] {
				t.Errorf("toSet(%v) missing key %q", tt.input, k)
			}
		}
	}
}

func TestSync_StatusFilter(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		CreatedAt: now, UpdatedAt: now,
	})
	sessionStore.Put(&domain.Session{
		Name: "owner/repo-2", URL: "https://github.com/owner/repo/pull/2",
		URLType: "pr", OwnerRepo: "owner/repo", Number: 2,
		CreatedAt: now, UpdatedAt: now,
	})

	// Pre-populate cache
	cacheStore.Save(&ghcache.CacheFile{
		Version: 1,
		Issues: map[string]*ghcache.IssueCacheEntry{
			"owner/repo-1": {CacheEntryBase: ghcache.CacheEntryBase{
				OwnerRepo: "owner/repo", Number: 1, State: "open",
			}},
		},
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-2": {CacheEntryBase: ghcache.CacheEntryBase{
				OwnerRepo: "owner/repo", Number: 2, State: "closed",
			}},
		},
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"OPEN","comments":[]}`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{
		StatusFilter: []string{"open"},
	})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	if result.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", result.Fetched)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
}

func TestSync_TypeFilter(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		CreatedAt: now, UpdatedAt: now,
	})
	sessionStore.Put(&domain.Session{
		Name: "owner/repo-2", URL: "https://github.com/owner/repo/pull/2",
		URLType: "pr", OwnerRepo: "owner/repo", Number: 2,
		CreatedAt: now, UpdatedAt: now,
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"pr:owner/repo:2": `{"title":"Feature","state":"OPEN","reviewDecision":"","comments":[],"reviews":[],"statusCheckRollup":[]}`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{
		TypeFilter: []string{"pr"},
	})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	if result.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", result.Fetched)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", result.Skipped)
	}
}

func TestSync_UncachedSessionAlwaysFetched(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		CreatedAt: now, UpdatedAt: now,
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"New","state":"OPEN","comments":[]}`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{
		StatusFilter: []string{"closed"},
	})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	if result.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1 (uncached should always be fetched)", result.Fetched)
	}
}

func TestSync_DetectsChanges(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		CreatedAt: now, UpdatedAt: now,
	})

	cacheStore.Save(&ghcache.CacheFile{
		Version: 1,
		Issues: map[string]*ghcache.IssueCacheEntry{
			"owner/repo-1": {CacheEntryBase: ghcache.CacheEntryBase{
				OwnerRepo: "owner/repo", Number: 1, State: "open", CommentCount: 1,
			}},
		},
		PullRequests: map[string]*ghcache.PRCacheEntry{},
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"CLOSED","comments":[{"databaseId":100},{"databaseId":200},{"databaseId":300}]}`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	if len(result.Changes) != 2 {
		t.Fatalf("expected 2 changes, got %d: %+v", len(result.Changes), result.Changes)
	}

	types := map[ghcache.ChangeType]bool{}
	for _, c := range result.Changes {
		types[c.Type] = true
	}
	if !types[ghcache.ChangeState] {
		t.Error("missing state change")
	}
	if !types[ghcache.ChangeNewComments] {
		t.Error("missing new comments change")
	}
}

// TestSync_DoesNotAppendEventLog guards against a past regression: legacy
// ghcache sync must not append github events to the per-session event log.
// github event publishing is owned by github-watcher/bus; sync appending here
// produced duplicate, stream-less events every time `tws ls`/`show --sync` ran.
func TestSync_DoesNotAppendEventLog(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		CreatedAt: now, UpdatedAt: now,
	})

	// Cache primed so the fetch below produces detectable changes (state + comments).
	cacheStore.Save(&ghcache.CacheFile{
		Version: 1,
		Issues: map[string]*ghcache.IssueCacheEntry{
			"owner/repo-1": {CacheEntryBase: ghcache.CacheEntryBase{
				OwnerRepo: "owner/repo", Number: 1, State: "open", CommentCount: 1,
			}},
		},
		PullRequests: map[string]*ghcache.PRCacheEntry{},
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"CLOSED","comments":[{"databaseId":100},{"databaseId":200}]}`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	if len(result.Changes) == 0 {
		t.Fatal("expected sync to detect changes; test setup is wrong")
	}

	evStore := eventlog.NewStore(sessionStore.Dir())
	evs, err := evStore.Tail("owner/repo-1", event.Filter{}, 0)
	if err != nil {
		t.Fatalf("Tail error: %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("expected no event log entries from sync, got %d: %+v", len(evs), evs)
	}
}

func TestSync_PurgesStaleEntries(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		CreatedAt: now, UpdatedAt: now,
	})

	cacheStore.Save(&ghcache.CacheFile{
		Version: 1,
		Issues: map[string]*ghcache.IssueCacheEntry{
			"owner/repo-1": {CacheEntryBase: ghcache.CacheEntryBase{
				OwnerRepo: "owner/repo", Number: 1, State: "open",
			}},
			"owner/repo-99": {CacheEntryBase: ghcache.CacheEntryBase{
				OwnerRepo: "owner/repo", Number: 99, State: "open",
			}},
		},
		PullRequests: map[string]*ghcache.PRCacheEntry{},
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"OPEN","comments":[]}`,
	})

	_, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	cache := cacheStore.Load()
	if _, ok := cache.Issues["owner/repo-99"]; ok {
		t.Error("stale entry owner/repo-99 should have been purged")
	}
	if _, ok := cache.Issues["owner/repo-1"]; !ok {
		t.Error("active entry owner/repo-1 should still exist")
	}
}

func TestSync_IssueDiscoversPR(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		Branch:    "issue/1",
		CreatedAt: now, UpdatedAt: now,
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1":           `{"title":"Bug","state":"OPEN","comments":[]}`,
		"pr-branch:owner/repo:issue/1": `[{"number":10,"title":"Fix bug","state":"OPEN","reviewDecision":"","comments":[],"reviews":[],"statusCheckRollup":[]}]`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	if result.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", result.Fetched)
	}

	// Verify the linked PR is stored in cache
	cache := cacheStore.Load()
	issueEntry := cache.Issues["owner/repo-1"]
	if issueEntry == nil {
		t.Fatal("issue cache entry not found")
	}
	if issueEntry.LinkedPR == nil {
		t.Fatal("LinkedPR should not be nil")
	}
	if issueEntry.LinkedPR.Number != 10 {
		t.Errorf("LinkedPR.Number = %d, want 10", issueEntry.LinkedPR.Number)
	}
	if issueEntry.LinkedPR.Title != "Fix bug" {
		t.Errorf("LinkedPR.Title = %q, want %q", issueEntry.LinkedPR.Title, "Fix bug")
	}
}

func TestSync_IssueLinkedPRDetectsChanges(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		Branch:    "issue/1",
		CreatedAt: now, UpdatedAt: now,
	})

	// Pre-populate cache with issue + linked PR
	cacheStore.Save(&ghcache.CacheFile{
		Version: 1,
		Issues: map[string]*ghcache.IssueCacheEntry{
			"owner/repo-1": {
				CacheEntryBase: ghcache.CacheEntryBase{
					OwnerRepo: "owner/repo", Number: 1, State: "open",
				},
				LinkedPR: &ghcache.PRCacheEntry{
					CacheEntryBase: ghcache.CacheEntryBase{
						OwnerRepo: "owner/repo", Number: 10, State: "open",
					},
					ChecksStatus: "PENDING",
				},
			},
		},
		PullRequests: map[string]*ghcache.PRCacheEntry{},
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1":           `{"title":"Bug","state":"OPEN","comments":[]}`,
		"pr-branch:owner/repo:issue/1": `[{"number":10,"title":"Fix bug","state":"OPEN","reviewDecision":"APPROVED","comments":[],"reviews":[{"databaseId":100}],"statusCheckRollup":[{"name":"ci","status":"completed","conclusion":"failure","state":""}]}]`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	// Should detect CI status change, review decision change, and new reviews
	types := map[ghcache.ChangeType]bool{}
	for _, c := range result.Changes {
		types[c.Type] = true
		// All changes from linked PR should have the PR URL, not the issue URL
		if c.URL != "https://github.com/owner/repo/pull/10" {
			t.Errorf("change %q URL = %q, want PR URL https://github.com/owner/repo/pull/10", c.Type, c.URL)
		}
	}
	if !types[ghcache.ChangeCIStatus] {
		t.Error("missing CI status change from linked PR")
	}
	if !types[ghcache.ChangeReviewDecision] {
		t.Error("missing review decision change from linked PR")
	}
	if !types[ghcache.ChangeNewReviews] {
		t.Error("missing new reviews change from linked PR")
	}
}

func TestSync_IssueNoPROnBranch(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-1", URL: "https://github.com/owner/repo/issues/1",
		URLType: "issue", OwnerRepo: "owner/repo", Number: 1,
		Branch:    "issue/1",
		CreatedAt: now, UpdatedAt: now,
	})

	// No pr-branch response → empty array returned by fake script
	fetcher := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"OPEN","comments":[]}`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	if result.Fetched != 1 {
		t.Errorf("Fetched = %d, want 1", result.Fetched)
	}

	cache := cacheStore.Load()
	issueEntry := cache.Issues["owner/repo-1"]
	if issueEntry == nil {
		t.Fatal("issue cache entry not found")
	}
	if issueEntry.LinkedPR != nil {
		t.Error("LinkedPR should be nil when no PR exists on branch")
	}
}

func TestSync_DetectsNewCommits(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	now := time.Now()

	sessionStore.Put(&domain.Session{
		Name: "owner/repo-2", URL: "https://github.com/owner/repo/pull/2",
		URLType: "pr", OwnerRepo: "owner/repo", Number: 2,
		CreatedAt: now, UpdatedAt: now,
	})

	// Pre-populate cache with old HEAD SHA
	cacheStore.Save(&ghcache.CacheFile{
		Version: 1,
		Issues:  map[string]*ghcache.IssueCacheEntry{},
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-2": {
				CacheEntryBase: ghcache.CacheEntryBase{
					OwnerRepo: "owner/repo", Number: 2, State: "open", CommentCount: 0,
				},
				HeadSHA:     "abc1234567890",
				CommitCount: 3,
			},
		},
	})

	fetcher := newFakeFetcher(t, map[string]string{
		"pr:owner/repo:2": `{"title":"Feature","state":"OPEN","reviewDecision":"","comments":[],"reviews":[],"statusCheckRollup":[],"headRefOid":"def5678901234","commits":[{"oid":"a"},{"oid":"b"},{"oid":"c"},{"oid":"d"},{"oid":"e"}]}`,
	})

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}

	if len(result.Changes) != 1 {
		t.Fatalf("expected 1 change, got %d: %+v", len(result.Changes), result.Changes)
	}
	if result.Changes[0].Type != ghcache.ChangeNewCommits {
		t.Errorf("Type = %q, want %q", result.Changes[0].Type, ghcache.ChangeNewCommits)
	}

	// Verify cache was updated with new HEAD SHA
	cache := cacheStore.Load()
	prEntry := cache.PullRequests["owner/repo-2"]
	if prEntry == nil {
		t.Fatal("PR cache entry not found")
	}
	if prEntry.HeadSHA != "def5678901234" {
		t.Errorf("HeadSHA = %q, want %q", prEntry.HeadSHA, "def5678901234")
	}
	if prEntry.CommitCount != 5 {
		t.Errorf("CommitCount = %d, want 5", prEntry.CommitCount)
	}
}

func TestSync_EmptySessions(t *testing.T) {
	dir := t.TempDir()
	sessionStore := state.NewStore(dir)
	cacheStore := ghcache.NewCacheStore(dir)
	cfg := &config.Config{}
	fetcher := ghcache.NewFetcher()

	result, err := Sync(cfg, sessionStore, cacheStore, fetcher, SyncParams{})
	if err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	if result.Fetched != 0 {
		t.Errorf("Fetched = %d, want 0", result.Fetched)
	}
}

// newFakeFetcher creates a Fetcher backed by a fake gh script.
// keys: "issue:owner/repo:number", "pr:owner/repo:number", or "pr-branch:owner/repo:branch"
func newFakeFetcher(t *testing.T, responses map[string]string) *ghcache.Fetcher {
	t.Helper()
	dir := t.TempDir()

	script := "#!/usr/bin/env bash\nsubcmd=$1; shift\n"
	script += "if [ \"$subcmd\" = \"issue\" ] && [ \"$1\" = \"view\" ]; then\n"
	script += "  number=$2; shift 2; repo=''\n"
	script += "  while [ $# -gt 0 ]; do case \"$1\" in --repo) repo=$2; shift 2;; *) shift;; esac; done\n"
	for k, v := range responses {
		parts := splitFakeKey(k)
		if parts[0] == "issue" {
			script += fmt.Sprintf("  [ \"$repo\" = \"%s\" ] && [ \"$number\" = \"%s\" ] && echo '%s' && exit 0\n", parts[1], parts[2], v)
		}
	}
	script += "  exit 1\nfi\n"
	script += "if [ \"$subcmd\" = \"pr\" ] && [ \"$1\" = \"view\" ]; then\n"
	script += "  number=$2; shift 2; repo=''\n"
	script += "  while [ $# -gt 0 ]; do case \"$1\" in --repo) repo=$2; shift 2;; *) shift;; esac; done\n"
	for k, v := range responses {
		parts := splitFakeKey(k)
		if parts[0] == "pr" {
			script += fmt.Sprintf("  [ \"$repo\" = \"%s\" ] && [ \"$number\" = \"%s\" ] && echo '%s' && exit 0\n", parts[1], parts[2], v)
		}
	}
	script += "  exit 1\nfi\n"
	// Handle: gh pr list --head <branch> --repo <repo> ...
	script += "if [ \"$subcmd\" = \"pr\" ] && [ \"$1\" = \"list\" ]; then\n"
	script += "  shift; head=''; repo=''\n"
	script += "  while [ $# -gt 0 ]; do case \"$1\" in --head) head=$2; shift 2;; --repo) repo=$2; shift 2;; *) shift;; esac; done\n"
	for k, v := range responses {
		parts := splitFakeKey(k)
		if parts[0] == "pr-branch" {
			script += fmt.Sprintf("  [ \"$repo\" = \"%s\" ] && [ \"$head\" = \"%s\" ] && echo '%s' && exit 0\n", parts[1], parts[2], v)
		}
	}
	// Return empty array if no match (no PR found for branch)
	script += "  echo '[]' && exit 0\n"
	script += "fi\n"
	script += "exit 1\n"

	scriptPath := dir + "/gh"
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake gh: %v", err)
	}

	return &ghcache.Fetcher{
		GhCommand: scriptPath,
		NowFunc:   func() time.Time { return time.Now() },
	}
}

// splitFakeKey splits "type:owner/repo:number" into 3 parts.
func splitFakeKey(key string) [3]string {
	var result [3]string
	idx := 0
	start := 0
	for i, c := range key {
		if c == ':' && idx < 2 {
			result[idx] = key[start:i]
			idx++
			start = i + 1
		}
	}
	result[idx] = key[start:]
	return result
}
