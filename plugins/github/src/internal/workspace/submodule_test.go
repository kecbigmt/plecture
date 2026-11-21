package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// addWorktreeWithSubmodule returns a registered worktree whose superproject
// records one submodule at vendor/sub, initialized inside the worktree the
// way a submodule-init setup step leaves it.
func addWorktreeWithSubmodule(t *testing.T, branch string, number int) (*Manager, string, *WorkspaceInfo) {
	t.Helper()
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	subDir := filepath.Join(t.TempDir(), "sub")
	runGitT(t, t.TempDir(), "init", "-b", "main", subDir)
	runGitT(t, subDir, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "first")
	if err := os.WriteFile(filepath.Join(subDir, "tracked.txt"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitT(t, subDir, "add", "tracked.txt")
	runGitT(t, subDir, "-c", "user.email=test@test.com", "-c", "user.name=Test", "commit", "-m", "second")

	mainDir := filepath.Join(mgr.RepoDir(ownerRepo), "main")
	runGitT(t, mainDir, "-c", "protocol.file.allow=always", "submodule", "add", subDir, "vendor/sub")
	runGitT(t, mainDir, "commit", "-m", "add submodule")
	runGitT(t, mainDir, "push", "origin", "main")

	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: branch, SessionName: sessionID(ownerRepo, number)})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	runGitT(t, info.WorktreePath, "-c", "protocol.file.allow=always", "submodule", "update", "--init")
	return mgr, ownerRepo, info
}

func TestRemove_CleanWorktreeWithSubmoduleSucceedsWithoutForce(t *testing.T) {
	mgr, ownerRepo, info := addWorktreeWithSubmodule(t, "issue/60", 60)

	if err := mgr.Remove(context.Background(), ownerRepo, "issue/60", false, false); err != nil {
		t.Fatalf("Remove of a clean worktree with a submodule should succeed, got: %v", err)
	}
	if _, err := os.Stat(info.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("expected worktree directory to be removed, stat err: %v", err)
	}
	gitDir, err := mgr.FindGitDir(mgr.RepoDir(ownerRepo))
	if err != nil {
		t.Fatal(err)
	}
	registered, err := mgr.isRegisteredWorktree(context.Background(), gitDir, info.WorktreePath)
	if err != nil {
		t.Fatal(err)
	}
	if registered {
		t.Error("expected worktree to be unregistered from git")
	}
}

func TestRemove_WorktreeWithSubmoduleNoForceRefusesUnsafeStates(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, mgr *Manager, ownerRepo, wtPath string)
	}{
		{
			name: "untracked file inside submodule",
			prepare: func(t *testing.T, _ *Manager, _, wtPath string) {
				if err := os.WriteFile(filepath.Join(wtPath, "vendor", "sub", "untracked.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "modified tracked file inside submodule",
			prepare: func(t *testing.T, _ *Manager, _, wtPath string) {
				if err := os.WriteFile(filepath.Join(wtPath, "vendor", "sub", "tracked.txt"), []byte("v2\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "submodule HEAD moved off the recorded commit",
			prepare: func(t *testing.T, _ *Manager, _, wtPath string) {
				runGitT(t, filepath.Join(wtPath, "vendor", "sub"), "checkout", "-q", "--detach", "HEAD~1")
			},
		},
		{
			name: "untracked file in the superproject",
			prepare: func(t *testing.T, _ *Manager, _, wtPath string) {
				if err := os.WriteFile(filepath.Join(wtPath, "untracked.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "untracked file in the superproject hidden by showUntrackedFiles=no",
			prepare: func(t *testing.T, _ *Manager, _, wtPath string) {
				runGitT(t, wtPath, "config", "status.showUntrackedFiles", "no")
				if err := os.WriteFile(filepath.Join(wtPath, "untracked.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "untracked file in submodule hidden by superproject showUntrackedFiles=no",
			prepare: func(t *testing.T, _ *Manager, _, wtPath string) {
				runGitT(t, wtPath, "config", "status.showUntrackedFiles", "no")
				if err := os.WriteFile(filepath.Join(wtPath, "vendor", "sub", "untracked.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "untracked file in submodule hidden by its own showUntrackedFiles=no",
			prepare: func(t *testing.T, _ *Manager, _, wtPath string) {
				subPath := filepath.Join(wtPath, "vendor", "sub")
				runGitT(t, subPath, "config", "status.showUntrackedFiles", "no")
				if err := os.WriteFile(filepath.Join(subPath, "untracked.txt"), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "locked worktree",
			prepare: func(t *testing.T, mgr *Manager, ownerRepo, wtPath string) {
				runGitT(t, filepath.Join(mgr.RepoDir(ownerRepo), "main"), "worktree", "lock", wtPath)
			},
		},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch := "issue/7" + string(rune('0'+i))
			mgr, ownerRepo, info := addWorktreeWithSubmodule(t, branch, 70+i)
			tt.prepare(t, mgr, ownerRepo, info.WorktreePath)

			if err := mgr.Remove(context.Background(), ownerRepo, branch, false, false); err == nil {
				t.Fatal("Remove without force should refuse")
			}
			if _, err := os.Stat(filepath.Join(info.WorktreePath, "vendor", "sub", ".git")); err != nil {
				t.Errorf("worktree and submodule content should remain on disk after refusal, stat err: %v", err)
			}
		})
	}
}

func TestRemove_DirtyWorktreeWithSubmoduleForceSucceeds(t *testing.T) {
	mgr, ownerRepo, info := addWorktreeWithSubmodule(t, "issue/61", 61)
	if err := os.WriteFile(filepath.Join(info.WorktreePath, "vendor", "sub", "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Remove(context.Background(), ownerRepo, "issue/61", true, false); err != nil {
		t.Fatalf("Remove with force should succeed, got: %v", err)
	}
	if _, err := os.Stat(info.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("expected worktree directory to be removed, stat err: %v", err)
	}
}
