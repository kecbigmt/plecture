package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a git repo structure that mimics what sennit expects:
// worktreesRoot/github.com/owner/repo/main is a real git repo (acts as gitDir).
// A bare repo is used as a local "origin" remote so git fetch works.
func setupTestRepo(t *testing.T) (worktreesRoot string, ownerRepo string) {
	t.Helper()
	worktreesRoot = t.TempDir()
	ownerRepo = "github.com/testowner/testrepo"
	repoDir := filepath.Join(worktreesRoot, filepath.FromSlash(ownerRepo))
	mainDir := filepath.Join(repoDir, "main")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a bare repo to act as origin
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("setup command %v failed: %v", args, err)
		}
	}

	run(t.TempDir(), "git", "init", "--bare", "-b", "main", bareDir)

	// Initialize the working repo
	run(mainDir, "git", "init", "-b", "main")
	run(mainDir, "git", "config", "user.email", "test@test.com")
	run(mainDir, "git", "config", "user.name", "Test")
	run(mainDir, "git", "commit", "--allow-empty", "-m", "init")
	run(mainDir, "git", "remote", "add", "origin", bareDir)
	run(mainDir, "git", "push", "-u", "origin", "main")

	return worktreesRoot, ownerRepo
}

func TestAdd_NewBranch(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/42", SessionName: sessionID(ownerRepo, 42)})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if info.ReusedWorktree {
		t.Error("expected ReusedWorktree=false for new branch")
	}

	// Verify worktree directory was created
	if _, err := os.Stat(info.WorktreePath); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}
}

func TestAdd_ExistingBranch(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	gitDir := filepath.Join(worktreesRoot, filepath.FromSlash(ownerRepo), "main")

	// Pre-create the branch in the git repo
	cmd := exec.Command("git", "branch", "issue/99")
	cmd.Dir = gitDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/99", SessionName: sessionID(ownerRepo, 99)})
	if err != nil {
		t.Fatalf("Add should succeed when branch already exists, got: %v", err)
	}

	if info.ReusedWorktree {
		t.Error("expected ReusedWorktree=false (worktree is new, branch was reused)")
	}

	if _, err := os.Stat(info.WorktreePath); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}
}

func TestAdd_ExistingWorktree(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	// First add: creates worktree
	_, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/7", SessionName: sessionID(ownerRepo, 7)})
	if err != nil {
		t.Fatalf("first Add failed: %v", err)
	}

	// Second add: should reuse existing worktree
	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/7", SessionName: sessionID(ownerRepo, 7)})
	if err != nil {
		t.Fatalf("second Add failed: %v", err)
	}

	if !info.ReusedWorktree {
		t.Error("expected ReusedWorktree=true for existing worktree")
	}
}

func TestAdd_TaggedBranch(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	// Create base worktree
	info1, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/42", SessionName: sessionID(ownerRepo, 42)})
	if err != nil {
		t.Fatalf("Add base failed: %v", err)
	}

	// Create tagged worktree
	info2, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/42+review", SessionName: taggedSessionID(ownerRepo, 42, "review")})
	if err != nil {
		t.Fatalf("Add tagged failed: %v", err)
	}

	if info1.WorktreePath == info2.WorktreePath {
		t.Error("expected different worktree paths for base and tagged")
	}
	if info2.SessionName != taggedSessionID(ownerRepo, 42, "review") {
		t.Errorf("SessionName = %q, want %q", info2.SessionName, taggedSessionID(ownerRepo, 42, "review"))
	}
	if info2.Branch != "issue/42+review" {
		t.Errorf("Branch = %q, want %q", info2.Branch, "issue/42+review")
	}
}

