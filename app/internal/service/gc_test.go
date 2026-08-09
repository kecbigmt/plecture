package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/ghcache"
	contract "github.com/kecbigmt/plect/contracts/state"
)

func TestClassifySession_WorktreeMissing(t *testing.T) {
	s := &domain.Session{
		Name:         "owner/repo-1",
		URL:          "https://github.com/owner/repo/issues/1",
		URLType:      "issue",
		OwnerRepo:    "owner/repo",
		Number:       1,
		WorktreePath: "/nonexistent/path",
	}
	cache := &ghcache.CacheFile{
		Issues:       make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: make(map[string]*ghcache.PRCacheEntry),
	}

	entry, _ := classifySession(s, cache, nil, map[string]*domain.Session{s.Name: s})
	if entry == nil {
		t.Fatal("expected GC entry for missing worktree")
	}
	if entry.Action != GCActionDelete {
		t.Errorf("Action = %q, want %q", entry.Action, GCActionDelete)
	}
	if entry.Reason != GCReasonWorktreeMissing {
		t.Errorf("Reason = %q, want %q", entry.Reason, GCReasonWorktreeMissing)
	}
}

func TestClassifySession_MergedClean(t *testing.T) {
	// Create a clean git worktree (temp dir with git init)
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "wt")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatal(err)
	}
	// Initialize a git repo so GetWorktreeStatus works
	runCmd(t, wtPath, "git", "init")
	runCmd(t, wtPath, "git", "commit", "--allow-empty", "-m", "init")

	s := &domain.Session{
		Name:         "owner/repo-10",
		URL:          "https://github.com/owner/repo/pull/10",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       10,
		WorktreePath: wtPath,
	}
	cache := &ghcache.CacheFile{
		Issues: make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-10": {
				CacheEntryBase: ghcache.CacheEntryBase{
					State: "merged",
				},
			},
		},
	}

	entry, _ := classifySession(s, cache, nil, map[string]*domain.Session{s.Name: s})
	if entry == nil {
		t.Fatal("expected GC entry for merged+clean session")
	}
	if entry.Action != GCActionDelete {
		t.Errorf("Action = %q, want %q", entry.Action, GCActionDelete)
	}
	if entry.Reason != GCReasonMergedClean {
		t.Errorf("Reason = %q, want %q", entry.Reason, GCReasonMergedClean)
	}
}

// runtimeTaskDefs returns task definitions declaring a run-scoped healthcheck
// (via cmd, so the test controls pass/fail without depending on any real
// runtime), and a matching produced task state for the session.
func runtimeTaskDefs(cmd string) map[string]config.TaskDefinition {
	return map[string]config.TaskDefinition{
		"runtime": {Scope: "run", Healthcheck: cmd},
	}
}

func producedRuntimeTask() *contract.TaskState {
	return &contract.TaskState{Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced}
}

func TestClassifySession_MergedDirty_Unhealthy(t *testing.T) {
	// Create a dirty worktree
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "wt")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, wtPath, "git", "init")
	runCmd(t, wtPath, "git", "commit", "--allow-empty", "-m", "init")
	// Create an untracked file to make it dirty
	os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("dirty"), 0644)

	s := &domain.Session{
		Name:         "owner/repo-11",
		URL:          "https://github.com/owner/repo/pull/11",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       11,
		WorktreePath: wtPath,
		Tasks:        map[string]*contract.TaskState{"runtime": producedRuntimeTask()},
	}
	cache := &ghcache.CacheFile{
		Issues: make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-11": {
				CacheEntryBase: ghcache.CacheEntryBase{
					State: "merged",
				},
			},
		},
	}

	// declared healthcheck fails → unhealthy + merged + dirty → manual
	entry, _ := classifySession(s, cache, runtimeTaskDefs("false"), map[string]*domain.Session{s.Name: s})
	if entry == nil {
		t.Fatal("expected GC entry for merged+dirty session with unhealthy runtime")
	}
	if entry.Action != GCActionManual {
		t.Errorf("Action = %q, want %q", entry.Action, GCActionManual)
	}
	if entry.Reason != GCReasonUnhealthy {
		t.Errorf("Reason = %q, want %q", entry.Reason, GCReasonUnhealthy)
	}
}

