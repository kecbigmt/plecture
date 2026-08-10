package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin down behavior that predates context plumbing: they must
// keep passing unchanged once GetWorktreeStatus gains a context.Context
// parameter.

func TestGetWorktreeStatus_NonexistentPath(t *testing.T) {
	_, err := GetWorktreeStatus(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected error for a nonexistent worktree path, got nil")
	}
}

func TestRunGitCapture_PreservesStderrOnFailure(t *testing.T) {
	mgr := NewManager(t.TempDir())
	dir := t.TempDir()
	stderr, err := mgr.runGitCapture(context.Background(), dir, "status")
	if err == nil {
		t.Fatal("expected error running git in a non-repo directory, got nil")
	}
	if !strings.Contains(stderr, "not a git repository") {
		t.Errorf("stderr = %q, want it to mention %q", stderr, "not a git repository")
	}
}

func TestManager_Add_WorktreeAddErrorPreservesHint(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	if _, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/7", SessionName: sessionID(ownerRepo, 7)}); err != nil {
		t.Fatalf("first Add() error = %v", err)
	}

	// Manually check the worktree out again outside sennit's own bookkeeping
	// so a second Add() collides with git's own "already checked out" guard.
	repoDir := mgr.RepoDir(ownerRepo)
	wtPath := mgr.WorktreePath(ownerRepo, "issue/7")
	gitDir, err := mgr.FindGitDir(repoDir, wtPath)
	if err != nil {
		t.Fatalf("FindGitDir() error = %v", err)
	}
	if err := os.RemoveAll(wtPath); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if err := mgr.runGit(context.Background(), gitDir, "worktree", "prune"); err != nil {
		t.Fatalf("worktree prune error = %v", err)
	}
	if _, err := mgr.runGitCapture(context.Background(), gitDir, "worktree", "add", filepath.Join(t.TempDir(), "other"), "issue/7"); err != nil {
		t.Fatalf("setup worktree add error = %v", err)
	}

	_, err = mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/7", SessionName: sessionID(ownerRepo, 7)})
	if err == nil {
		t.Fatal("expected error re-adding an already checked out branch, got nil")
	}
	if !strings.Contains(err.Error(), "already checked out") && !strings.Contains(err.Error(), "Hint:") {
		t.Errorf("error = %v, want the already-checked-out hint", err)
	}
}
