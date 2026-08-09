package service

import (
	"fmt"
	"os"
	"strings"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/ghcache"
	gh "github.com/kecbigmt/plect/app/internal/github"
	"github.com/kecbigmt/plect/app/internal/hook"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/traceid"
	"github.com/kecbigmt/plect/app/internal/workspace"
)

// SyncParams holds parameters for the Sync operation.
type SyncParams struct {
	// StatusFilter restricts sync to sessions whose cached state matches these values.
	// Empty means all. Uncached sessions are always fetched.
	StatusFilter []string
	// TypeFilter restricts sync to sessions of these URL types ("issue", "pr").
	// Empty means all.
	TypeFilter []string
}

// SyncResult holds the result of a Sync operation.
type SyncResult struct {
	Fetched int              `json:"fetched"`
	Skipped int              `json:"skipped"`
	Changes []ghcache.Change `json:"changes,omitempty"`
}

// Sync fetches GitHub data for tracked sessions, detects changes, and fires hooks.
func Sync(cfg *config.Config, sessionStore *state.Store, cacheStore *ghcache.CacheStore, fetcher *ghcache.Fetcher, params SyncParams) (*SyncResult, error) {
	sessions := sessionStore.All()
	oldCache := cacheStore.Load()
	newCache := &ghcache.CacheFile{
		Version:      oldCache.Version,
		Issues:       make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: make(map[string]*ghcache.PRCacheEntry),
	}

	// Copy existing cache entries (will be overwritten for fetched sessions)
	for k, v := range oldCache.Issues {
		newCache.Issues[k] = v
	}
	for k, v := range oldCache.PullRequests {
		newCache.PullRequests[k] = v
	}

	result := &SyncResult{}
	var allChanges []ghcache.Change

	statusSet := toSet(params.StatusFilter)
	typeSet := toSet(params.TypeFilter)

	for _, session := range sessions {
		key := ghcache.CacheKey(session.OwnerRepo, session.Number)

		// Type filter
		if len(typeSet) > 0 && !typeSet[session.URLType] {
			result.Skipped++
			continue
		}

		// Status filter: check cached state. Uncached sessions always pass.
		if len(statusSet) > 0 {
			cachedState := getCachedState(oldCache, session.URLType, key)
			if cachedState != "" && !statusSet[cachedState] {
				result.Skipped++
				continue
			}
		}

		changes, err := syncSession(session, key, oldCache, newCache, fetcher)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: sync failed for %s: %v\n", session.Name, err)
			result.Skipped++
			continue
		}

		result.Fetched++
		allChanges = append(allChanges, changes...)
	}

	result.Changes = allChanges

	// Purge cache entries for sessions that no longer exist
	newCache = purgeStaleEntries(newCache, sessions)

	newCache.LastSynced = fetcher.Now()
	if err := cacheStore.Save(newCache); err != nil {
		return nil, fmt.Errorf("failed to save cache: %w", err)
	}

	// Record each change in the durable event log and fire hooks.
	if len(allChanges) > 0 {
		processChanges(cfg, sessionStore, allChanges)
	}

	return result, nil
}

func syncSession(session *domain.Session, key string, oldCache, newCache *ghcache.CacheFile, fetcher *ghcache.Fetcher) ([]ghcache.Change, error) {
	if session.URLType == string(gh.URLTypePR) {
		return syncPR(session, key, oldCache, newCache, fetcher)
	}
	return syncIssue(session, key, oldCache, newCache, fetcher)
}

func syncIssue(session *domain.Session, key string, oldCache, newCache *ghcache.CacheFile, fetcher *ghcache.Fetcher) ([]ghcache.Change, error) {
	entry, err := fetcher.FetchIssue(session.OwnerRepo, session.Number)
	if err != nil {
		return nil, err
	}

	// Auto-discover PRs on the session's branch
	if session.Branch != "" {
		prEntry, err := fetcher.FetchPRByBranch(session.OwnerRepo, session.Branch)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to discover PR for %s (branch %s): %v\n", session.Name, session.Branch, err)
		} else if prEntry != nil {
			entry.LinkedPR = prEntry
		}
	}

	newCache.Issues[key] = entry
	delete(newCache.PullRequests, key) // in case type changed

	old := oldCache.Issues[key]
	return ghcache.DiffIssue(session.Name, old, entry), nil
}

func syncPR(session *domain.Session, key string, oldCache, newCache *ghcache.CacheFile, fetcher *ghcache.Fetcher) ([]ghcache.Change, error) {
	entry, err := fetcher.FetchPR(session.OwnerRepo, session.Number)
	if err != nil {
		return nil, err
	}
	newCache.PullRequests[key] = entry
	delete(newCache.Issues, key) // in case type changed

	old := oldCache.PullRequests[key]
	return ghcache.DiffPR(session.Name, old, entry), nil
}

func getCachedState(cache *ghcache.CacheFile, urlType, key string) string {
	if urlType == string(gh.URLTypePR) {
		if entry, ok := cache.PullRequests[key]; ok {
			return entry.State
		}
	} else {
		if entry, ok := cache.Issues[key]; ok {
			return entry.State
		}
	}
	return "" // uncached
}

func purgeStaleEntries(cache *ghcache.CacheFile, sessions map[string]*domain.Session) *ghcache.CacheFile {
	activeKeys := make(map[string]bool)
	for _, s := range sessions {
		activeKeys[ghcache.CacheKey(s.OwnerRepo, s.Number)] = true
	}

	for k := range cache.Issues {
		if !activeKeys[k] {
			delete(cache.Issues, k)
		}
	}
	for k := range cache.PullRequests {
		if !activeKeys[k] {
			delete(cache.PullRequests, k)
		}
	}

	return cache
}

// processChanges fires the post_sync_change hook for each detected change
// when one is configured. github event publishing is owned by github-watcher
// (subscription-based polling → bus publish with stream_id + resource); legacy
// ghcache sync no longer appends to the event log to avoid double emission.
func processChanges(cfg *config.Config, sessionStore *state.Store, changes []ghcache.Change) {
	mgr := workspace.NewManager(cfg.WorktreesRoot)
	for _, change := range changes {
		session := sessionStore.Get(change.SessionName)
		if session == nil {
			continue
		}

		postSync := cfg.MergedHooks(mgr.RepoDir(session.OwnerRepo)).PostSyncChange
		if len(postSync) == 0 {
			continue
		}

		changeURL := session.URL
		if change.URL != "" {
			changeURL = change.URL
		}
		tid := traceid.Generate()

		hookVars := hook.Vars{
			SessionName:      change.SessionName,
			WorktreePath:     session.WorktreePath,
			URL:              changeURL,
			OwnerRepo:        session.OwnerRepo,
			Branch:           session.Branch,
			TraceID:          tid,
			ConversationJSON: conversationJSON(session.Conversation),
			ChangeSummary:    change.Summary,
			ChangeType:       string(change.Type),
		}

		if err := hook.Run(hook.PostSyncChange, postSync, hookVars); err != nil {
			fmt.Fprintf(os.Stderr, "warning: post_sync_change hook error for %s: %v\n", change.SessionName, err)
		}
	}
}

func toSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]bool, len(items))
	for _, item := range items {
		for _, s := range strings.Split(item, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				set[s] = true
			}
		}
	}
	return set
}