func TestAdd_PRTag(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	gitDir := filepath.Join(worktreesRoot, filepath.FromSlash(ownerRepo), "main")

	// Create a branch to simulate a PR branch
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("command %v failed: %v", args, err)
		}
	}
	run(gitDir, "git", "branch", "feat/login")
	// Push the branch to origin
	run(gitDir, "git", "push", "origin", "feat/login")

	// Create base worktree for PR
	_, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "feat/login", SessionName: sessionID(ownerRepo, 10)})
	if err != nil {
		t.Fatalf("Add PR base failed: %v", err)
	}

	// Create tagged worktree for PR - should create new branch from remote base
	info, err := mgr.Add(context.Background(), AddParams{
		Repo:        ownerRepo,
		Branch:      "feat/login+review",
		BaseBranch:  "feat/login",
		SessionName: taggedSessionID(ownerRepo, 10, "review"),
	})
	if err != nil {
		t.Fatalf("Add PR tagged failed: %v", err)
	}

	if info.SessionName != taggedSessionID(ownerRepo, 10, "review") {
		t.Errorf("SessionName = %q, want %q", info.SessionName, taggedSessionID(ownerRepo, 10, "review"))
	}
	if info.Branch != "feat/login+review" {
		t.Errorf("Branch = %q, want %q", info.Branch, "feat/login+review")
	}
}

func TestAdd_PRFallbackToRef(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	gitDir := filepath.Join(worktreesRoot, filepath.FromSlash(ownerRepo), "main")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("command %v failed: %v", args, err)
		}
	}

	// Create a branch, push it, then simulate a PR ref and delete the remote branch.
	run(gitDir, "git", "checkout", "-b", "feat/deleted")
	run(gitDir, "git", "commit", "--allow-empty", "-m", "pr commit")
	run(gitDir, "git", "push", "origin", "feat/deleted")

	// Create a PR-style ref (pull/99/head) pointing to the same commit
	run(gitDir, "git", "push", "origin", "feat/deleted:refs/pull/99/head")

	// Delete the remote branch so that `git fetch origin feat/deleted` will fail
	run(gitDir, "git", "push", "origin", "--delete", "feat/deleted")
	run(gitDir, "git", "checkout", "main")
	run(gitDir, "git", "branch", "-D", "feat/deleted")

	// Add should succeed by falling back to pull/99/head
	info, err := mgr.Add(context.Background(), AddParams{
		Repo:        ownerRepo,
		Branch:      "feat/deleted",
		SessionName: sessionID(ownerRepo, 99),
	})
	if err != nil {
		t.Fatalf("Add should fall back to PR ref when branch is deleted, got: %v", err)
	}

	if info.ReusedWorktree {
		t.Error("expected ReusedWorktree=false")
	}

	if _, err := os.Stat(info.WorktreePath); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}
}

func TestAdd_PRFallbackToRef_WithTag(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	gitDir := filepath.Join(worktreesRoot, filepath.FromSlash(ownerRepo), "main")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("command %v failed: %v", args, err)
		}
	}

	// Same setup: create branch, push PR ref, delete remote branch
	run(gitDir, "git", "checkout", "-b", "feat/deleted2")
	run(gitDir, "git", "commit", "--allow-empty", "-m", "pr commit")
	run(gitDir, "git", "push", "origin", "feat/deleted2")
	run(gitDir, "git", "push", "origin", "feat/deleted2:refs/pull/100/head")
	run(gitDir, "git", "push", "origin", "--delete", "feat/deleted2")
	run(gitDir, "git", "checkout", "main")
	run(gitDir, "git", "branch", "-D", "feat/deleted2")

	// Add with tag should succeed using local ref from PR fetch as startPoint
	info, err := mgr.Add(context.Background(), AddParams{
		Repo:        ownerRepo,
		Branch:      "feat/deleted2+review",
		BaseBranch:  "feat/deleted2",
		SessionName: taggedSessionID(ownerRepo, 100, "review"),
	})
	if err != nil {
		t.Fatalf("Add PR tag with deleted branch should fall back to PR ref, got: %v", err)
	}

	if info.Branch != "feat/deleted2+review" {
		t.Errorf("Branch = %q, want %q", info.Branch, "feat/deleted2+review")
	}

	if _, err := os.Stat(info.WorktreePath); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}
}

