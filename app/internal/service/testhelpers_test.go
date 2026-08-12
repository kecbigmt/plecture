//go:build integration

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
)

// setupE2ERepo creates a git repo structure that mimics what plect expects.
// The worktrees root is HOME-based so a `plect` subprocess spawned by a
// provider hook resolves the same root from its own config defaults.
// Returns worktreesRoot.
func setupE2ERepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	worktreesRoot := filepath.Join(home, "worktrees")
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

// writeIntegrationFixture is writeWorkflowFixture plus the provider that backs
// the workflow: the real GitHub provider hooks, so an integration test
// exercises actual worktree acquisition rather than a stub directory.
func writeIntegrationFixture(t *testing.T, worktreesRoot, wfID string, defs []taskFixture, nodes []nodeFixture) *config.Config {
	t.Helper()
	cfg := writeWorkflowFixture(t, worktreesRoot, wfID, defs, nodes)
	attachGithubProvider(t, cfg, wfID)
	return cfg
}

// attachGithubProvider points a workflow at a provider whose hooks are the
// shipped GitHub provider executable, and puts that executable (and the plect
// CLI it calls) on PATH.
func attachGithubProvider(t *testing.T, cfg *config.Config, wfID string) {
	t.Helper()
	buildProviderBinaries(t, repoRoot(t))
	providersDir := filepath.Join(cfg.BaseDir, "providers")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := shippedGithubProviderTOML(t)
	if err := os.WriteFile(filepath.Join(providersDir, wfID+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(cfg.BaseDir, "workflows", wfID+".toml")
	existing, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wfPath, append([]byte("provider = \""+wfID+"\"\n"), existing...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shippedGithubProviderTOML reads the provider config the GitHub provider
// plugin ships, so fixtures never drift from it.
func shippedGithubProviderTOML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins", "github-provider", "providers", "github.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