func TestClassifySession_NotMerged_Unhealthy(t *testing.T) {
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "wt")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, wtPath, "git", "init")
	runCmd(t, wtPath, "git", "commit", "--allow-empty", "-m", "init")

	s := &domain.Session{
		Name:         "owner/repo-12",
		URL:          "https://github.com/owner/repo/pull/12",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       12,
		WorktreePath: wtPath,
		Tasks:        map[string]*contract.TaskState{"runtime": producedRuntimeTask()},
	}
	cache := &ghcache.CacheFile{
		Issues: make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-12": {
				CacheEntryBase: ghcache.CacheEntryBase{
					State: "open",
				},
			},
		},
	}

	// declared healthcheck fails + not merged + clean → manual
	entry, _ := classifySession(s, cache, runtimeTaskDefs("false"), map[string]*domain.Session{s.Name: s})
	if entry == nil {
		t.Fatal("expected GC entry for open session with unhealthy runtime")
	}
	if entry.Action != GCActionManual {
		t.Errorf("Action = %q, want %q", entry.Action, GCActionManual)
	}
	if entry.Reason != GCReasonUnhealthy {
		t.Errorf("Reason = %q, want %q", entry.Reason, GCReasonUnhealthy)
	}
}

func TestClassifySession_Healthy_PassingHealthcheck(t *testing.T) {
	tmpDir := t.TempDir()
	wtPath := filepath.Join(tmpDir, "wt")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, wtPath, "git", "init")
	runCmd(t, wtPath, "git", "commit", "--allow-empty", "-m", "init")

	s := &domain.Session{
		Name:         "owner/repo-13",
		URL:          "https://github.com/owner/repo/pull/13",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       13,
		WorktreePath: wtPath,
		Tasks:        map[string]*contract.TaskState{"runtime": producedRuntimeTask()},
	}
	cache := &ghcache.CacheFile{
		Issues: make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-13": {CacheEntryBase: ghcache.CacheEntryBase{State: "open"}},
		},
	}

	// declared healthcheck passes + not merged → healthy, skipped by GC
	entry, _ := classifySession(s, cache, runtimeTaskDefs("true"), map[string]*domain.Session{s.Name: s})
	if entry != nil {
		t.Errorf("expected nil entry for healthy session, got %+v", entry)
	}
}

