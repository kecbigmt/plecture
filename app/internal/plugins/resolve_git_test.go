package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/procexec"
)

// newLocalGitRepo creates a git repository at a local path with one commit
// tagged v1.0.0, then a second commit on top. Cloning a local path this way
// exercises FetchGit's real git plumbing without any network dependency.
func newLocalGitRepo(t *testing.T) (repoDir, firstCommit, secondCommit string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
	dir := t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=plect-test", "GIT_AUTHOR_EMAIL=plect-test@example.test",
		"GIT_COMMITTER_NAME=plect-test", "GIT_COMMITTER_EMAIL=plect-test@example.test",
	)
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte("name = \"okf\"\nplect_min_version = \"0.1.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "--quiet", "-m", "v1")
	run("tag", "v1.0.0")
	first := run("rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte("name = \"okf\"\nplect_min_version = \"0.2.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "--quiet", "-am", "v2")
	second := run("rev-parse", "HEAD")

	return dir, first, second
}

func TestFetchGit_ResolvesTagToCommit(t *testing.T) {
	repo, firstCommit, _ := newLocalGitRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	resolved, err := FetchGit(context.Background(), procexec.Default, repo, "v1.0.0", dest)
	if err != nil {
		t.Fatalf("FetchGit: unexpected error: %v", err)
	}
	if resolved != firstCommit {
		t.Errorf("resolved = %q, want %q", resolved, firstCommit)
	}
	if _, err := os.Stat(filepath.Join(dest, "plugin.toml")); err != nil {
		t.Errorf("plugin.toml not checked out: %v", err)
	}
}

func TestFetchGit_ResolvesCommitToItself(t *testing.T) {
	repo, _, secondCommit := newLocalGitRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	resolved, err := FetchGit(context.Background(), procexec.Default, repo, secondCommit, dest)
	if err != nil {
		t.Fatalf("FetchGit: unexpected error: %v", err)
	}
	if resolved != secondCommit {
		t.Errorf("resolved = %q, want %q", resolved, secondCommit)
	}
}

func TestFetchGit_UnknownRevisionFails(t *testing.T) {
	repo, _, _ := newLocalGitRepo(t)
	dest := filepath.Join(t.TempDir(), "checkout")

	if _, err := FetchGit(context.Background(), procexec.Default, repo, "does-not-exist", dest); err == nil {
		t.Fatal("want error for an unknown revision, got nil")
	}
}

func TestFetchGit_RefusesToOverwriteExistingDestination(t *testing.T) {
	repo, firstCommit, _ := newLocalGitRepo(t)
	dest := t.TempDir() // already exists

	if _, err := FetchGit(context.Background(), procexec.Default, repo, firstCommit, dest); err == nil {
		t.Fatal("want error when destDir already exists, got nil")
	}
}

func TestFetchGit_UnreachableSourceFails(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "checkout")

	if _, err := FetchGit(context.Background(), procexec.Default, filepath.Join(t.TempDir(), "no-such-repo"), "HEAD", dest); err == nil {
		t.Fatal("want error for an unreachable source, got nil")
	}
}
