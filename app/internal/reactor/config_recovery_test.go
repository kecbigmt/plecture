package reactor

import (
	"os"
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

const workflowHeader = "\n[wf]\nkind = \"workflow\"\n[[wf.nodes]]\nid = \"claude\"\nuses = \"claude\"\n"

const heartbeatWorkflow = workflowHeader + `
[wf.tick]
heartbeat = "1ms"
`

const declaresOnOnly = workflowHeader + "\n[wf.tick]\non = [\"resource.*\"]\n"

// A workflow file removed mid-session must not take heartbeat scheduling
// down with it because the session retains its stored declaration.
func TestSessionReactor_KeepsLastGoodTickConfigWhenWorkflowLoadFails(t *testing.T) {
	base := t.TempDir()
	writeClaudeRunTask(t, base)
	workflowPath := filepath.Join(base, "workflows", "wf.toml")
	writeFile(t, workflowPath, heartbeatWorkflow)
	cfg := &config.Config{BaseDir: base, WorkspaceDirsRoot: t.TempDir()}

	dir := t.TempDir()
	log := eventlog.NewStore(dir)
	hub := sessionhub.NewRegistry(log, sessionhub.WithPollInterval(2*time.Millisecond))
	t.Cleanup(hub.Close)
	st := state.NewStore(dir)
	session := &domain.Session{
		Name:     "o/r-1",
		Workflow: "wf",
		Nodes: map[string]*contract.TaskState{
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
	}
	pinRevision(t, cfg, session)
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

// newRefreshFixture builds a reactor over a workflow file the caller can
// rewrite, wired the way production wires it (through the supervisor), and
// returns the path so a case can drive refreshTickConfig across byte states.
func newRefreshFixture(t *testing.T, body string) (*sessionReactor, string) {
	t.Helper()
	base := t.TempDir()
	writeClaudeRunTask(t, base)
	workflowPath := filepath.Join(base, "workflows", "wf.toml")
	writeFile(t, workflowPath, body)
	cfg := &config.Config{BaseDir: base, WorkspaceDirsRoot: t.TempDir()}
	dir := t.TempDir()
	log := eventlog.NewStore(dir)
	hub := sessionhub.NewRegistry(log, sessionhub.WithPollInterval(2*time.Millisecond))
	t.Cleanup(hub.Close)
	st := state.NewStore(dir)
	session := &domain.Session{
		Name:     "o/r-1",
		Workflow: "wf",
		Nodes: map[string]*contract.TaskState{
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
	}
	pinRevision(t, cfg, session)
	if err := st.Put(session); err != nil {
		t.Fatal(err)
	}
	return NewSupervisor(func() *config.Config { return cfg }, st, log, hub).buildReactor("o/r-1", session), workflowPath
}

func TestRefreshTickConfig_KeepsStoredDeclarationAcrossFileEdits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		start     string
		rewritten string
		remove    bool
		want      time.Duration
	}{
		{name: "zero-length mid-truncate", start: heartbeatWorkflow, rewritten: "", want: time.Millisecond},
		{name: "table written, keys not yet", start: heartbeatWorkflow, rewritten: workflowHeader + "\n[wf.tick]\n", want: time.Millisecond},
		{name: "unparseable", start: heartbeatWorkflow, rewritten: "this is not valid toml", want: time.Millisecond},
		{name: "workflow file gone", start: heartbeatWorkflow, remove: true, want: time.Millisecond},
		{name: "declaration changed", start: heartbeatWorkflow, rewritten: workflowHeader + "\n[wf.tick]\nheartbeat = \"7ms\"\n", want: time.Millisecond},
		{name: "narrowed but still scheduling", start: heartbeatWorkflow, rewritten: declaresOnOnly, want: time.Millisecond},
		{name: "pattern remains", start: declaresOnOnly, rewritten: "", want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, path := newRefreshFixture(t, tc.start)
			if tc.remove {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, path, tc.rewritten)
			}
			r.refreshTickConfig()
			if got := r.tick.Heartbeat.Duration; got != tc.want {
				t.Fatalf("heartbeat after re-read = %v, want %v", got, tc.want)
			}
		})
	}
}

// The rule is stated over what the declaration schedules, not over how the
// re-read failed, so a session left with a pattern but no clock keeps that
// pattern.
func TestRefreshTickConfig_KeepsPatternOnlyDeclaration(t *testing.T) {
	r, path := newRefreshFixture(t, declaresOnOnly)
	writeFile(t, path, "")
	r.refreshTickConfig()
	if len(r.tick.On) != 1 || r.tick.On[0] != "resource.*" {
		t.Fatalf("on after a re-read declaring nothing = %+v, want the previous pattern kept", r.tick.On)
	}
}
