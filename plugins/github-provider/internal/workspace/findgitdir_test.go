package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func newLayoutManager(t *testing.T) (*Manager, string, string) {
	t.Helper()
	root := t.TempDir()
	worktrees := filepath.Join(root, "worktrees")
	src := filepath.Join(root, "src")
	return &Manager{WorktreesRoot: worktrees, SrcRoot: src}, worktrees, src
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func TestFindGitDir_PrefersPrimaryCheckout(t *testing.T) {
	m, worktrees, src := newLayoutManager(t)
	repoDir := filepath.Join(worktrees, "github.com", "owner", "repo")
	srcDir := filepath.Join(src, "github.com", "owner", "repo")

	// Both layouts present: the bare one must lose, otherwise a machine
	// mid-migration keeps writing worktrees against the stale object store.
	mustMkdir(t, filepath.Join(repoDir, ".bare"))
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ./.bare\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(srcDir, ".git"))

	got, err := m.FindGitDir(repoDir)
	if err != nil {
		t.Fatalf("FindGitDir: %v", err)
	}
	if got != srcDir {
		t.Errorf("FindGitDir = %q, want %q", got, srcDir)
	}
}

func TestFindGitDir_FallsBackToBareLayout(t *testing.T) {
	m, worktrees, _ := newLayoutManager(t)
	repoDir := filepath.Join(worktrees, "github.com", "owner", "repo")

	mustMkdir(t, filepath.Join(repoDir, ".bare"))
	if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: ./.bare\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := m.FindGitDir(repoDir)
	if err != nil {
		t.Fatalf("FindGitDir: %v", err)
	}
	if got != repoDir {
		t.Errorf("FindGitDir = %q, want %q", got, repoDir)
	}
}

func TestFindGitDir_FallsBackToLegacySubdir(t *testing.T) {
	m, worktrees, _ := newLayoutManager(t)
	repoDir := filepath.Join(worktrees, "github.com", "owner", "repo")
	legacy := filepath.Join(repoDir, "main")

	mustMkdir(t, filepath.Join(legacy, ".git"))

	got, err := m.FindGitDir(repoDir)
	if err != nil {
		t.Fatalf("FindGitDir: %v", err)
	}
	if got != legacy {
		t.Errorf("FindGitDir = %q, want %q", got, legacy)
	}
}

func TestFindGitDir_IgnoresPrimaryWithoutGitDir(t *testing.T) {
	m, worktrees, src := newLayoutManager(t)
	repoDir := filepath.Join(worktrees, "github.com", "owner", "repo")
	legacy := filepath.Join(repoDir, "main")

	// A bare ~/src directory (clone interrupted, or a stray mkdir) must not
	// shadow a working layout — it would resolve every git call to a non-repo.
	mustMkdir(t, filepath.Join(src, "github.com", "owner", "repo"))
	mustMkdir(t, filepath.Join(legacy, ".git"))

	got, err := m.FindGitDir(repoDir)
	if err != nil {
		t.Fatalf("FindGitDir: %v", err)
	}
	if got != legacy {
		t.Errorf("FindGitDir = %q, want %q", got, legacy)
	}
}

func TestSrcDir_RejectsPathOutsideWorktreesRoot(t *testing.T) {
	m, _, _ := newLayoutManager(t)
	if got := m.SrcDir("/somewhere/else/repo"); got != "" {
		t.Errorf("SrcDir = %q, want \"\"", got)
	}
}
