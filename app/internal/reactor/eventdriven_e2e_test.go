//go:build integration

package reactor

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventbus"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/plugins"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
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

// buildGithubPluginBinaries compiles plect, github-worktree, and
// github-watcher into a temp bin dir, prepends it to PATH, and returns the
// mounted-plugin entry the shipped worktree.toml's `{{bin ...}}` references
// need to resolve. Mirrors app/internal/service's own
// buildWorkspaceProviderBinaries — duplicated here rather than shared,
// because that helper is unexported in a package this one cannot import (see
// the comment on TestE2E_TaskSetupResourceDeliversRealWatcherEventToReactiveTick
// for why this test lives in this package at all).
func buildGithubPluginBinaries(t *testing.T, root string) []plugins.Mounted {
	t.Helper()
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	build := func(moduleDir, pkg, out string) {
		t.Helper()
		cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, out), pkg)
		cmd.Dir = filepath.Join(root, moduleDir)
		cmd.Env = append(os.Environ(), goToolCachesForE2E...)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("build %s: %v", out, err)
		}
	}
	build("app", "./cmd/plect", "plect")
	build(filepath.Join("plugins", "github", "src"), "./cmd/github-worktree", "github-worktree")
	build(filepath.Join("plugins", "github", "src"), "./cmd/github-watcher", "github-watcher")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return []plugins.Mounted{{
		ID:  "official/github",
		Dir: binDir,
		Manifest: plugins.Manifest{Executables: []plugins.Executable{
			{Name: "github-worktree", Path: "github-worktree"},
			{Name: "github-watcher", Path: "github-watcher"},
		}},
	}}
}

