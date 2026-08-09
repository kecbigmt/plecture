package ghcache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/kecbigmt/plect/contracts/atomicfile"
)

const cacheVersion = 1

// CacheEntryBase holds fields common to both Issue and PR cache entries.
type CacheEntryBase struct {
	OwnerRepo     string    `json:"owner_repo"`
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	State         string    `json:"state"`
	CommentCount  int       `json:"comment_count"`
	LastCommentID int64     `json:"last_comment_id"`
	FetchedAt     time.Time `json:"fetched_at"`
}

// IssueCacheEntry holds cached data for a GitHub Issue.
type IssueCacheEntry struct {
	CacheEntryBase
	// LinkedPR holds cached data for a PR discovered on the same branch.
	// nil if no associated PR exists.
	LinkedPR *PRCacheEntry `json:"linked_pr,omitempty"`
}

// PRCacheEntry holds cached data for a GitHub PR.
type PRCacheEntry struct {
	CacheEntryBase
	Mergeable           string `json:"mergeable,omitempty"`
	ReviewDecision      string `json:"review_decision"`
	ChecksStatus        string `json:"checks_status"`
	ChecksDetail        string `json:"checks_detail"`
	ReviewCount         int    `json:"review_count"`
	LastReviewID        int64  `json:"last_review_id"`
	HeadSHA             string `json:"head_sha,omitempty"`
	CommitCount         int    `json:"commit_count,omitempty"`
	LatestCommitSubject string `json:"latest_commit_subject,omitempty"`
}

// CacheFile is the top-level structure of github_cache.json.
type CacheFile struct {
	Version      int                         `json:"version"`
	LastSynced   time.Time                   `json:"last_synced"`
	Issues       map[string]*IssueCacheEntry `json:"issues"`
	PullRequests map[string]*PRCacheEntry    `json:"pull_requests"`
}

// CacheStore manages loading and saving the GitHub cache file.
type CacheStore struct {
	path string
	mu   sync.Mutex
}

// NewCacheStore creates a CacheStore. If dir is empty, defaults to ~/.local/share/tws.
func NewCacheStore(dir string) *CacheStore {
	if dir == "" {
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			home, _ := os.UserHomeDir()
			dataHome = filepath.Join(home, ".local", "share")
		}
		dir = filepath.Join(dataHome, "tws")
	}
	return &CacheStore{path: filepath.Join(dir, "github_cache.json")}
}

// Load reads the cache file from disk. Returns an empty CacheFile if the file doesn't exist.
func (s *CacheStore) Load() *CacheFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *CacheStore) loadLocked() *CacheFile {
	cf := &CacheFile{
		Version:      cacheVersion,
		Issues:       make(map[string]*IssueCacheEntry),
		PullRequests: make(map[string]*PRCacheEntry),
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return cf
	}

	if err := json.Unmarshal(data, cf); err != nil {
		return &CacheFile{
			Version:      cacheVersion,
			Issues:       make(map[string]*IssueCacheEntry),
			PullRequests: make(map[string]*PRCacheEntry),
		}
	}

	if cf.Issues == nil {
		cf.Issues = make(map[string]*IssueCacheEntry)
	}
	if cf.PullRequests == nil {
		cf.PullRequests = make(map[string]*PRCacheEntry)
	}

	return cf
}

// Save writes the cache file to disk atomically (temp file + rename).
func (s *CacheStore) Save(cf *CacheFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	data, err := json.MarshalIndent(cf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache: %w", err)
	}

	if err := atomicfile.Write(s.path, data); err != nil {
		return fmt.Errorf("failed to write cache file: %w", err)
	}
	return nil
}

// CacheKey returns the cache map key for a session (owner/repo-number).
func CacheKey(ownerRepo string, number int) string {
	return fmt.Sprintf("%s-%d", ownerRepo, number)
}