func TestRemove_WorktreeAlreadyGone(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	// Create a worktree
	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/50", SessionName: sessionID(ownerRepo, 50)})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Delete the worktree directory to simulate it being gone
	if err := os.RemoveAll(info.WorktreePath); err != nil {
		t.Fatalf("failed to remove worktree dir: %v", err)
	}

	// Remove should succeed (prune stale entry) instead of failing
	if err := mgr.Remove(context.Background(), ownerRepo, "issue/50", false, false); err != nil {
		t.Fatalf("Remove should succeed when worktree is already gone, got: %v", err)
	}
}

func TestRemove_WorktreeNotRegistered(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	// Create a directory that looks like a worktree but is not registered
	wtPath := mgr.WorktreePath(ownerRepo, "issue/51")
	if err := os.MkdirAll(wtPath, 0o755); err != nil {
		t.Fatalf("failed to create fake worktree dir: %v", err)
	}

	// Remove should succeed (remove directory directly)
	if err := mgr.Remove(context.Background(), ownerRepo, "issue/51", false, false); err != nil {
		t.Fatalf("Remove should succeed for unregistered worktree dir, got: %v", err)
	}

	// Directory should be gone
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Error("expected worktree directory to be removed")
	}
}

func TestRemove_WorktreeWithUntrackedFiles_NoForceFails(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/52", SessionName: sessionID(ownerRepo, 52)})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	untrackedFile := filepath.Join(info.WorktreePath, "untracked.txt")
	if err := os.WriteFile(untrackedFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("failed to create untracked file: %v", err)
	}

	if err := mgr.Remove(context.Background(), ownerRepo, "issue/52", false, false); err == nil {
		t.Fatal("Remove without --force should fail when worktree has untracked files")
	}

	if _, err := os.Stat(info.WorktreePath); err != nil {
		t.Errorf("worktree directory should remain on disk after refusal, stat err: %v", err)
	}
	if _, err := os.Stat(untrackedFile); err != nil {
		t.Errorf("untracked file should remain on disk after refusal, stat err: %v", err)
	}
}

func TestRemove_WorktreeWithUntrackedFiles_ForceSucceeds(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/53", SessionName: sessionID(ownerRepo, 53)})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	untrackedFile := filepath.Join(info.WorktreePath, "untracked.txt")
	if err := os.WriteFile(untrackedFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("failed to create untracked file: %v", err)
	}

	if err := mgr.Remove(context.Background(), ownerRepo, "issue/53", true, false); err != nil {
		t.Fatalf("Remove with --force should succeed even with untracked files, got: %v", err)
	}

	if _, err := os.Stat(info.WorktreePath); !os.IsNotExist(err) {
		t.Error("expected worktree directory to be removed with --force")
	}
}

func TestFindGitDir_ExcludesPath(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	// Create a second worktree
	_, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/60", SessionName: sessionID(ownerRepo, 60)})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	repoDir := mgr.RepoDir(ownerRepo)
	mainDir := filepath.Join(repoDir, "main")
	wtDir := filepath.Join(repoDir, "issue-60")

	// Without exclude, FindGitDir should return one of the dirs
	gitDir, err := mgr.FindGitDir(repoDir)
	if err != nil {
		t.Fatalf("FindGitDir without exclude failed: %v", err)
	}
	if gitDir != mainDir && gitDir != wtDir {
		t.Errorf("FindGitDir returned unexpected dir: %s", gitDir)
	}

	// Exclude the worktree — should return main
	gitDir, err = mgr.FindGitDir(repoDir, wtDir)
	if err != nil {
		t.Fatalf("FindGitDir with exclude failed: %v", err)
	}
	if gitDir != mainDir {
		t.Errorf("FindGitDir with exclude should return main, got: %s", gitDir)
	}

	// Exclude main — should return the worktree
	gitDir, err = mgr.FindGitDir(repoDir, mainDir)
	if err != nil {
		t.Fatalf("FindGitDir excluding main failed: %v", err)
	}
	if gitDir != wtDir {
		t.Errorf("FindGitDir excluding main should return worktree, got: %s", gitDir)
	}
}