func TestIsMerged_PR(t *testing.T) {
	cache := &ghcache.CacheFile{
		Issues: make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-5": {CacheEntryBase: ghcache.CacheEntryBase{State: "merged"}},
			"owner/repo-6": {CacheEntryBase: ghcache.CacheEntryBase{State: "open"}},
		},
	}

	tests := []struct {
		name    string
		session *domain.Session
		want    bool
	}{
		{
			name:    "merged PR",
			session: &domain.Session{OwnerRepo: "owner/repo", Number: 5, URLType: "pr"},
			want:    true,
		},
		{
			name:    "open PR",
			session: &domain.Session{OwnerRepo: "owner/repo", Number: 6, URLType: "pr"},
			want:    false,
		},
		{
			name:    "PR not in cache",
			session: &domain.Session{OwnerRepo: "owner/repo", Number: 99, URLType: "pr"},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMerged(tt.session, cache)
			if got != tt.want {
				t.Errorf("isMerged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsMerged_Issue(t *testing.T) {
	cache := &ghcache.CacheFile{
		Issues: map[string]*ghcache.IssueCacheEntry{
			"owner/repo-1": {
				CacheEntryBase: ghcache.CacheEntryBase{State: "closed"},
				LinkedPR:       &ghcache.PRCacheEntry{CacheEntryBase: ghcache.CacheEntryBase{State: "merged"}},
			},
			"owner/repo-2": {
				CacheEntryBase: ghcache.CacheEntryBase{State: "open"},
			},
			"owner/repo-3": {
				CacheEntryBase: ghcache.CacheEntryBase{State: "closed"},
				LinkedPR:       &ghcache.PRCacheEntry{CacheEntryBase: ghcache.CacheEntryBase{State: "open"}},
			},
		},
		PullRequests: make(map[string]*ghcache.PRCacheEntry),
	}

	tests := []struct {
		name    string
		session *domain.Session
		want    bool
	}{
		{
			name:    "issue with merged linked PR",
			session: &domain.Session{OwnerRepo: "owner/repo", Number: 1, URLType: "issue"},
			want:    true,
		},
		{
			name:    "issue without linked PR",
			session: &domain.Session{OwnerRepo: "owner/repo", Number: 2, URLType: "issue"},
			want:    false,
		},
		{
			name:    "issue with open linked PR",
			session: &domain.Session{OwnerRepo: "owner/repo", Number: 3, URLType: "issue"},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMerged(tt.session, cache)
			if got != tt.want {
				t.Errorf("isMerged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGC_DryRun_NoStale(t *testing.T) {
	// No sessions → no entries
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	store := testStore(t)

	result, err := GC(cfg, store, GCParams{Execute: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(result.Entries))
	}
}

func TestGC_DryRun_WorktreeMissing(t *testing.T) {
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	store := testStore(t)

	now := time.Now()
	store.Put(&domain.Session{
		Name:         "owner/repo-1",
		URL:          "https://github.com/owner/repo/issues/1",
		URLType:      "issue",
		OwnerRepo:    "owner/repo",
		Number:       1,
		WorktreePath: "/nonexistent/path",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	result, err := GC(cfg, store, GCParams{Execute: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].Action != GCActionDelete {
		t.Errorf("Action = %q, want %q", result.Entries[0].Action, GCActionDelete)
	}
	if result.Entries[0].Deleted {
		t.Error("expected Deleted=false in dry-run")
	}

	// Verify session was NOT deleted (dry-run)
	if store.Get("owner/repo-1") == nil {
		t.Error("session should not be deleted in dry-run")
	}
}

// TestGC_ExecuteWorktreeMissing_SkipsWhenChildrenExist covers the guard on
// executeGCDelete's direct store.Delete call: a parent with children must
// be skipped (reported, not silently orphaning) so a later pass — once the
// children are gone — can collect it (leaf-first over multiple gc runs).
func TestGC_ExecuteWorktreeMissing_SkipsWhenChildrenExist(t *testing.T) {
	cfg := &config.Config{WorktreesRoot: t.TempDir()}
	store := testStore(t)

	now := time.Now()
	store.Put(&domain.Session{
		Name:         "owner/repo-1",
		URL:          "https://github.com/owner/repo/issues/1",
		URLType:      "issue",
		OwnerRepo:    "owner/repo",
		Number:       1,
		WorktreePath: "/nonexistent/path",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	store.Put(&domain.Session{
		Name:          "owner/repo-2",
		URL:           "https://github.com/owner/repo/issues/2",
		URLType:       "issue",
		OwnerRepo:     "owner/repo",
		Number:        2,
		ParentSession: "owner/repo-1",
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	result, err := GC(cfg, store, GCParams{Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	// Both sessions lack a worktree, so both classify as delete candidates:
	// the child (leaf) is collected as before, while the parent is skipped
	// because — at classification time — it still has a child.
	if len(result.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result.Entries))
	}
	var parentEntry *GCEntry
	for i := range result.Entries {
		if result.Entries[i].SessionName == "owner/repo-1" {
			parentEntry = &result.Entries[i]
		}
	}
	if parentEntry == nil {
		t.Fatal("expected an entry for the parent session")
	}
	if parentEntry.Deleted {
		t.Error("expected parent Deleted=false when it still has a child")
	}
	if joined := strings.Join(parentEntry.DeleteWarnings, "|"); !strings.Contains(joined, "owner/repo-2") {
		t.Fatalf("expected child session name in parent's DeleteWarnings, got %q", joined)
	}

	if store.Get("owner/repo-1") == nil {
		t.Error("parent session should not be deleted while it has children")
	}
	if store.Get("owner/repo-2") != nil {
		t.Error("expected the childless leaf child to still be collected as before")
	}
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func cleanGitWorktree(t *testing.T) string {
	t.Helper()
	wtPath := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wtPath, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, wtPath, "git", "init")
	runCmd(t, wtPath, "git", "commit", "--allow-empty", "-m", "init")
	return wtPath
}

func TestClassifySession_WorkflowDoneWhenNoLongerDrivesGC(t *testing.T) {
	wtPath := cleanGitWorktree(t)
	s := &domain.Session{
		Name:         "owner/repo-20",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       20,
		WorktreePath: wtPath,
		Workflow:     "wf",
	}
	cache := &ghcache.CacheFile{
		Issues: make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-20": {CacheEntryBase: ghcache.CacheEntryBase{State: "merged"}},
		},
	}

	entry, warn := classifySession(s, cache, nil, map[string]*domain.Session{s.Name: s})
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
	if entry == nil || entry.Action != GCActionDelete || entry.Reason != GCReasonMergedClean {
		t.Fatalf("workflow.done_when must not be the GC reason, got %+v", entry)
	}
}

func TestClassifySession_NoTaskDoneWhenUsesMergedFallback(t *testing.T) {
	wtPath := cleanGitWorktree(t)
	s := &domain.Session{
		Name:         "owner/repo-21",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       21,
		WorktreePath: wtPath,
		Workflow:     "wf",
	}
	cache := &ghcache.CacheFile{
		Issues: make(map[string]*ghcache.IssueCacheEntry),
		PullRequests: map[string]*ghcache.PRCacheEntry{
			"owner/repo-21": {CacheEntryBase: ghcache.CacheEntryBase{State: "merged"}},
		},
	}

	entry, warn := classifySession(s, cache, nil, map[string]*domain.Session{s.Name: s})
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
	if entry == nil || entry.Action != GCActionDelete || entry.Reason != GCReasonMergedClean {
		t.Fatalf("no-task sessions should use merged fallback, got %+v", entry)
	}
}

func TestGC_ExecuteDone_DestroysViaTaskCleanup(t *testing.T) {
	store := testStore(t)
	worktreesRoot := t.TempDir()

	// Real layout so destroy's worktree removal works:
	// <root>/github.com/owner/repo/{main, issue-30(registered worktree)}
	repoDir := filepath.Join(worktreesRoot, "github.com", "owner", "repo")
	mainDir := filepath.Join(repoDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, mainDir, "git", "init", "-b", "main")
	runCmd(t, mainDir, "git", "commit", "--allow-empty", "-m", "init")
	runCmd(t, mainDir, "git", "worktree", "add", "-b", "issue-30", filepath.Join(repoDir, "issue-30"))
	wtPath := filepath.Join(repoDir, "issue-30")

	marker := filepath.Join(t.TempDir(), "cleaned")

	cfg := writeWorkflowFixture(t, worktreesRoot, "wf",
		[]taskFixture{{id: "touchy", scope: "session", setup: "echo '{}'", cleanup: "touch " + marker, extra: "[[done_when.all]]\ncheck = \"done\"\neq = \"yes\""}},
		[]nodeFixture{{id: "touchy"}})

	now := time.Now()
	store.Put(&domain.Session{
		Name:         "owner/repo-30",
		URL:          "https://github.com/owner/repo/pull/30",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       30,
		WorktreePath: wtPath,
		Workflow:     "wf",
		Tasks: map[string]*contract.TaskState{
			"touchy": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Outputs: map[string]any{"done": "yes"}},
			contract.WorkflowPseudoNodeID: {
				Scope:   contract.TaskScopeSession,
				Status:  contract.TaskStatusProduced,
				Outputs: map[string]any{"pr_state": "merged"},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})

	if err := os.MkdirAll(filepath.Join(cfg.BaseDir, "providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "providers", "wf.toml"), []byte("setup = \"echo '{\\\"workdir\\\":\\\"/tmp/x\\\"}'\"\ncleanup = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(cfg.BaseDir, "workflows", "wf.toml")
	existing, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	content := "provider = \"wf\"\n" + string(existing)
	if err := os.WriteFile(wfPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := GC(cfg, store, GCParams{Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %+v", result.Entries)
	}
	e := result.Entries[0]
	if e.Reason != GCReasonDone {
		t.Errorf("Reason = %q, want done", e.Reason)
	}
	if !e.Deleted {
		t.Errorf("expected Deleted=true, warnings: %v", e.DeleteWarnings)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("task cleanup did not run — GC must tear down through the destroy path")
	}
	if store.Get("owner/repo-30") != nil {
		t.Error("state entry should be deleted")
	}
}

// TestGC_ExecuteDone_SkipsWhenChildrenExist covers the guard on the
// executeGCDestroy path (done session, workflow present): it routes through
// Destroy(Force: false), which now rejects destroying a session with
// children, so GC must report it as skipped rather than orphan the child.
func TestGC_ExecuteDone_SkipsWhenChildrenExist(t *testing.T) {
	store := testStore(t)
	worktreesRoot := t.TempDir()

	repoDir := filepath.Join(worktreesRoot, "github.com", "owner", "repo")
	mainDir := filepath.Join(repoDir, "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	runCmd(t, mainDir, "git", "init", "-b", "main")
	runCmd(t, mainDir, "git", "commit", "--allow-empty", "-m", "init")
	runCmd(t, mainDir, "git", "worktree", "add", "-b", "issue-30", filepath.Join(repoDir, "issue-30"))
	wtPath := filepath.Join(repoDir, "issue-30")
	runCmd(t, mainDir, "git", "worktree", "add", "-b", "issue-31", filepath.Join(repoDir, "issue-31"))
	childWtPath := filepath.Join(repoDir, "issue-31")

	marker := filepath.Join(t.TempDir(), "cleaned")

	cfg := writeWorkflowFixture(t, worktreesRoot, "wf",
		[]taskFixture{{id: "touchy", scope: "session", setup: "echo '{}'", cleanup: "touch " + marker, extra: "[[done_when.all]]\ncheck = \"done\"\neq = \"yes\""}},
		[]nodeFixture{{id: "touchy"}})

	now := time.Now()
	store.Put(&domain.Session{
		Name:         "owner/repo-30",
		URL:          "https://github.com/owner/repo/pull/30",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       30,
		WorktreePath: wtPath,
		Workflow:     "wf",
		Tasks: map[string]*contract.TaskState{
			"touchy": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, Outputs: map[string]any{"done": "yes"}},
			contract.WorkflowPseudoNodeID: {
				Scope:   contract.TaskScopeSession,
				Status:  contract.TaskStatusProduced,
				Outputs: map[string]any{"pr_state": "merged"},
			},
		},
		CreatedAt: now,
		UpdatedAt: now,
	})
	// Child has its own (clean, no-workflow) worktree so it is not itself a GC
	// candidate — classifySession reports it healthy-but-tmux-dead (manual),
	// isolating this test to the parent's own skip-when-children behavior.
	store.Put(&domain.Session{
		Name:          "owner/repo-31",
		URL:           "https://github.com/owner/repo/issues/31",
		URLType:       "issue",
		OwnerRepo:     "owner/repo",
		Number:        31,
		WorktreePath:  childWtPath,
		ParentSession: "owner/repo-30",
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	if err := os.MkdirAll(filepath.Join(cfg.BaseDir, "providers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "providers", "wf.toml"), []byte("setup = \"echo '{\\\"workdir\\\":\\\"/tmp/x\\\"}'\"\ncleanup = \"true\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(cfg.BaseDir, "workflows", "wf.toml")
	existing, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	content := "provider = \"wf\"\n" + string(existing)
	if err := os.WriteFile(wfPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := GC(cfg, store, GCParams{Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	var parentEntry *GCEntry
	for i := range result.Entries {
		if result.Entries[i].SessionName == "owner/repo-30" {
			parentEntry = &result.Entries[i]
		}
	}
	if parentEntry == nil {
		t.Fatalf("expected an entry for owner/repo-30, got %+v", result.Entries)
	}
	if parentEntry.Deleted {
		t.Errorf("expected Deleted=false when the session still has a child, warnings: %v", parentEntry.DeleteWarnings)
	}
	if joined := strings.Join(parentEntry.DeleteWarnings, "|"); !strings.Contains(joined, "owner/repo-31") {
		t.Fatalf("expected child session name in DeleteWarnings, got %q", joined)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("task cleanup must not run when destroy is blocked by existing children")
	}
	if store.Get("owner/repo-30") == nil {
		t.Error("parent state entry should not be deleted while it has children")
	}
	if child := store.Get("owner/repo-31"); child == nil || child.ParentSession != "owner/repo-30" {
		t.Error("child session must remain attached to its parent")
	}
}

// A session whose frozen workflow TOML was renamed/removed must never be
// reported as deletable: the destroy path can't build a plan for it, so a
// "delete" verdict would be one that execute cannot honor.
func TestGC_MissingFrozenWorkflowIsManual(t *testing.T) {
	store := testStore(t)
	// Config with no workflows on disk at all.
	cfg := &config.Config{WorktreesRoot: t.TempDir(), BaseDir: t.TempDir()}

	wtPath := cleanGitWorktree(t)
	now := time.Now()
	store.Put(&domain.Session{
		Name:         "owner/repo-40",
		URL:          "https://github.com/owner/repo/pull/40",
		URLType:      "pr",
		OwnerRepo:    "owner/repo",
		Number:       40,
		WorktreePath: wtPath,
		Workflow:     "renamed-away",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	// Cache says merged — under the legacy fallback this would read deletable.
	cache := ghcache.NewCacheStore(t.TempDir())
	_ = cache

	result, err := GC(cfg, store, GCParams{Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %+v", result.Entries)
	}
	e := result.Entries[0]
	if e.Action != GCActionManual || e.Reason != GCReasonWorkflowMissing {
		t.Errorf("got action=%q reason=%q, want manual/workflow_missing", e.Action, e.Reason)
	}
	if len(result.Warnings) == 0 {
		t.Error("expected a warning pointing at the missing workflow")
	}
	if store.Get("owner/repo-40") == nil {
		t.Error("session must survive — nothing can run its cleanups")
	}
}

// Worktree-missing must win over workflow-missing: the reconcile path needs
// no workflow plan, and for repo-local workflows the TOML lives inside the
// worktree — once it's gone the workflow is unloadable by definition, so a
// manual verdict would strand the stale state forever (review follow-up).
func TestGC_WorktreeMissingWinsOverMissingWorkflow(t *testing.T) {
	store := testStore(t)
	cfg := &config.Config{WorktreesRoot: t.TempDir(), BaseDir: t.TempDir()}

	now := time.Now()
	store.Put(&domain.Session{
		Name:         "owner/repo-41",
		URL:          "https://github.com/owner/repo/issues/41",
		URLType:      "issue",
		OwnerRepo:    "owner/repo",
		Number:       41,
		WorktreePath: "/nonexistent/owner/repo/issue-41",
		Workflow:     "repo-local-workflow",
		CreatedAt:    now,
		UpdatedAt:    now,
	})

	result, err := GC(cfg, store, GCParams{Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %+v", result.Entries)
	}
	e := result.Entries[0]
	if e.Action != GCActionDelete || e.Reason != GCReasonWorktreeMissing {
		t.Errorf("got action=%q reason=%q, want delete/worktree_missing", e.Action, e.Reason)
	}
	if !e.Deleted {
		t.Errorf("expected reconcile to delete, warnings: %v", e.DeleteWarnings)
	}
	if store.Get("owner/repo-41") != nil {
		t.Error("stale state entry must be reconciled away even without a loadable workflow")
	}
}
