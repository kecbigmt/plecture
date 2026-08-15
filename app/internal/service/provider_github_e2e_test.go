//go:build integration

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// shippedGithubProvider loads the provider config that ships with the GitHub
// catalog plugin, so the invariant/convergence tests exercise the hooks that
// ship rather than a fixture. It also builds the binaries those hooks
// resolve through `{{bin ...}}` and returns the mounted-plugin entry that
// resolution needs, since the hooks are what is under test here.
//
// The provider is loaded from inside the same directory buildProviderBinaries
// mounted (not straight from the repo's plugins/github/), because
// plugin-local `{{bin "<name>"}}` resolution finds the containing plugin from
// the provider's own SourcePath — it must actually live under the mounted
// plugin's directory, the way a real catalog mount always does. The copy is
// made here, not inside buildProviderBinaries, because attachGithubProvider
// (the other buildProviderBinaries caller) writes its own differently-named
// provider file into the same mounted directory — a second, unconditional
// "github.toml" there would collide with it under a repeated-alias test like
// TestIntegration_WorkflowFrozenOnSession's two-workflow fixture.
func shippedGithubProvider(t *testing.T) (config.ProviderConfig, []plugins.Mounted) {
	t.Helper()
	root := repoRoot(t)
	mounted := buildProviderBinaries(t, root)
	providersDir := filepath.Join(mounted[0].Dir, "providers")
	if err := os.MkdirAll(providersDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shipped, err := os.ReadFile(filepath.Join(root, "plugins", "github", "providers", "github.toml"))
	if err != nil {
		t.Fatalf("read shipped github provider: %v", err)
	}
	if err := os.WriteFile(filepath.Join(providersDir, "github.toml"), shipped, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{PluginDirs: []string{mounted[0].Dir}}
	provs, err := cfg.LoadProviders()
	if err != nil {
		t.Fatalf("load mounted github provider: %v", err)
	}
	prov, ok := provs["github"]
	if !ok {
		t.Fatal(`mounted github provider: "github" not found`)
	}
	return prov, mounted
}

// goToolCaches holds the module and build cache locations resolved before any
// test rewrites HOME. Without them, a build issued from a test whose HOME is a
// temp directory would populate a throwaway module cache on every run.
var goToolCaches = resolveGoToolCaches()

func resolveGoToolCaches() []string {
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return nil
	}
	return []string{"GOMODCACHE=" + lines[0], "GOCACHE=" + lines[1]}
}

// buildProviderBinaries compiles the plect CLI and the two executables the
// GitHub catalog plugin ships (plect-github-provider, github-watcher) into a
// temp directory, prepends it to PATH (`plect` itself is still resolved that
// way), and returns the mounted-plugin entry a WorkflowHookVars/
// SubscribeHookVars.Plugins needs so the shipped hooks' `{{bin ...}}`
// references resolve to the code in this working tree.
func buildProviderBinaries(t *testing.T, root string) []plugins.Mounted {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	build := func(moduleDir, pkg, out string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, out), pkg)
		cmd.Dir = filepath.Join(root, moduleDir)
		cmd.Env = append(os.Environ(), goToolCaches...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("build %s: %v", out, err)
		}
	}
	build("app", "./cmd/plect", "plect")
	build(filepath.Join("plugins", "github-provider"), "./cmd/plect-github-provider", "plect-github-provider")
	build(filepath.Join("plugins", "github-watcher"), "./cmd/github-watcher", "github-watcher")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return []plugins.Mounted{{
		ID:  "official/plugins/github",
		Dir: binDir,
		Manifest: plugins.Manifest{Executables: []plugins.Executable{
			{Name: "plect-github-provider", Path: "plect-github-provider"},
			{Name: "github-watcher", Path: "github-watcher"},
		}},
	}}
}

// setupHomeRepo builds the bare-ish layout the github provider expects under
// $HOME/workdirs (the path the setup script hard-codes) and points HOME at it.
func setupHomeRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	gitDir := filepath.Join(home, "workdirs", "github.com", "testowner", "testrepo", "main")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("setup %v: %v", args, err)
		}
	}
	run(t.TempDir(), "git", "init", "--bare", "-b", "main", bareDir)
	run(gitDir, "git", "init", "-b", "main")
	run(gitDir, "git", "config", "user.email", "test@test.com")
	run(gitDir, "git", "config", "user.name", "Test")
	run(gitDir, "git", "commit", "--allow-empty", "-m", "init")
	run(gitDir, "git", "remote", "add", "origin", bareDir)
	run(gitDir, "git", "push", "-u", "origin", "main")
	return gitDir
}

// setupSrcRepo builds the ~/src layout: a primary checkout plus a workdir
// container that is deliberately NOT a repository. Returns the primary
// checkout, which is the git dir both setup and cleanup must resolve to.
func setupSrcRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	srcDir := filepath.Join(home, "src", "github.com", "testowner", "testrepo")
	container := filepath.Join(home, "workdirs", "github.com", "testowner", "testrepo")
	bareDir := filepath.Join(t.TempDir(), "origin.git")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("setup %v: %v", args, err)
		}
	}
	run(t.TempDir(), "git", "init", "--bare", "-b", "main", bareDir)
	run(srcDir, "git", "init", "-b", "main")
	run(srcDir, "git", "config", "user.email", "test@test.com")
	run(srcDir, "git", "config", "user.name", "Test")
	run(srcDir, "git", "commit", "--allow-empty", "-m", "init")
	run(srcDir, "git", "remote", "add", "origin", bareDir)
	run(srcDir, "git", "push", "-u", "origin", "main")
	return srcDir
}

