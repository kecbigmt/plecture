package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestLayout_RepoDirAndWorktreePath pins the on-disk path convention the
// manager owns: a repository container directly under the worktrees root,
// and one sanitized-branch directory per worktree inside it.
func TestLayout_RepoDirAndWorktreePath(t *testing.T) {
	mgr := NewManager("/roots/worktrees")

	repoDir := mgr.RepoDir("example.test/testowner/testrepo")
	if want := filepath.Join("/roots/worktrees", "example.test", "testowner", "testrepo"); repoDir != want {
		t.Errorf("RepoDir = %q, want %q", repoDir, want)
	}

	tests := []struct {
		name   string
		branch string
		want   string
	}{
		{"plain branch", "topic", "topic"},
		{"slash separated", "item/42", "item-42"},
		{"tag suffix", "item/42+review", "item-42-review"},
		{"colon separated", "ns:topic", "ns-topic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mgr.WorktreePath("example.test/testowner/testrepo", tt.branch)
			if want := filepath.Join(repoDir, tt.want); got != want {
				t.Errorf("WorktreePath = %q, want %q", got, want)
			}
		})
	}
}

// TestLayout_WorktreeExists pins that existence is decided purely by the
// path convention, before and after an acquisition.
func TestLayout_WorktreeExists(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	if mgr.WorktreeExists(ownerRepo, "item/12") {
		t.Fatal("worktree should not exist before acquisition")
	}
	if _, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "item/12", SessionName: "s"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !mgr.WorktreeExists(ownerRepo, "item/12") {
		t.Error("worktree should exist after acquisition")
	}
}

// TestLayout_AddMissingRepoContainer pins the failure path when the
// repository container has never been cloned: acquisition refuses rather
// than creating an empty directory tree.
func TestLayout_AddMissingRepoContainer(t *testing.T) {
	mgr := NewManager(t.TempDir())

	_, err := mgr.Add(context.Background(), AddParams{Repo: "absent/repo", Branch: "item/1", SessionName: "s"})
	if err == nil {
		t.Fatal("expected an error when the repository container is absent")
	}
	if !strings.Contains(err.Error(), "repository not found") {
		t.Errorf("error = %v, want a repository-not-found message", err)
	}
}

// TestLayout_AddDefaultsBaseBranchToBranch pins that omitting the base
// branch makes acquisition treat the target branch as its own base.
func TestLayout_AddDefaultsBaseBranchToBranch(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "item/3", SessionName: "s"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if info.Branch != "item/3" {
		t.Errorf("Branch = %q, want item/3", info.Branch)
	}
	if info.WorktreePath != mgr.WorktreePath(ownerRepo, "item/3") {
		t.Errorf("WorktreePath = %q, want the path-convention location", info.WorktreePath)
	}
	if info.GitDir == "" || info.RepoDir != mgr.RepoDir(ownerRepo) {
		t.Errorf("RepoDir/GitDir = %q/%q", info.RepoDir, info.GitDir)
	}
}
