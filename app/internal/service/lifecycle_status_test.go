package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// genericResolverFields is a workspace-provider resolver pair with no
// hosting-provider-shaped identifier, for a test that only needs Create's
// URL-dispatch path to resolve to some session name, not any specific
// provider's own URL convention.
const genericResolverFields = `match = '^acceptance://(?P<acct>[^/]+)/(?P<num>\d+)'
name  = { expr = "'sess-' + match.acct + '-' + match.num" }
`

// genericProviderRunningScript mirrors providerRunningScript
// (dispatch_test.go) but under genericResolverFields instead of a
// specific-hosting-provider-shaped match pattern.
func genericProviderRunningScript(id, script string) string {
	return providerDoc(id, genericResolverFields, `[%[1]s.setup]
type    = "exec"
command = "sh"
args    = ["-c", `+fmt.Sprintf("%q", script)+`, "provider"]
`)
}

func TestCreate_LeavesSessionStatusDown(t *testing.T) {
	store := testStore(t)
	workdir := filepath.Join(t.TempDir(), "wd")
	cfg := writeWorkflowFixture(t, t.TempDir(), "wf",
		[]taskFixture{{id: "probe", scope: "session", setup: `echo '{}'`}},
		[]nodeFixture{{id: "probe"}})
	provID, body := providerAside("wf", genericProviderRunningScript("wf", fmt.Sprintf(`mkdir -p %s
echo '{"workspace_dir":"%s"}'
`, workdir, workdir)))
	workspacesDir := filepath.Join(cfg.BaseDir, "workspaces")
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspacesDir, provID+".toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	addWorkflowFields(t, cfg, "wf", "workspace_provider = \""+provID+"\"\n")

	if _, err := Create(cfg, store, CreateParams{URL: "acceptance://acct/9"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	s := store.Get("sess-acct-9+wf")
	if s == nil {
		t.Fatal("session not persisted")
	}
	if s.Status != contract.SessionStatusDown {
		t.Errorf("Status after Create = %q, want %q", s.Status, contract.SessionStatusDown)
	}
}

func TestUp_SuccessfulLaunchSetsStatusUp(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	sessionName := "work-20"
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`}},
		[]nodeFixture{{id: "runtime"}},
	)
	seedSessionWithNodes(t, store, sessionName, "acct", 20, "default", nil)
	if s := store.Get(sessionName); s.Status != contract.SessionStatusDown {
		t.Fatalf("precondition: Status = %q, want %q", s.Status, contract.SessionStatusDown)
	}

	if _, err := Up(cfg, store, UpParams{Identifier: sessionName}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if s := store.Get(sessionName); s.Status != contract.SessionStatusUp {
		t.Fatalf("Status after successful Up = %q, want %q", s.Status, contract.SessionStatusUp)
	}
	wakes, _, _, err := eventlog.NewStore(store.Dir()).List(sessionName, 0, event.Filter{Types: []string{event.TypeSessionWake}})
	if err != nil || len(wakes) != 0 {
		t.Fatalf("ordinary Up wake events = %v, %v; want none", wakes, err)
	}
}

func TestUp_FailedLaunchLeavesStatusDown(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	sessionName := "work-21"
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "runtime", scope: "run", setup: `exit 1`}},
		[]nodeFixture{{id: "runtime"}},
	)
	seedSessionWithNodes(t, store, sessionName, "acct", 21, "default", nil)

	if _, err := Up(cfg, store, UpParams{Identifier: sessionName}); err == nil {
		t.Fatal("Up: want an error from the failing setup script")
	}
	if s := store.Get(sessionName); s.Status != contract.SessionStatusDown {
		t.Fatalf("Status after failed Up = %q, want %q (failure-atomic)", s.Status, contract.SessionStatusDown)
	}
}

func TestDown_SetsStatusDown(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	sessionName := "work-22"
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "runtime"}},
	)
	seedSessionWithNodes(t, store, sessionName, "acct", 22, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: sessionName}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if s := store.Get(sessionName); s.Status != contract.SessionStatusUp {
		t.Fatalf("precondition: Status after Up = %q, want %q", s.Status, contract.SessionStatusUp)
	}

	if _, err := Down(cfg, store, DownParams{Identifier: sessionName}); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if s := store.Get(sessionName); s.Status != contract.SessionStatusDown {
		t.Fatalf("Status after Down = %q, want %q", s.Status, contract.SessionStatusDown)
	}
}

func TestDown_SleepAndFullStopTransitions(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "runtime"}})
	seedSessionWithNodes(t, store, "sleep-transition", "acct", 23, "default", nil)
	if _, err := Down(cfg, store, DownParams{Identifier: "sleep-transition", Sleep: true}); err != nil {
		t.Fatal(err)
	}
	if got := store.Get("sleep-transition"); got.SleepSince.IsZero() || got.Status != contract.SessionStatusDown {
		t.Fatalf("sleep state = %+v, want down with sleep marker", got)
	}
	if _, err := RequestWake(store, "sleep-transition", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := Down(cfg, store, DownParams{Identifier: "sleep-transition"}); err != nil {
		t.Fatal(err)
	}
	if got := store.Get("sleep-transition"); !got.SleepSince.IsZero() {
		t.Fatalf("full stop retained sleep marker: %v", got.SleepSince)
	}
	if position, err := SleepQueuePosition(store, "sleep-transition"); err != nil || position != 0 {
		t.Fatalf("full stop queue position = %d, %v", position, err)
	}
	if _, err := Down(cfg, store, DownParams{Identifier: "sleep-transition", Sleep: true}); err != nil {
		t.Fatal(err)
	}
	if got := store.Get("sleep-transition"); got.SleepSince.IsZero() {
		t.Fatal("sleep after full stop did not set marker")
	}
	if _, err := Up(cfg, store, UpParams{Identifier: "sleep-transition"}); err != nil {
		t.Fatal(err)
	}
	wakes, _, _, err := eventlog.NewStore(store.Dir()).List("sleep-transition", 0, event.Filter{Types: []string{event.TypeSessionWake}})
	if err != nil || len(wakes) != 1 || wakes[0].Metadata["result"] != "succeeded" {
		t.Fatalf("sleep wake events = %v, %v; want one success", wakes, err)
	}
}

func TestRequestWake_SkipsFinalizedSession(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	seedSessionWithNodes(t, store, "finalized", "acct", 1, "default", nil)
	if err := store.Update("finalized", func(s *domain.Session) error {
		s.SleepSince = now.Add(-time.Minute)
		s.Tasks = map[string]*contract.TaskState{"initial": {Scope: contract.TaskScopeSession, Status: contract.TaskStatusProduced, FinalizedAt: now}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	requested, err := RequestWake(store, "finalized", now)
	if err != nil || requested {
		t.Fatalf("RequestWake = %t, %v; want no wake", requested, err)
	}
	if position, err := SleepQueuePosition(store, "finalized"); err != nil || position != 0 {
		t.Fatalf("finalized queue position = %d, %v", position, err)
	}
}

func TestUp_FailedForceRecreateFromUpSessionSetsStatusDown(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	store := testStore(t)
	sessionName := "work-23"
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "runtime", scope: "run", setup: `echo '{}'`, cleanup: "true"}},
		[]nodeFixture{{id: "runtime"}},
	)
	seedSessionWithNodes(t, store, sessionName, "acct", 23, "default", nil)
	if _, err := Up(cfg, store, UpParams{Identifier: sessionName}); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if s := store.Get(sessionName); s.Status != contract.SessionStatusUp {
		t.Fatalf("precondition: Status after Up = %q, want %q", s.Status, contract.SessionStatusUp)
	}

	if _, err := Up(cfg, store, UpParams{Identifier: sessionName, ForceRecreate: true}); err == nil {
		t.Fatal("Up --force-recreate: want an error (fixture workflow declares no workspace provider)")
	}
	if s := store.Get(sessionName); s.Status != contract.SessionStatusDown {
		t.Fatalf("Status after failed force-recreate = %q, want %q", s.Status, contract.SessionStatusDown)
	}
}

func TestDown_PlanConstructionFailureSetsStatusDown(t *testing.T) {
	store := testStore(t)
	sessionName := "work-24"
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{{id: "runtime", scope: "run", cleanup: "true"}},
		[]nodeFixture{{id: "runtime"}},
	)
	if err := os.WriteFile(filepath.Join(cfg.BaseDir, "tasks", "broken.toml"), []byte("scope = \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedSessionWithNodes(t, store, sessionName, "acct", 24, "default", map[string]*contract.TaskState{
		"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
	})
	if err := store.Update(sessionName, func(s *domain.Session) error {
		s.Status = contract.SessionStatusUp
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := Down(cfg, store, DownParams{Identifier: sessionName}); err == nil {
		t.Fatal("Down: want an error when plan construction fails")
	}
	if s := store.Get(sessionName); s.Status != contract.SessionStatusDown {
		t.Fatalf("Status after failed Down = %q, want %q", s.Status, contract.SessionStatusDown)
	}
}