// runGit lets the user see git's stderr live and returns the raw exit status —
// callers add their own context, so the helper does not pre-prefix.
func TestRunGit_ReturnsRawExitStatus(t *testing.T) {
	mgr := NewManager(t.TempDir())
	err := mgr.runGit(context.Background(), t.TempDir(), "worktree", "add", "/nonexistent/path", "no-such-branch")
	if err == nil {
		t.Fatal("expected error from runGit")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exit status") {
		t.Errorf("expected exit status in error, got: %s", msg)
	}
	if strings.Contains(msg, "git worktree add") {
		t.Errorf("runGit should not pre-prefix the command (would cause stuttering when callers wrap), got: %s", msg)
	}
}

// runGitCapture surfaces git's stderr to the caller so callers like
// worktreeAddError can detect specific messages without parsing err.Error().
func TestRunGitCapture_ReturnsStderr(t *testing.T) {
	mgr := NewManager(t.TempDir())
	stderr, err := mgr.runGitCapture(context.Background(), t.TempDir(), "worktree", "add", "/nonexistent/path", "no-such-branch")
	if err == nil {
		t.Fatal("expected error from runGitCapture")
	}
	if !strings.Contains(stderr, "fatal:") {
		t.Errorf("expected git's fatal message in captured stderr, got: %q", stderr)
	}
}

func TestWorktreeAddError_AlreadyCheckedOutHint(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	// Create a worktree for issue/70
	_, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: "issue/70", SessionName: sessionID(ownerRepo, 70)})
	if err != nil {
		t.Fatalf("first Add failed: %v", err)
	}

	// Try to create a new worktree at a different path for the same branch.
	// git worktree add will fail because the branch is already checked out.
	wtPath := mgr.WorktreePath(ownerRepo, "issue/70")
	altPath := wtPath + "-alt"

	gitDir, err := mgr.FindGitDir(mgr.RepoDir(ownerRepo))
	if err != nil {
		t.Fatalf("FindGitDir failed: %v", err)
	}

	stderr, err := mgr.runGitCapture(context.Background(), gitDir, "worktree", "add", altPath, "issue/70")
	if err == nil {
		t.Fatal("expected error for already checked out branch")
	}

	wrapped := worktreeAddError(err, stderr)
	msg := wrapped.Error()
	if !strings.Contains(msg, "git worktree add failed") {
		t.Errorf("expected 'git worktree add failed' prefix in error, got: %s", msg)
	}
	if !strings.Contains(msg, "sennit up <resource> --tag <tag>") {
		t.Errorf("expected tag hint in error, got: %s", msg)
	}
}

func TestWorktreeAddError_NoHintForOtherErrors(t *testing.T) {
	err := fmt.Errorf("exit status 128")
	wrapped := worktreeAddError(err, "fatal: some other git error")
	msg := wrapped.Error()
	if !strings.Contains(msg, "git worktree add failed") {
		t.Errorf("expected 'git worktree add failed' prefix, got: %s", msg)
	}
	if strings.Contains(msg, "Hint:") {
		t.Errorf("should not contain hint for non-checkout errors, got: %s", msg)
	}
}

func TestBranchExists(t *testing.T) {
	worktreesRoot, _ := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	gitDir := filepath.Join(worktreesRoot, "github.com/testowner/testrepo/main")

	if !mgr.branchExists(context.Background(), gitDir, "main") {
		t.Error("expected main branch to exist")
	}

	if mgr.branchExists(context.Background(), gitDir, "nonexistent") {
		t.Error("expected nonexistent branch to not exist")
	}
}

// runGitT is a tiny test helper that runs a git command and fails the test on
// error (the suite repeats this inline; the named helper keeps the new
// convergence tests readable).
func runGitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
}

