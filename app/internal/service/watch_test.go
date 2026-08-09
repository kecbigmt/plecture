package service

import (
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/ghcache"
	"github.com/kecbigmt/plect/app/internal/state"
)

// TestSync_CanBeCalledRepeatedly verifies that Sync can be called multiple times
// (as tws watch does in its polling loop) and correctly detects changes between polls.
func TestSync_CanBeCalledRepeatedly(t *testing.T) {
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

	// First poll: establish baseline
	fetcher1 := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"OPEN","comments":[]}`,
	})
	result1, err := Sync(cfg, sessionStore, cacheStore, fetcher1, SyncParams{})
	if err != nil {
		t.Fatalf("first Sync error: %v", err)
	}
	if result1.Fetched != 1 {
		t.Errorf("first poll: Fetched = %d, want 1", result1.Fetched)
	}

	// Second poll: no changes
	fetcher2 := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"OPEN","comments":[]}`,
	})
	result2, err := Sync(cfg, sessionStore, cacheStore, fetcher2, SyncParams{})
	if err != nil {
		t.Fatalf("second Sync error: %v", err)
	}
	if len(result2.Changes) != 0 {
		t.Errorf("second poll: expected 0 changes, got %d: %+v", len(result2.Changes), result2.Changes)
	}

	// Third poll: state changes → should detect
	fetcher3 := newFakeFetcher(t, map[string]string{
		"issue:owner/repo:1": `{"title":"Bug","state":"CLOSED","comments":[]}`,
	})
	result3, err := Sync(cfg, sessionStore, cacheStore, fetcher3, SyncParams{})
	if err != nil {
		t.Fatalf("third Sync error: %v", err)
	}
	if len(result3.Changes) != 1 {
		t.Fatalf("third poll: expected 1 change, got %d: %+v", len(result3.Changes), result3.Changes)
	}
	if result3.Changes[0].Type != ghcache.ChangeState {
		t.Errorf("third poll: change type = %q, want %q", result3.Changes[0].Type, ghcache.ChangeState)
	}
}