// fakeGhForWatcherPoll writes a `gh` stand-in answering the real watcher
// poller's own `gh api ... -i` calls. Only the pulls/<number> endpoint's
// mergeable_state varies (read fresh from stateFile on every invocation);
// check-runs/status answer an empty-but-parseable response, which the
// poller's fetchChecks tolerates without affecting the diff this test drives.
// This is independent of the task document's own resource_observer below,
// which never shells out to gh at all — see this file's own top-level
// comment for why two independent fact sources are in play.
func fakeGhForWatcherPoll(t *testing.T, binDir, stateFile string) {
	t.Helper()
	script := `#!/usr/bin/env bash
path="$2"
case "$path" in
  */pulls/*)
    state=$(cat "` + stateFile + `" 2>/dev/null || echo unknown)
    printf 'HTTP/1.1 200 OK\n\n{"state":"open","merged":false,"sha":"abc1234","mergeable_state":"%s","draft":false}\n' "$state"
    ;;
  *)
    printf 'HTTP/1.1 200 OK\n\n'
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runWatcherServeOnce runs `github-watcher serve` long enough for its
// synchronous startup Sweep+Tick to complete (it ticks once immediately,
// before ever reaching its --interval ticker), then kills it — a long
// --interval means it never re-ticks on its own, so killing it after the
// startup tick is equivalent to "poll exactly once".
func runWatcherServeOnce(t *testing.T) {
	t.Helper()
	cmd := exec.Command("github-watcher", "serve", "--interval", "1h")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start github-watcher serve: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// watcherSubscriptions reads the real github-watcher's on-disk subscription
// registry directly — this package never imports the plugin's watcher
// package, so this is the only way to see what the real subscribe hook (run
// by service.TaskSetup's subscribeIfWired) actually persisted. dataHome is
// the XDG_DATA_HOME this test set explicitly (not a bare $HOME), so the
// registry sits directly under it rather than under a "share/.local" nesting.
func watcherSubscriptions(t *testing.T, dataHome string) map[string]json.RawMessage {
	t.Helper()
	path := filepath.Join(dataHome, "github-watcher", "subscriptions.json")
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

func waitUntilOrFatal(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}

// writeEventDrivenObserverDoc writes a resource_observer whose facts this
// test flips mid-run by calling it again — matching e2eObserver's own
// `^fixture://` match pattern loosened to '.', which (an unanchored
// single-char regex) matches any non-empty resource id, including the real
// https://github.com/... PR URL this test's instance binds.
func writeEventDrivenObserverDoc(t *testing.T, baseDir string, facts map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	// %q escapes the JSON's own embedded quotes the same way TOML's basic
	// string syntax requires, matching app/internal/service's
	// stubObservedFacts test helper (a proven fixture-writer for exactly this
	// "cat a heredoc of JSON" observer shape).
	script := fmt.Sprintf("%q", "cat <<'JSON'\n"+string(encoded)+"\nJSON")
	doc := "[fixture]\nkind  = \"resource_observer\"\nmatch = '.'\n\n" +
		"[fixture.observe]\ntype   = \"shell\"\nscript = " + script + "\n\n" +
		"[fixture.state_schema]\ntype = \"object\"\n\n[fixture.state_schema.properties]\nchecks_status = {}\n"
	writeFile(t, filepath.Join(baseDir, "resources", "fixture.toml"), doc)
}

// This test lives in package reactor, not app/internal/service (where its
// setup logic would read more naturally), because app/internal/reactor
// already imports app/internal/service (reactor.go, for service.TickSession)
// — a test needing both service.TaskSetup and this package's own
// sessionReactor from the service side would be an import cycle. Living here
// instead is the only direction the existing dependency edge allows.
//
// TestE2E_TaskSetupResourceDeliversRealWatcherEventToReactiveTick is the
// acceptance-level pin for issue-234-shaped delivery wiring: binding a task
// instance to a resource via TaskSetup's --resource (subscribeIfWired,
// running the real shipped GitHub workspace provider's `subscribe` hook
// against the real github-watcher binary) must make a real watcher poll
// cycle's published event actually reach the session's event log and drive
// done_when to satisfied through a reactive tick — never a manually-called
// TickSession/plect tick.
func TestE2E_TaskSetupResourceDeliversRealWatcherEventToReactiveTick(t *testing.T) {
	xdgHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdgHome)
	t.Setenv("HOME", xdgHome)

	root := repoRootForE2E(t)
	mounted := buildGithubPluginBinaries(t, root)
	workspacesDir := filepath.Join(mounted[0].Dir, "config", "workspaces")
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shipped, err := os.ReadFile(filepath.Join(root, "plugins", "github", "config", "workspaces", "worktree.toml"))
	if err != nil {
		t.Fatalf("read shipped github workspace provider: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacesDir, "github.toml"), shipped, 0o644); err != nil {
		t.Fatal(err)
	}

	mergeableStateFile := filepath.Join(t.TempDir(), "mergeable_state")
	if err := os.WriteFile(mergeableStateFile, []byte("unknown"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeGhForWatcherPoll(t, mounted[0].Dir, mergeableStateFile)

	const prURL = "https://github.com/eventdriven/repo/pull/9"
	const session = "eventdriven/repo-9+work"
	const parent = "eventdriven/repo-parent"

	base := t.TempDir()
	writeEventDrivenObserverDoc(t, base, map[string]any{"checks_status": "PENDING"})
	writeFile(t, filepath.Join(base, "tasks", "work.toml"), `[work]
kind              = "task"
description       = "work fixture"
resource_observer = "fixture"
instructions      = [{ text = "Carry out the work." }]

[work.done_when]
all = [
  { check = "resource.state.checks_status", eq = "SUCCESS" },
]
`)
	cfg := &config.Config{
		BaseDir:    base,
		PluginDirs: []string{mounted[0].Dir},
		Plugins:    mounted,
	}

	st := state.NewStore("")
	if err := st.Put(&domain.Session{Name: parent}); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(&domain.Session{
		Name:          session,
		ParentSession: parent,
		// A live run-scoped task keeps the reactor's drain loop active,
		// mirroring a real session's persistent tmux/agent node — matching
		// TestSessionReactor_ReactiveTickReachesDoneWhenConsequence's own
		// "claude" seed in this same package.
		Tasks: map[string]*contract.TaskState{
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
	}); err != nil {
		t.Fatal(err)
	}

	setupResult, err := service.TaskSetup(cfg, st, service.TaskSetupParams{TaskID: "work", SessionName: session, Name: "initial", Resource: prURL})
	if err != nil {
		t.Fatalf("TaskSetup: %v", err)
	}
	if !setupResult.Subscribed {
		t.Fatalf("TaskSetup did not wire delivery for %q (SubscribeError=%q)", prURL, setupResult.SubscribeError)
	}

	log := eventlog.NewStore(st.Dir())
	hub := sessionhub.NewRegistry(log, sessionhub.WithPollInterval(5*time.Millisecond))
	t.Cleanup(hub.Close)

	socket := filepath.Join(t.TempDir(), "bus.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on bus socket: %v", err)
	}
	busSrv := &http.Server{Handler: eventbus.New(log, "", hub).Routes()}
	go busSrv.Serve(ln)
	t.Cleanup(func() { busSrv.Close() })
	t.Setenv("PLECT_BUS_SOCKET", socket)

	r := &sessionReactor{
		session: session,
		cfg:     cfg,
		state:   st,
		log:     log,
		hub:     hub,
		tick:    config.TickConfig{On: []string{"github.*"}},
	}
	stop := startReactor(t, r)
	t.Cleanup(stop)
	time.Sleep(50 * time.Millisecond)

	// First poll cycle: no prior baseline for this subscription, so the
	// watcher can only establish one — never notify (summarizeChanges:
	// "initial observations produce no notifications"). Waiting for the
	// baseline to actually land means tick two's diff is against a real
	// prior value, not a race with tick one's own write.
	runWatcherServeOnce(t)
	waitUntilOrFatal(t, 10*time.Second, "watcher never established its polling baseline", func() bool {
		subs := watcherSubscriptions(t, xdgHome)
		raw, ok := subs[session+"\x00"+prURL]
		if !ok {
			return false
		}
		var sub struct {
			Last map[string]string `json:"last"`
		}
		return json.Unmarshal(raw, &sub) == nil && len(sub.Last) > 0
	})

	// Flip both independent fact sources together: the watcher's own next
	// poll now sees a changed, resolved mergeable_state (triggering a
	// publish), and the task document's own observer now reports the
	// done_when-satisfying fact a reactive tick will read.
	if err := os.WriteFile(mergeableStateFile, []byte("clean"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeEventDrivenObserverDoc(t, base, map[string]any{"checks_status": "SUCCESS"})

	runWatcherServeOnce(t)

	waitUntilOrFatal(t, 15*time.Second, "the watcher's published event never drove a reactive tick to done_when-satisfied", func() bool {
		evs, _, _, err := log.List(parent, 0, event.Filter{Types: []string{event.TypeTerminalDone}})
		return err == nil && len(evs) == 1
	})
}
