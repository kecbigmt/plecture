//go:build integration

package service

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/state"
	"time"
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

// watcherSubscriptions reads the real github-watcher's on-disk subscription
// registry (~/.local/share/github-watcher/subscriptions.json, following its
// own XDG default) directly: core never imports the plugin's watcher
// package, so this is the only way an app-side e2e test can see what the
// real `subscribe`/`unsubscribe` hooks — the shipped worktree.toml this
// package cannot otherwise observe — actually persisted.
func watcherSubscriptions(t *testing.T, home string) map[string]json.RawMessage {
	t.Helper()
	path := filepath.Join(home, ".local", "share", "github-watcher", "subscriptions.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read subscriptions.json: %v", err)
	}
	var doc struct {
		Subscriptions map[string]json.RawMessage `json:"subscriptions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse subscriptions.json: %v", err)
	}
	return doc.Subscriptions
}

// TestE2E_GithubResourceWiresAndDropsEventDelivery pins
// binding-implies-delivery against the real shipped hooks, not a fixture
// resource: binding a resource via subscribeIfWired — the same call
// TaskSetup makes for an explicit --resource — must actually register a
// subscription the real github-watcher binary persists, through the shipped
// resource's `subscribe` hook. unsubscribeIfWired — TaskCleanup's
// counterpart — must drop exactly that one registration through its
// `unsubscribe` hook.
func TestE2E_GithubResourceWiresAndDropsEventDelivery(t *testing.T) {
	root := repoRoot(t)
	mounted := buildShippedPluginBinaries(t, root)
	cfg := &config.Config{PluginDirs: []string{mounted[0].Dir}, Plugins: mounted}
	copyShippedGithubResources(t, mounted[0].Dir)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")

	const session = "testowner/testrepo-42+review"
	const resource = "https://github.com/testowner/testrepo/pull/42"

	wired, err := subscribeIfWiredWithPolicy(cfg, session, resource, nil, false)
	if err != nil {
		t.Fatalf("subscribeIfWired: %v", err)
	}
	if !wired {
		t.Fatal("the shipped github resource must wire delivery for an issue/PR URL")
	}
	subs := watcherSubscriptions(t, home)
	if len(subs) != 1 {
		t.Fatalf("subscriptions after subscribeIfWired = %v, want exactly one", subs)
	}

	unsubscribed, err := unsubscribeIfWired(cfg, session, resource, nil)
	if err != nil {
		t.Fatalf("unsubscribeIfWired: %v", err)
	}
	if !unsubscribed {
		t.Fatal("the shipped github resource must unwire delivery for an issue/PR URL")
	}
	subs = watcherSubscriptions(t, home)
	if len(subs) != 0 {
		t.Fatalf("subscriptions after unsubscribeIfWired = %v, want none", subs)
	}
}

func TestIntegration_SubscribeCallsGithubWatcherForPullRequest(t *testing.T) {
	root := repoRoot(t)
	mounted := buildShippedPluginBinaries(t, root)
	copyShippedGithubResources(t, mounted[0].Dir)
	cfg := &config.Config{PluginDirs: []string{mounted[0].Dir}, Plugins: mounted}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	store := subscribeStore(t, "testowner/testrepo-57")
	const resource = "https://github.com/testowner/testrepo/pull/57"
	if err := Subscribe(cfg, store, SubscribeParams{ResourceID: resource, SessionName: "testowner/testrepo-57"}); err != nil {
		t.Fatal(err)
	}
	if subs := watcherSubscriptions(t, home); len(subs) != 1 {
		t.Fatalf("subscriptions after Subscribe = %v, want one", subs)
	}
}

func TestIntegration_RetryCallsGithubWatcherAfterHookFailure(t *testing.T) {
	root := repoRoot(t)
	mounted := buildShippedPluginBinaries(t, root)
	copyShippedGithubResources(t, mounted[0].Dir)
	cfg := &config.Config{PluginDirs: []string{mounted[0].Dir}, Plugins: mounted}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	const resource = "https://github.com/testowner/testrepo/issues/58"
	const name = "testowner/testrepo-58"
	store := state.NewStore(t.TempDir())
	now := time.Now()
	if err := store.Put(&domain.Session{Name: name, ResourceID: resource, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	watcher := filepath.Join(mounted[0].Dir, "github-watcher")
	if err := os.Rename(watcher, watcher+".held"); err != nil {
		t.Fatal(err)
	}
	wired, msg := wireDeliveryOnSetup(cfg, store, name, resource, nil, false)
	if wired || msg == "" {
		t.Fatalf("first attempt wired=%v error=%q", wired, msg)
	}
	if err := os.Rename(watcher+".held", watcher); err != nil {
		t.Fatal(err)
	}
	if errs := flushPendingDelivery(cfg, store, name); len(errs) != 0 {
		t.Fatalf("retry errors = %v", errs)
	}
	if subs := watcherSubscriptions(t, home); len(subs) != 1 {
		t.Fatalf("subscriptions after retry = %v, want one", subs)
	}
	if n := subscriptionRetryCount(t, store, name, retrySubscribe, resource, false); n != 0 {
		t.Fatalf("pending retries = %d", n)
	}
}

// TestIntegration_CreateSubscribesToGithubWatcher exercises Create itself
// against the real shipped github-watcher hooks, not subscribeIfWired
// directly as above.
func TestIntegration_CreateSubscribesToGithubWatcher(t *testing.T) {
	workspaceDirsRoot := setupE2ERepo(t)
	home := os.Getenv("HOME")
	setupFakeScripts(t)
	store := state.NewStore(t.TempDir())

	cfg := writeIntegrationFixture(t, workspaceDirsRoot, "default",
		[]taskFixture{{id: "noop", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "noop"}},
	)
	useShippedGithubResources(t, cfg, "default")

	url := "https://github.com/testowner/testrepo/issues/55"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	subs := watcherSubscriptions(t, home)
	if len(subs) != 1 {
		t.Fatalf("subscriptions after Create = %v, want exactly one", subs)
	}
}

// TestIntegration_DestroyUnsubscribesFromGithubWatcher is the previous
// test's teardown counterpart, against the real shipped hooks.
func TestIntegration_DestroyUnsubscribesFromGithubWatcher(t *testing.T) {
	workspaceDirsRoot := setupE2ERepo(t)
	home := os.Getenv("HOME")
	setupFakeScripts(t)
	store := state.NewStore(t.TempDir())

	cfg := writeIntegrationFixture(t, workspaceDirsRoot, "default",
		[]taskFixture{{id: "noop", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "noop"}},
	)
	useShippedGithubResources(t, cfg, "default")

	url := "https://github.com/testowner/testrepo/issues/56"
	sessionName := "testowner/testrepo-56"
	if _, err := Create(cfg, store, CreateParams{URL: url}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if subs := watcherSubscriptions(t, home); len(subs) != 1 {
		t.Fatalf("precondition: subscriptions after Create = %v, want exactly one", subs)
	}

	if _, err := Destroy(cfg, store, DestroyParams{Identifier: sessionName, Force: true}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	if subs := watcherSubscriptions(t, home); len(subs) != 0 {
		t.Fatalf("subscriptions after Destroy = %v, want none", subs)
	}
}