// TestE2E_GithubProviderSrcLayoutSingleWorkdir covers the ~/src layout's
// thinnest case: the session owns the container's only workdir. Cleanup has
// no sibling workdir and no bare layout to fall back to, so resolving the
// primary checkout is the only thing keeping destroy from stranding it.
func TestE2E_GithubProviderSrcLayoutSingleWorkdir(t *testing.T) {
	setupFakeScripts(t)
	setupSrcRepo(t)
	prov, mounted := shippedGithubProvider(t)
	session := "testowner/testrepo-42+review"

	tasks := map[string]*contract.TaskState{}
	vars := task.WorkflowHookVars{ResourceID: "https://github.com/testowner/testrepo/issues/42", SessionName: session, Plugins: mounted, SourcePath: prov.SourcePath}
	out, err := task.RunWorkflowSetup(prov, vars, tasks, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	workdir, _ := out["workdir"].(string)
	if _, err := os.Stat(workdir); err != nil {
		t.Fatalf("workdir not created: %v", err)
	}
	if !strings.Contains(workdir, filepath.Join("workdirs", "github.com", "testowner", "testrepo")) {
		t.Errorf("workdir = %q, want it inside the workdir container", workdir)
	}

	if err := task.RunWorkflowCleanup(prov, vars, tasks, nil); err != nil {
		t.Fatalf("cleanup on the container's only workdir: %v", err)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("workdir should be removed, stat err: %v", err)
	}
}

func runSetup(t *testing.T, prov config.ProviderConfig, mounted []plugins.Mounted, session string) map[string]any {
	t.Helper()
	tasks := map[string]*contract.TaskState{}
	out, err := task.RunWorkflowSetup(prov, task.WorkflowHookVars{
		ResourceID:  "https://github.com/testowner/testrepo/issues/42",
		SessionName: session,
		Plugins:     mounted,
		SourcePath:  prov.SourcePath,
	}, tasks, nil)
	if err != nil {
		t.Fatalf("provider setup: %v", err)
	}
	return out
}

// TestE2E_GithubProviderInvariant runs the shipped github setup and asserts the
// session is a function of the tagged session name (branch + workdir path
// both carry the tag).
func TestE2E_GithubProviderInvariant(t *testing.T) {
	setupFakeScripts(t)
	setupHomeRepo(t)
	prov, mounted := shippedGithubProvider(t)

	out := runSetup(t, prov, mounted, "testowner/testrepo-42+review")

	if got := out["branch"]; got != "issue/42+review" {
		t.Errorf("branch = %v, want issue/42+review (derived from tagged session name)", got)
	}
	workdir, _ := out["workdir"].(string)
	if !strings.HasSuffix(workdir, "/issue-42-review") {
		t.Errorf("workdir = %q, want suffix /issue-42-review", workdir)
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Fatalf("workdir not created: %v", err)
	}
	// A different tool's session on the same resource must land in a distinct
	// session (the cross-tool separation the default tag guarantees).
	out2 := runSetup(t, prov, mounted, "testowner/testrepo-42+claude")
	if out2["workdir"] == workdir {
		t.Error("tagged sessions must not share a session")
	}
}

// TestE2E_GithubProviderConvergesAndReclaims covers destroy convergence on
// the live provider path: cleanup reclaims the tagged branch, and a re-dispatch
// over an orphaned branch reuses it instead of failing.
func TestE2E_GithubProviderConvergesAndReclaims(t *testing.T) {
	setupFakeScripts(t)
	gitDir := setupHomeRepo(t)
	prov, mounted := shippedGithubProvider(t)
	session := "testowner/testrepo-42+review"
	branch := "issue/42+review"

	// First dispatch.
	tasks := map[string]*contract.TaskState{}
	// delete_branch opts into branch reclaim on cleanup — the default is now
	// to leave it, so this test's own name ("...AndReclaims") requires it.
	vars := task.WorkflowHookVars{
		ResourceID:    "https://github.com/testowner/testrepo/issues/42",
		SessionName:   session,
		Plugins:       mounted,
		SourcePath:    prov.SourcePath,
		CleanupInputs: map[string]string{"delete_branch": "true"},
	}
	if _, err := task.RunWorkflowSetup(prov, vars, tasks, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}

	branchExists := func() bool {
		cmd := exec.Command("git", "-C", gitDir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		return cmd.Run() == nil
	}
	if !branchExists() {
		t.Fatal("precondition: tagged branch should exist after setup")
	}

	// Cleanup reclaims workdir + branch (branch is merged into main → safe -d).
	if err := task.RunWorkflowCleanup(prov, vars, tasks, nil); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	workdir, _ := tasks[contract.WorkflowPseudoNodeID].Outputs["workdir"].(string)
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("workdir should be removed, stat err: %v", err)
	}
	if branchExists() {
		t.Error("cleanup must reclaim the tagged branch")
	}

	// Now simulate an orphan: re-create the branch + workdir, then remove only
	// the workdir (branch survives), and re-dispatch. Setup must converge.
	tasks2 := map[string]*contract.TaskState{}
	out := runSetup(t, prov, mounted, session)
	wd, _ := out["workdir"].(string)
	// "worktree", not "workdir": this is the literal git subcommand, not
	// plect's own vocabulary for the acquired directory.
	if err := exec.Command("git", "-C", gitDir, "worktree", "remove", wd).Run(); err != nil {
		t.Fatalf("orphan the branch (remove workdir): %v", err)
	}
	if !branchExists() {
		t.Fatal("precondition: branch should survive workdir removal")
	}
	tasks2 = map[string]*contract.TaskState{}
	if _, err := task.RunWorkflowSetup(prov, vars, tasks2, nil); err != nil {
		t.Fatalf("re-dispatch over orphan branch should reuse it, got: %v", err)
	}
}
