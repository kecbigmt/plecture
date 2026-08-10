//go:build integration

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupE2ERepo creates a git repo structure that mimics what sennit expects.
// Returns worktreesRoot.
func setupE2ERepo(t *testing.T) string {
	t.Helper()
	worktreesRoot := t.TempDir()
	ownerRepo := "testowner/testrepo"
	repoDir := filepath.Join(worktreesRoot, "github.com", ownerRepo)
	mainDir := filepath.Join(repoDir, "main")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}

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
	run(mainDir, "git", "init", "-b", "main")
	run(mainDir, "git", "config", "user.email", "test@test.com")
	run(mainDir, "git", "config", "user.name", "Test")
	run(mainDir, "git", "commit", "--allow-empty", "-m", "init")
	run(mainDir, "git", "remote", "add", "origin", bareDir)
	run(mainDir, "git", "push", "-u", "origin", "main")

	return worktreesRoot
}

// setupFakeScripts creates a fake gh script in a temp dir and prepends it to PATH.
func setupFakeScripts(t *testing.T) {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ghScript := `#!/usr/bin/env bash
case "$1 $2" in
  "issue view")
    echo '{"state":"OPEN"}'
    if [[ "$*" == *"--jq"* ]]; then
      echo "open"
    fi
    ;;
  "pr view")
    echo '{"headRefName":"feature-branch","state":"OPEN","reviewDecision":""}'
    if [[ "$*" == *"--jq"* ]]; then
      echo "open"
    fi
    ;;
  "pr list")
    echo '[]'
    ;;
  *)
    exit 0
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)
}
