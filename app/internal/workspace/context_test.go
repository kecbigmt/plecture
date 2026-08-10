package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gh "github.com/kecbigmt/sennit/app/internal/github"
)

// writeFakeBin creates an executable at dir/name that just runs body as a
// shell script, and prepends dir to PATH for the duration of the test — used
// to simulate a hung git or gh child process without actually blocking for
// the real timeout.
func writeFakeBin(t *testing.T, name, body string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	script := "#!/usr/bin/env sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake %s: %v", name, err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestResolveBranch_PR_ContextCancellationKillsHungGh(t *testing.T) {
	writeFakeBin(t, "gh", "sleep 30")
	mgr := NewManager(t.TempDir())
	parsed := &gh.ParsedURL{Type: gh.URLTypePR, Number: 1, OwnerRepo: "acme/widgets"}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := mgr.ResolveBranch(ctx, parsed)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from a timed-out ResolveBranch, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("ResolveBranch took %v; the hung gh process was not terminated promptly", elapsed)
	}
}

func TestManager_Add_ContextCancellationKillsHungGit(t *testing.T) {
	writeFakeBin(t, "git", "sleep 30")
	worktreesRoot := t.TempDir()
	ownerRepo := "testowner/testrepo"
	repoDir := filepath.Join(worktreesRoot, "github.com", ownerRepo, "main")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A fake git means FindGitDir's os.Stat-based checks still work (no real
	// .git needed for the primary-checkout/bare-layout paths this hits), but
	// any actual git invocation Add issues will hang until killed.
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(worktreesRoot)
	parsed := &gh.ParsedURL{OwnerRepo: ownerRepo, Repo: "testrepo", Type: gh.URLTypeIssue, Number: 1}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := mgr.Add(ctx, AddParams{Parsed: parsed, Branch: "issue/1", SessionName: gh.SessionName(ownerRepo, 1)})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from a timed-out Add, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("Add took %v; the hung git process was not terminated promptly", elapsed)
	}
}

func TestGetWorktreeStatus_ContextCancellationKillsHungGit(t *testing.T) {
	writeFakeBin(t, "git", "sleep 30")

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := GetWorktreeStatus(ctx, t.TempDir())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from a timed-out GetWorktreeStatus, got nil")
	}
	if elapsed > 5*time.Second {
		t.Errorf("GetWorktreeStatus took %v; the hung git process was not terminated promptly", elapsed)
	}
}

// TestManager_Remove_CancelledDuringRegistrationCheck_DoesNotBypassGitRemove
// is a regression test for a safety bug: isRegisteredWorktree used to return
// a bare bool, so any failure of the `git worktree list` check — including
// one caused by a cancelled/expired ctx — was indistinguishable from "not a
// registered worktree" and fell through to a raw os.RemoveAll, bypassing
// git's own worktree-remove data-loss guard entirely. Remove must instead
// fail the whole operation when it cannot determine registration status.
func TestManager_Remove_CancelledDuringRegistrationCheck_DoesNotBypassGitRemove(t *testing.T) {
	worktreesRoot, ownerRepo := setupTestRepo(t)
	mgr := NewManager(worktreesRoot)
	parsed := &gh.ParsedURL{OwnerRepo: ownerRepo, Repo: "testrepo", Type: gh.URLTypeIssue, Number: 90}

	info, err := mgr.Add(context.Background(), AddParams{Parsed: parsed, Branch: "issue/90", SessionName: gh.SessionName(ownerRepo, 90)})
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the registration check even runs

	err = mgr.Remove(ctx, parsed, "issue/90", false, false)
	if err == nil {
		t.Fatal("expected Remove() to fail when the registration check itself cannot run, got nil")
	}

	if _, statErr := os.Stat(info.WorktreePath); statErr != nil {
		t.Errorf("worktree path was removed despite the registration check failing; Remove() must not fall back to a raw filesystem delete: stat error = %v", statErr)
	}
}
