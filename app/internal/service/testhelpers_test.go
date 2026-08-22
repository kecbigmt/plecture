//go:build integration

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// setupE2ERepo creates a git repo structure that mimics what plect expects.
// The workspace-dirs root is HOME-based so a `plect` subprocess spawned by a
// workspace provider hook resolves the same root from its own config
// defaults. Returns the workspace-dirs root.
func setupE2ERepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	workdirsRoot := filepath.Join(home, "workspace_dirs")
	ownerRepo := "testowner/testrepo"
	repoDir := filepath.Join(workdirsRoot, "github.com", ownerRepo)
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

	return workdirsRoot
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

// writeIntegrationFixture is writeWorkflowFixture plus the workspace
// provider that backs the workflow: the real GitHub workspace provider
// hooks, so an integration test exercises actual workspace acquisition
// rather than a stub directory.
func writeIntegrationFixture(t *testing.T, workdirsRoot, wfID string, defs []taskFixture, nodes []nodeFixture) *config.Config {
	t.Helper()
	cfg := writeWorkflowFixture(t, workdirsRoot, wfID, defs, nodes)
	attachGithubWorkspaceProvider(t, cfg, wfID)
	return cfg
}

// attachGithubWorkspaceProvider points a workflow at a workspace provider
// whose hooks are the shipped GitHub workspace provider executable, and
// mounts that executable (and github-watcher) as cfg.Plugins so the hooks'
// `{{bin ...}}` references resolve the way they would for a real
// catalog-mounted plugin. The workspace provider file itself is written
// inside that same mounted plugin's directory (not the global config
// layer) — plugin-local `{{bin "<name>"}}` resolution finds the containing
// plugin from the workspace provider file's own path, so a fixture that
// split the two across different directories the way a real mount never
// does would fail to resolve its own bare-name references.
func attachGithubWorkspaceProvider(t *testing.T, cfg *config.Config, wfID string) {
	t.Helper()
	// Appended, not assigned: a fixture attaching more than one workflow (each
	// its own attachGithubWorkspaceProvider call) must keep every earlier call's mount
	// resolvable too, not just the last one.
	newMount := buildWorkspaceProviderBinaries(t, repoRoot(t))
	cfg.Plugins = append(cfg.Plugins, newMount...)
	pluginDir := newMount[0].Dir
	cfg.PluginDirs = append(cfg.PluginDirs, pluginDir)
	workspacesDir := filepath.Join(pluginDir, "config", "workspaces")
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The shipped declaration's id is its table name, so a fixture that wants
	// the provider under the workflow's own id renames the table rather than
	// the file.
	body := strings.ReplaceAll(shippedGithubWorkspaceProviderTOML(t), "[github", "["+wfID)
	if err := os.WriteFile(filepath.Join(workspacesDir, wfID+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wfPath := filepath.Join(cfg.BaseDir, "workflows", wfID+".toml")
	existing, err := os.ReadFile(wfPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wfPath, append([]byte("workspace_provider = \""+wfID+"\"\n"), existing...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shippedGithubWorkspaceProviderTOML reads the workspace provider config the
// GitHub workspace provider plugin ships, so fixtures never drift from it.
func shippedGithubWorkspaceProviderTOML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins", "github", "config", "workspaces", "github.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
