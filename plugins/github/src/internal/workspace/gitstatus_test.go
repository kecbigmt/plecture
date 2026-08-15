package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupGitRepo creates a minimal git repo with an initial commit and a fake remote.
func setupGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bareDir := filepath.Join(t.TempDir(), "origin.git")

	run := func(d string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = d
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("setup command %v failed: %v", args, err)
		}
	}

	run(t.TempDir(), "git", "init", "--bare", "-b", "main", bareDir)
	run(dir, "git", "init", "-b", "main")
	run(dir, "git", "config", "user.email", "test@test.com")
	run(dir, "git", "config", "user.name", "Test")
	run(dir, "git", "commit", "--allow-empty", "-m", "init")
	run(dir, "git", "remote", "add", "origin", bareDir)
	run(dir, "git", "push", "-u", "origin", "main")

	return dir
}

func TestGetWorktreeStatus_Clean(t *testing.T) {
	dir := setupGitRepo(t)

	status, err := GetWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("GetWorktreeStatus(context.Background(), ) error: %v", err)
	}
	if status.Dirty {
		t.Error("expected clean worktree, got dirty")
	}
	if status.UntrackedFiles != 0 {
		t.Errorf("UntrackedFiles = %d, want 0", status.UntrackedFiles)
	}
	if status.Ahead != 0 {
		t.Errorf("Ahead = %d, want 0", status.Ahead)
	}
	if status.Behind != 0 {
		t.Errorf("Behind = %d, want 0", status.Behind)
	}
}

func TestGetWorktreeStatus_Dirty(t *testing.T) {
	dir := setupGitRepo(t)

	// Create a tracked file and modify it
	f := filepath.Join(dir, "file.txt")
	os.WriteFile(f, []byte("hello"), 0o644)
	cmd := exec.Command("git", "add", "file.txt")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "add file")
	cmd.Dir = dir
	cmd.Run()
	cmd = exec.Command("git", "push")
	cmd.Dir = dir
	cmd.Run()

	// Modify the tracked file
	os.WriteFile(f, []byte("modified"), 0o644)

	status, err := GetWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("GetWorktreeStatus(context.Background(), ) error: %v", err)
	}
	if !status.Dirty {
		t.Error("expected dirty worktree, got clean")
	}
}

func TestGetWorktreeStatus_UntrackedFiles(t *testing.T) {
	dir := setupGitRepo(t)

	// Create untracked files
	os.WriteFile(filepath.Join(dir, "untracked1.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "untracked2.txt"), []byte("b"), 0o644)

	status, err := GetWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("GetWorktreeStatus(context.Background(), ) error: %v", err)
	}
	if status.UntrackedFiles != 2 {
		t.Errorf("UntrackedFiles = %d, want 2", status.UntrackedFiles)
	}
}

func TestGetWorktreeStatus_Ahead(t *testing.T) {
	dir := setupGitRepo(t)

	// Make a commit without pushing
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "unpushed")
	cmd.Dir = dir
	cmd.Run()

	status, err := GetWorktreeStatus(context.Background(), dir)
	if err != nil {
		t.Fatalf("GetWorktreeStatus(context.Background(), ) error: %v", err)
	}
	if status.Ahead != 1 {
		t.Errorf("Ahead = %d, want 1", status.Ahead)
	}
}
