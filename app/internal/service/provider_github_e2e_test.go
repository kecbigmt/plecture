//go:build integration

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// shippedGithubProvider loads the provider config that ships with the GitHub
// provider plugin, so the invariant/convergence tests exercise the hooks that
// ship rather than a fixture. It also builds the binaries those hooks invoke
// and puts them on PATH, since the hooks are what is under test here.
func shippedGithubProvider(t *testing.T) config.ProviderConfig {
	t.Helper()
	root := repoRoot(t)
	buildProviderBinaries(t, root)
	return loadShippedProvider(t, "github-provider", "github")
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

// buildProviderBinaries compiles the plect CLI and the GitHub provider
// executable into a temp directory that is prepended to PATH, so the shipped
// hooks resolve to the code in this working tree.
func buildProviderBinaries(t *testing.T, root string) {
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
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// setupHomeRepo builds the bare-ish layout the github provider expects under
// $HOME/worktrees (the path the setup script hard-codes) and points HOME at it.
func setupHomeRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	gitDir := filepath.Join(home, "worktrees", "github.com", "testowner", "testrepo", "main")
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

// setupSrcRepo builds the ~/src layout: a primary checkout plus a worktree
// container that is deliberately NOT a repository. Returns the primary
// checkout, which is the git dir both setup and cleanup must resolve to.
func setupSrcRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	srcDir := filepath.Join(home, "src", "github.com", "testowner", "testrepo")
	container := filepath.Join(home, "worktrees", "github.com", "testowner", "testrepo")
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

// TestE2E_GithubProviderSrcLayoutSingleWorktree covers the ~/src layout's
// thinnest case: the session owns the container's only worktree. Cleanup has
// no sibling worktree and no bare layout to fall back to, so resolving the
// primary checkout is the only thing keeping destroy/gc from stranding it.
func TestE2E_GithubProviderSrcLayoutSingleWorktree(t *testing.T) {
	setupFakeScripts(t)
	setupSrcRepo(t)
	prov := shippedGithubProvider(t)
	session := "testowner/testrepo-42+review"

	tasks := map[string]*contract.TaskState{}
	vars := task.WorkflowHookVars{ResourceID: "https://github.com/testowner/testrepo/issues/42", SessionName: session}
	out, err := task.RunWorkflowSetup(prov, vars, tasks, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	workdir, _ := out["workdir"].(string)
	if _, err := os.Stat(workdir); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	if !strings.Contains(workdir, filepath.Join("worktrees", "github.com", "testowner", "testrepo")) {
		t.Errorf("workdir = %q, want it inside the worktree container", workdir)
	}

	if err := task.RunWorkflowCleanup(prov, vars, tasks, nil); err != nil {
		t.Fatalf("cleanup on the container's only worktree: %v", err)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed, stat err: %v", err)
	}
}

func runSetup(t *testing.T, prov config.ProviderConfig, session string) map[string]any {
	t.Helper()
	tasks := map[string]*contract.TaskState{}
	out, err := task.RunWorkflowSetup(prov, task.WorkflowHookVars{
		ResourceID:  "https://github.com/testowner/testrepo/issues/42",
		SessionName: session,
	}, tasks, nil)
	if err != nil {
		t.Fatalf("provider setup: %v", err)
	}
	return out
}

// TestE2E_GithubProviderInvariant runs the shipped github setup and asserts the
// workspace is a function of the tagged session name (branch + worktree path
// both carry the tag).
func TestE2E_GithubProviderInvariant(t *testing.T) {
	setupFakeScripts(t)
	setupHomeRepo(t)
	prov := shippedGithubProvider(t)

	out := runSetup(t, prov, "testowner/testrepo-42+review")

	if got := out["branch"]; got != "issue/42+review" {
		t.Errorf("branch = %v, want issue/42+review (derived from tagged session name)", got)
	}
	workdir, _ := out["workdir"].(string)
	if !strings.HasSuffix(workdir, "/issue-42-review") {
		t.Errorf("workdir = %q, want suffix /issue-42-review", workdir)
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Fatalf("worktree not created: %v", err)
	}
	// A different tool's session on the same resource must land in a distinct
	// workspace (the cross-tool separation the default tag guarantees).
	out2 := runSetup(t, prov, "testowner/testrepo-42+claude")
	if out2["workdir"] == workdir {
		t.Error("tagged sessions must not share a workspace")
	}
}

// TestE2E_GithubProviderConvergesAndReclaims covers destroy/GC convergence on
// the live provider path: cleanup reclaims the tagged branch, and a re-dispatch
// over an orphaned branch reuses it instead of failing.
func TestE2E_GithubProviderConvergesAndReclaims(t *testing.T) {
	setupFakeScripts(t)
	gitDir := setupHomeRepo(t)
	prov := shippedGithubProvider(t)
	session := "testowner/testrepo-42+review"
	branch := "issue/42+review"

	// First dispatch.
	tasks := map[string]*contract.TaskState{}
	vars := task.WorkflowHookVars{ResourceID: "https://github.com/testowner/testrepo/issues/42", SessionName: session}
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

	// Cleanup reclaims worktree + branch (branch is merged into main → safe -d).
	if err := task.RunWorkflowCleanup(prov, vars, tasks, nil); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	workdir, _ := tasks[contract.WorkflowPseudoNodeID].Outputs["workdir"].(string)
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("worktree should be removed, stat err: %v", err)
	}
	if branchExists() {
		t.Error("cleanup must reclaim the tagged branch")
	}

	// Now simulate an orphan: re-create the branch + worktree, then remove only
	// the worktree (branch survives), and re-dispatch. Setup must converge.
	tasks2 := map[string]*contract.TaskState{}
	out := runSetup(t, prov, session)
	wd, _ := out["workdir"].(string)
	if err := exec.Command("git", "-C", gitDir, "worktree", "remove", wd).Run(); err != nil {
		t.Fatalf("orphan the branch (remove worktree): %v", err)
	}
	if !branchExists() {
		t.Fatal("precondition: branch should survive worktree removal")
	}
	tasks2 = map[string]*contract.TaskState{}
	if _, err := task.RunWorkflowSetup(prov, vars, tasks2, nil); err != nil {
		t.Fatalf("re-dispatch over orphan branch should reuse it, got: %v", err)
	}
}