// TestAdd_TaggedWorktreeDerivesFromSessionName is the ADR's GitHub-provider
// invariant: the workspace (branch + worktree path) is a function of the tagged
// session name, so the session name and its workspace can never diverge.
func TestAdd_TaggedWorktreeDerivesFromSessionName(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)

	const tag = "review"
	sessionName := taggedSessionID(ownerRepo, 42, tag)
	branch := "issue/42+" + tag

	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: branch, SessionName: sessionName})
	if err != nil {
		t.Fatalf("Add tagged: %v", err)
	}
	// The tag in the session name must surface in both branch and worktree path.
	if info.Branch != branch {
		t.Errorf("Branch = %q, want %q (derived from tagged session name)", info.Branch, branch)
	}
	wantPath := mgr.WorktreePath(ownerRepo, branch)
	if info.WorktreePath != wantPath {
		t.Errorf("WorktreePath = %q, want %q", info.WorktreePath, wantPath)
	}
	if !strings.Contains(info.WorktreePath, SanitizeBranch(branch)) {
		t.Errorf("worktree path %q must encode the sanitized tagged branch %q", info.WorktreePath, SanitizeBranch(branch))
	}
}

// TestAdd_ReusesOrphanTaggedPRBranch is the destroy-leak fix: when a prior
// session's worktree is removed but its tagged branch survives (a non-force
// destroy that kept the branch), a subsequent dispatch reuses the orphan branch
// instead of failing with "a branch named ... already exists".
func TestAdd_ReusesOrphanTaggedPRBranch(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	gitDir := filepath.Join(worktreesRoot, filepath.FromSlash(ownerRepo), "main")

	runGitT(t, gitDir, "branch", "feat/login")
	runGitT(t, gitDir, "push", "origin", "feat/login")

	taggedBranch := "feat/login+review"

	// First dispatch: materialize the tagged workspace.
	first, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: taggedBranch, BaseBranch: "feat/login", SessionName: taggedSessionID(ownerRepo, 10, "review")})
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}

	// Simulate a non-force destroy that removed the worktree but kept the
	// local branch (deleteBranch=false) — the orphan the ADR worries about.
	if err := mgr.RemoveByPath(context.Background(), first.WorktreePath, gitDir, taggedBranch, false, false); err != nil {
		t.Fatalf("RemoveByPath (keep branch): %v", err)
	}
	if !mgr.branchExists(context.Background(), gitDir, taggedBranch) {
		t.Fatal("precondition: orphan tagged branch should still exist")
	}

	// Re-dispatch must converge: the orphan branch is reused, not fatal.
	second, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: taggedBranch, BaseBranch: "feat/login", SessionName: taggedSessionID(ownerRepo, 10, "review")})
	if err != nil {
		t.Fatalf("re-dispatch over orphan tagged branch should reuse it, got: %v", err)
	}
	if _, err := os.Stat(second.WorktreePath); err != nil {
		t.Fatalf("re-dispatched worktree missing: %v", err)
	}
}

// TestRemoveByPath_ReclaimsTaggedBranch verifies the worktree+branch reclaim:
// destroy with deleteBranch removes both, so nothing is left to seed a future
// collision.
func TestRemoveByPath_ReclaimsTaggedBranch(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	gitDir := filepath.Join(worktreesRoot, filepath.FromSlash(ownerRepo), "main")

	branch := "issue/42+review"
	info, err := mgr.Add(context.Background(), AddParams{Repo: ownerRepo, Branch: branch, SessionName: taggedSessionID(ownerRepo, 42, "review")})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := mgr.RemoveByPath(context.Background(), info.WorktreePath, gitDir, branch, true, true); err != nil {
		t.Fatalf("RemoveByPath (reclaim branch): %v", err)
	}
	if _, err := os.Stat(info.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("worktree should be gone, stat err: %v", err)
	}
	if mgr.branchExists(context.Background(), gitDir, branch) {
		t.Errorf("tagged branch %q should be reclaimed", branch)
	}
}

// sessionID / taggedSessionID build opaque session identifiers for the tests.
// The manager treats them as strings, so their shape only has to be stable,
// not meaningful.
func sessionID(repo string, number int) string {
	return fmt.Sprintf("%s-%d", repo, number)
}

func taggedSessionID(repo string, number int, tag string) string {
	return fmt.Sprintf("%s-%d+%s", repo, number, tag)
}
