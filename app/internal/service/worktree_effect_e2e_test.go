//go:build integration

package service

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

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

var pluginBinaries = []struct{ moduleDir, pkg, name string }{
	{"app", "./cmd/plect", "plect"},
	{filepath.Join("plugins", "github", "src"), "./cmd/github-worktree", "github-worktree"},
	{filepath.Join("plugins", "github", "src"), "./cmd/github-watcher", "github-watcher"},
	{filepath.Join("plugins", "github", "src"), "./cmd/github-issue-pr", "github-issue-pr"},
	{filepath.Join("plugins", "github", "src"), "./cmd/github-webhook-receiver", "github-webhook-receiver"},
	{filepath.Join("plugins", "github", "src"), "./cmd/gh-app-token", "gh-app-token"},
}

// buildSharedPluginBinaries builds pluginBinaries once
// per test binary run: dozens of tests each mount a plugin
// fixture, and a per-test rebuild of all four was this package's dominant
// integration-test cost.
func buildSharedPluginBinaries(root string) (string, error) {
	return sharedPluginBinaries.build(root, pluginBinaries, goToolCaches)
}

// buildShippedPluginBinaries symlinks the shared binaries (see
// buildSharedPluginBinaries) into a fresh per-test directory and
// PATH, so a test's own config writes do not collide with
// another test's.
func buildShippedPluginBinaries(t *testing.T, root string) []plugins.Mounted {
	t.Helper()
	sharedDir, err := buildSharedPluginBinaries(root)
	if err != nil {
		t.Fatalf("build plugin binaries: %v", err)
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range pluginBinaries {
		if err := os.Symlink(filepath.Join(sharedDir, b.name), filepath.Join(binDir, b.name)); err != nil {
			t.Fatalf("symlink %s: %v", b.name, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return []plugins.Mounted{{
		ID:  "official/github",
		Dir: binDir,
		Manifest: plugins.Manifest{Executables: []plugins.Executable{
			{Name: "github-worktree", Path: "github-worktree"},
			{Name: "github-watcher", Path: "github-watcher"},
			{Name: "github-issue-pr", Path: "github-issue-pr"},
			{Name: "github-webhook-receiver", Path: "github-webhook-receiver"},
			{Name: "gh-app-token", Path: "gh-app-token"},
		}},
	}}
}

func TestBuildPluginBinaries_BuildsOnce(t *testing.T) {
	root := repoRoot(t)
	buildShippedPluginBinaries(t, root)
	firstDir, err := buildSharedPluginBinaries(root)
	if err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(filepath.Join(firstDir, "plect"))
	if err != nil {
		t.Fatal(err)
	}

	buildShippedPluginBinaries(t, root)
	secondDir, err := buildSharedPluginBinaries(root)
	if err != nil {
		t.Fatal(err)
	}
	if secondDir != firstDir {
		t.Fatalf("shared binaries directory changed between calls: %q vs %q", firstDir, secondDir)
	}
	secondInfo, err := os.Stat(filepath.Join(secondDir, "plect"))
	if err != nil {
		t.Fatal(err)
	}
	if !secondInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Error("plect was rebuilt on a second mount request; plugin binaries must build exactly once per test binary run")
	}
}

const forceSharedBuildFailureEnvVar = "PLECT_TEST_FORCE_SHARED_BUILD_FAILURE"

// Poisoning the real shared build singleton is safe only in a throwaway
// process, so this only runs as a subprocess.
func TestForceSharedBuildFailure(t *testing.T) {
	if os.Getenv(forceSharedBuildFailureEnvVar) != "1" {
		t.Skip("subprocess helper; set " + forceSharedBuildFailureEnvVar + " to run")
	}
	dir, err := sharedPluginBinaries.build(repoRoot(t), []struct{ moduleDir, pkg, name string }{
		{"app", "./cmd/this-package-does-not-exist", "nope"},
	}, goToolCaches)
	if err == nil {
		t.Fatal("expected an error building a nonexistent package")
	}
	os.Stdout.WriteString("SHARED_BIN_DIR=" + dir + "\n")
}

// Spawns TestForceSharedBuildFailure as a subprocess so its real TestMain,
// not a copy of its logic, is what cleans up.
func TestMain_RemovesTheSharedDirAfterAFailedBuild(t *testing.T) {
	cmd := exec.Command("go", "test", "-tags", "integration", "-run", "^TestForceSharedBuildFailure$", "-v", "./internal/service/...")
	cmd.Dir = filepath.Join(repoRoot(t), "app")
	cmd.Env = append(os.Environ(), forceSharedBuildFailureEnvVar+"=1")
	cmd.Env = append(cmd.Env, goToolCaches...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, out)
	}

	var dir string
	for _, line := range strings.Split(string(out), "\n") {
		if after, ok := strings.CutPrefix(line, "SHARED_BIN_DIR="); ok {
			dir = after
		}
	}
	if dir == "" {
		t.Fatalf("subprocess output missing SHARED_BIN_DIR=...:\n%s", out)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("TestMain should have removed %s, stat err = %v", dir, statErr)
	}
}
