//go:build integration

package reactor

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/plugins"
)

// repoRootForE2E locates the repository root from this file's own path, so
// the test does not depend on the working directory it runs from.
func repoRootForE2E(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}

// goToolCachesForE2E holds the module/build cache locations resolved before
// any test in this file redirects HOME — computed once at package init, not
// per-test, since a build issued after HOME points at a throwaway temp dir
// would otherwise resolve GOMODCACHE/GOCACHE relative to that fake HOME too
// and populate a throwaway module cache (and, worse, leave read-only cache
// files under a t.TempDir() that its own cleanup cannot remove).
var goToolCachesForE2E = resolveGoToolCachesForE2E()

func resolveGoToolCachesForE2E() []string {
	out, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		return nil
	}
	lines := splitLines(string(out))
	if len(lines) != 2 {
		return nil
	}
	return []string{"GOMODCACHE=" + lines[0], "GOCACHE=" + lines[1]}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

// This file drives the one real, shipped provider plugin capable of proving
// task-instance delivery wiring end to end (a resident watcher daemon
// publishing over a real event bus) — there is no generic-provider way to
// pin that. Its identifiers below are a genuine, necessary exception to
// core's own provider-neutrality, using check-provider-boundary.sh's own
// "boundary-allow" escape hatch, the same way the pre-existing
// app/internal/service/worktree_effect_e2e_test.go already does.
const (
	pluginDirName      = "github"           // boundary-allow: real plugin directory name
	worktreeBin        = "github-worktree"  // boundary-allow: real plugin binary name
	watcherBin         = "github-watcher"   // boundary-allow: real plugin binary name
	appTokenBin        = "gh-app-token"     // boundary-allow: real plugin binary name
	mountedID          = "official/github"  // boundary-allow: real catalog mount address
	eventSource        = "github"           // boundary-allow: real event.Source the watcher publishes
	mergeableEventType = "github.mergeable" // boundary-allow: real event type the watcher publishes
)

var shippedPluginBinaries = []struct{ moduleDir, pkg, name string }{
	{"app", "./cmd/plect", "plect"},
	{filepath.Join("plugins", pluginDirName, "src"), "./cmd/" + worktreeBin, worktreeBin},
	{filepath.Join("plugins", pluginDirName, "src"), "./cmd/" + watcherBin, watcherBin},
	{filepath.Join("plugins", pluginDirName, "src"), "./cmd/" + appTokenBin, appTokenBin},
}

// buildSharedShippedPluginBinaries builds shippedPluginBinaries once per test
// binary run rather than once per call: this file's two tests each mount a
// fresh copy, and each was separately rebuilding all four.
func buildSharedShippedPluginBinaries(root string) (string, error) {
	return sharedShippedPluginBinaries.build(root, shippedPluginBinaries, goToolCachesForE2E)
}

// buildGithubPluginBinaries symlinks the shared binaries (see
// buildSharedShippedPluginBinaries) into a fresh per-test directory and PATH,
// and returns the mounted-plugin entry the shipped worktree.toml's
// `{{bin ...}}` references need to resolve. Mirrors app/internal/service's
// own buildShippedPluginBinaries — duplicated here rather than shared,
// because that helper is unexported in a package this one cannot import (see
// the comment on TestE2E_TaskSetupResourceDeliversRealWatcherEventToReactiveTick
// for why this test lives in this package at all).
func buildGithubPluginBinaries(t *testing.T, root string) []plugins.Mounted {
	t.Helper()
	sharedDir, err := buildSharedShippedPluginBinaries(root)
	if err != nil {
		t.Fatalf("build shipped plugin binaries: %v", err)
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range shippedPluginBinaries {
		if err := os.Symlink(filepath.Join(sharedDir, b.name), filepath.Join(binDir, b.name)); err != nil {
			t.Fatalf("symlink %s: %v", b.name, err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return []plugins.Mounted{{
		ID:  mountedID,
		Dir: binDir,
		Manifest: plugins.Manifest{Executables: []plugins.Executable{
			{Name: worktreeBin, Path: worktreeBin},
			{Name: watcherBin, Path: watcherBin},
			{Name: appTokenBin, Path: appTokenBin},
		}},
	}}
}

func TestBuildShippedPluginBinaries_BuildsOnce(t *testing.T) {
	root := repoRootForE2E(t)
	buildGithubPluginBinaries(t, root)
	firstDir, err := buildSharedShippedPluginBinaries(root)
	if err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(filepath.Join(firstDir, "plect"))
	if err != nil {
		t.Fatal(err)
	}

	buildGithubPluginBinaries(t, root)
	secondDir, err := buildSharedShippedPluginBinaries(root)
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
		t.Error("plect was rebuilt on a second mount request; shipped plugin binaries must build exactly once per test binary run")
	}
}

const forceSharedBuildFailureEnvVar = "PLECT_TEST_FORCE_SHARED_BUILD_FAILURE"

func TestForceSharedBuildFailure(t *testing.T) {
	if os.Getenv(forceSharedBuildFailureEnvVar) != "1" {
		t.Skip("subprocess helper; set " + forceSharedBuildFailureEnvVar + " to run")
	}
	dir, err := sharedShippedPluginBinaries.build(repoRootForE2E(t), []struct{ moduleDir, pkg, name string }{
		{"app", "./cmd/this-package-does-not-exist", "nope"},
	}, goToolCachesForE2E)
	if err == nil {
		t.Fatal("expected an error building a nonexistent package")
	}
	os.Stdout.WriteString("SHARED_BIN_DIR=" + dir + "\n")
}

func TestMain_RemovesTheSharedDirAfterAFailedBuild(t *testing.T) {
	cmd := exec.Command("go", "test", "-tags", "integration", "-run", "^TestForceSharedBuildFailure$", "-v", "./internal/reactor/...")
	cmd.Dir = filepath.Join(repoRootForE2E(t), "app")
	cmd.Env = append(os.Environ(), forceSharedBuildFailureEnvVar+"=1")
	cmd.Env = append(cmd.Env, goToolCachesForE2E...)
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
