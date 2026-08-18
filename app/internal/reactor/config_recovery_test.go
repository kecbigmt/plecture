package reactor

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/sessionhub"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// max_heartbeat pins the quiet-tick backoff flat: these cases assert across
// many consecutive unchanged sweeps, and an uncapped heartbeat * 2^n would
// outgrow the test's own wait long before the assertion is due.
const heartbeatWorkflow = `
[tick]
heartbeat = "1ms"
max_heartbeat = "5ms"
`

// TestSessionReactor_ReArmsHeartbeatAfterWorkflowLoadRecovers reproduces the
// production wedge: while the config home was inconsistent, every workflow
// load failed, so the reactor started with no `[tick]` at all — and since
// nothing rebuilds a reactor whose session never goes down, heartbeat
// scheduling stayed off long after the config was restored, until the daemon
// process was restarted.
func TestSessionReactor_ReArmsHeartbeatAfterWorkflowLoadRecovers(t *testing.T) {
	base := t.TempDir()
	workflowPath := filepath.Join(base, "workflows", "wf.toml")
	writeFile(t, workflowPath, "this is not valid toml")
	cfg := &config.Config{BaseDir: base, WorkspaceDirsRoot: t.TempDir()}

	log := eventlog.NewStore(t.TempDir())
	hub := sessionhub.NewRegistry(log, sessionhub.WithPollInterval(2*time.Millisecond))
	t.Cleanup(hub.Close)
	st := state.NewStore(t.TempDir())
	session := &domain.Session{
		Name:     "o/r-1",
		Workflow: "wf",
		Tasks: map[string]*contract.TaskState{
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
	}
	if err := st.Put(session); err != nil {
		t.Fatal(err)
	}

	sup := NewSupervisor(func() *config.Config { return cfg }, st, log, hub)
	r := sup.buildReactor("o/r-1", session)
	r.heartbeatEvery = 5 * time.Millisecond

	var mu sync.Mutex
	ticks := 0
	r.tickFn = func(*config.Config, *state.Store, service.TickParams) (*service.CheckResult, error) {
		mu.Lock()
		defer mu.Unlock()
		ticks++
		return &service.CheckResult{}, nil
	}
	countTicks := func() int {
		mu.Lock()
		defer mu.Unlock()
		return ticks
	}

	stop := startReactor(t, r)
	defer stop()

	time.Sleep(80 * time.Millisecond) // several sweep cycles' worth
	if got := countTicks(); got != 0 {
		t.Fatalf("ticks = %d while the workflow was unloadable, want 0", got)
	}

	writeFile(t, workflowPath, heartbeatWorkflow)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if countTicks() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("heartbeat never re-armed after the workflow became loadable again; the reactor stayed wedged on its startup resolution")
}

// A workflow that stops loading mid-life must not take heartbeat scheduling
// down with it: the last good declaration stands, matching the fail-safe the
// daemon's own periodic config refresh applies.
func TestSessionReactor_KeepsLastGoodTickConfigWhenWorkflowLoadFails(t *testing.T) {
	base := t.TempDir()
	workflowPath := filepath.Join(base, "workflows", "wf.toml")
	writeFile(t, workflowPath, heartbeatWorkflow)
	cfg := &config.Config{BaseDir: base, WorkspaceDirsRoot: t.TempDir()}

	log := eventlog.NewStore(t.TempDir())
	hub := sessionhub.NewRegistry(log, sessionhub.WithPollInterval(2*time.Millisecond))
	t.Cleanup(hub.Close)
	st := state.NewStore(t.TempDir())
	session := &domain.Session{
		Name:     "o/r-1",
		Workflow: "wf",
		Tasks: map[string]*contract.TaskState{
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
	}
	if err := st.Put(session); err != nil {
		t.Fatal(err)
	}

	sup := NewSupervisor(func() *config.Config { return cfg }, st, log, hub)
	r := sup.buildReactor("o/r-1", session)
	r.heartbeatEvery = 5 * time.Millisecond

	var mu sync.Mutex
	ticks := 0
	r.tickFn = func(*config.Config, *state.Store, service.TickParams) (*service.CheckResult, error) {
		mu.Lock()
		defer mu.Unlock()
		ticks++
		return &service.CheckResult{}, nil
	}
	countTicks := func() int {
		mu.Lock()
		defer mu.Unlock()
		return ticks
	}

	stop := startReactor(t, r)
	defer stop()

	deadline := time.Now().Add(2 * time.Second)
	for countTicks() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat never fired with a loadable workflow")
		}
		time.Sleep(5 * time.Millisecond)
	}

	writeFile(t, workflowPath, "this is not valid toml")
	before := countTicks()
	deadline = time.Now().Add(2 * time.Second)
	for countTicks() <= before {
		if time.Now().After(deadline) {
			t.Fatal("heartbeat stopped when the workflow became unloadable; the last good declaration must stand")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
