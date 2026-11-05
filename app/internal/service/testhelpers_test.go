//go:build integration

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// repoRoot locates the repository root from this test file's own path, so a
// test that reads a shipped config file does not depend on the working
// directory it is run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// setupE2ERepo creates a git repo structure that mimics what plect expects.
// The workspace-dirs root is HOME-based so a `plect` subprocess spawned by an
// effect hook resolves the same root from its own defaults. Returns the
// workspace-dirs root.
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
	// The head branch the fake `gh pr view` reports, so a session dispatched
	// against a pull request has a ref to acquire.
	run(mainDir, "git", "push", "origin", "main:feature-branch")

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
  "api repos"*|"api "*)
    # A pull request acquisition reads its head ref through the gh api
    # subcommand and, unlike an issue, propagates a failure -- so the stub
    # answers with the branch setupE2ERepo pushed.
    case "$2" in
      *"/pulls/"*) echo '{"head":{"ref":"feature-branch"},"state":"open","merged":false}' ;;
      *) echo '{}' ;;
    esac
    ;;
  *)
    exit 0
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stand-in multiplexer that reports what it was asked to do, so a test
	// can assert on a resolved terminal verb by running it — on a machine
	// with a real tmux the verb would otherwise act on that machine.
	tmuxScript := "#!/usr/bin/env bash\necho \"$@\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(tmuxScript), 0o755); err != nil {
		t.Fatal(err)
	}

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)
}

// writeIntegrationFixture is writeWorkflowFixture plus the shipped GitHub
// worktree effect that backs the workflow's directory: the real hooks, so an
// integration test exercises actual worktree acquisition rather than a stub
// directory. workdirsRoot is where the effect places its worktrees.
func writeIntegrationFixture(t *testing.T, workdirsRoot, wfID string, defs []taskFixture, nodes []nodeFixture) *config.Config {
	t.Helper()
	cfg := writeWorkflowFixture(t, wfID, defs, nodes)
	attachGithubWorktree(t, cfg, wfID, workdirsRoot)
	return cfg
}

// attachGithubWorktree mounts the GitHub plugin's executables (and the shipped
// worktree effect, inside the same mounted directory so its plugin-local
// `bin` references resolve the way they do for a real catalog mount), adds the
// effect as a workflow node and points the workflow's workdir at its output.
// A second call for another workflow reuses the mount: one catalog identity
// has one directory.
func attachGithubWorktree(t *testing.T, cfg *config.Config, wfID, workdirsRoot string) {
	t.Helper()
	if len(cfg.Plugins) == 0 {
		mount := buildWorkspaceProviderBinaries(t, repoRoot(t))
		cfg.Plugins = append(cfg.Plugins, mount...)
		cfg.PluginDirs = append(cfg.PluginDirs, mount[0].Dir)
		tasksDir := filepath.Join(mount[0].Dir, "config", "tasks")
		if err := os.MkdirAll(tasksDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tasksDir, "worktree.toml"), []byte(shippedGithubWorktreeTOML(t)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	addWorkflowFields(t, cfg, wfID, "workdir = { from = \"nodes.worktree.outputs.workspace_dir\" }\n")
	path := filepath.Join(cfg.BaseDir, "workflows", wfID+".toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[[%[1]s.nodes]]\nid   = \"worktree\"\nuses = \"official.github.worktree\"\n\n[%[1]s.nodes.inputs]\ndirs_root = %[2]q\n", wfID, workdirsRoot); err != nil {
		t.Fatal(err)
	}
}

// useShippedGithubResources makes the workflow accept the GitHub plugin's own
// issue resource, in place of the fixture's default, so Create wires delivery
// through the real watcher hooks.
func useShippedGithubResources(t *testing.T, cfg *config.Config, wfID string) {
	t.Helper()
	copyShippedGithubResources(t, cfg.PluginDirs[0])
	dropIssueResource(t, cfg)
	setWorkflowResource(t, cfg, wfID, "official.github.issue")
}

func copyShippedGithubResources(t *testing.T, pluginDir string) {
	t.Helper()
	dir := filepath.Join(pluginDir, "config", "resources")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"issue", "pull_request"} {
		data, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins", "github", "config", "resources", name+".toml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".toml"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// shippedGithubWorktreeTOML reads the worktree effect the GitHub plugin
// ships, so fixtures never drift from it.
func shippedGithubWorktreeTOML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "plugins", "github", "config", "tasks", "worktree.toml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
