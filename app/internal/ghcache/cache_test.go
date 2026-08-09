package ghcache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCacheStore(t *testing.T) *CacheStore {
	t.Helper()
	return NewCacheStore(t.TempDir())
}

func TestCacheStore_LoadEmpty(t *testing.T) {
	store := testCacheStore(t)
	cf := store.Load()

	if cf.Version != cacheVersion {
		t.Errorf("Version = %d, want %d", cf.Version, cacheVersion)
	}
	if len(cf.Issues) != 0 {
		t.Errorf("Issues should be empty, got %d", len(cf.Issues))
	}
	if len(cf.PullRequests) != 0 {
		t.Errorf("PullRequests should be empty, got %d", len(cf.PullRequests))
	}
}

func TestCacheStore_SaveAndLoad(t *testing.T) {
	store := testCacheStore(t)
	now := time.Now().Truncate(time.Second)

	cf := &CacheFile{
		Version:    cacheVersion,
		LastSynced: now,
		Issues: map[string]*IssueCacheEntry{
			"owner/repo-1": {
				CacheEntryBase: CacheEntryBase{
					OwnerRepo:    "owner/repo",
					Number:       1,
					Title:        "Fix bug",
					State:        "open",
					CommentCount: 3,
					FetchedAt:    now,
				},
			},
		},
		PullRequests: map[string]*PRCacheEntry{
			"owner/repo-2": {
				CacheEntryBase: CacheEntryBase{
					OwnerRepo:    "owner/repo",
					Number:       2,
					Title:        "Add feature",
					State:        "open",
					CommentCount: 1,
					FetchedAt:    now,
				},
				ReviewDecision: "APPROVED",
				ChecksStatus:   "SUCCESS",
			},
		},
	}

	if err := store.Save(cf); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded := store.Load()
	if loaded.Version != cacheVersion {
		t.Errorf("Version = %d, want %d", loaded.Version, cacheVersion)
	}
	if !loaded.LastSynced.Equal(now) {
		t.Errorf("LastSynced = %v, want %v", loaded.LastSynced, now)
	}

	issue, ok := loaded.Issues["owner/repo-1"]
	if !ok {
		t.Fatal("missing issue owner/repo-1")
	}
	if issue.Title != "Fix bug" {
		t.Errorf("issue Title = %q, want %q", issue.Title, "Fix bug")
	}
	if issue.State != "open" {
		t.Errorf("issue State = %q, want %q", issue.State, "open")
	}

	pr, ok := loaded.PullRequests["owner/repo-2"]
	if !ok {
		t.Fatal("missing PR owner/repo-2")
	}
	if pr.Title != "Add feature" {
		t.Errorf("pr Title = %q, want %q", pr.Title, "Add feature")
	}
	if pr.ReviewDecision != "APPROVED" {
		t.Errorf("pr ReviewDecision = %q, want %q", pr.ReviewDecision, "APPROVED")
	}
	if pr.ChecksStatus != "SUCCESS" {
		t.Errorf("pr ChecksStatus = %q, want %q", pr.ChecksStatus, "SUCCESS")
	}
}

func TestCacheStore_LoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "github_cache.json")
	os.WriteFile(path, []byte("not json"), 0644)

	store := NewCacheStore(dir)
	cf := store.Load()

	if cf.Version != cacheVersion {
		t.Errorf("Version = %d, want %d", cf.Version, cacheVersion)
	}
	if len(cf.Issues) != 0 {
		t.Error("Issues should be empty for corrupt file")
	}
}

func TestCacheKey(t *testing.T) {
	key := CacheKey("owner/repo", 42)
	if key != "owner/repo-42" {
		t.Errorf("CacheKey = %q, want %q", key, "owner/repo-42")
	}
}
