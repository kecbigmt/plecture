package reactor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/app/internal/sessionhub"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/contracts/event"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// newE2EConfig writes a task definition with a real done_when (not the tests
// elsewhere in this package, which never load one) so evaluateSessionActions
// has a leaf to actually evaluate — TickSession is exercised through the real
// `internal/service` code, not a stub.
func newE2EConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	tasksDir := filepath.Join(base, "tasks")
	if err := os.MkdirAll(tasksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	def := `
scope = "session"

[done_when]
all = [
  { check = "checks_status", eq = "SUCCESS" },
]
`
	if err := os.WriteFile(filepath.Join(tasksDir, "work.toml"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	return &config.Config{BaseDir: base, WorktreesRoot: t.TempDir()}
}

// TestSessionReactor_ReactiveTickReachesDoneWhenConsequence is the AC1 E2E:
// a declared-pattern event delivered to a session with a generated,
// done_when-bearing task instance reaches the done_when consequence (here,
// `satisfied` → a `tws.terminal.done` pushed to the parent) with no manual
// `tws tick` call — the reactor alone drives it, exactly mirroring "PR turns
// CI green → the review chain fires unattended".
func TestSessionReactor_ReactiveTickReachesDoneWhenConsequence(t *testing.T) {
	cfg := newE2EConfig(t)
	st := state.NewStore(t.TempDir())
	// service.TickSession's terminal push resolves its own eventlog store as
	// eventlog.NewStore(store.Dir()) — this must be the same directory the
	// reactor reads/writes through, or its output silently lands elsewhere.
	log := eventlog.NewStore(st.Dir())
	hub := sessionhub.NewRegistry(log, sessionhub.WithPollInterval(2*time.Millisecond))
	t.Cleanup(hub.Close)

	if err := st.Put(&domain.Session{Name: "o/parent"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(&domain.Session{
		Name:          "o/r-1",
		ParentSession: "o/parent",
		Tasks: map[string]*contract.TaskState{
			// A run-scope task keeps the reactor's drain loop active (mirrors a
			// real session's tmux/claude node); the session-scope "initial"
			// instance is what carries done_when.
			"claude": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
			"initial": {
				Scope:   contract.TaskScopeSession,
				TaskID:  "work",
				Status:  contract.TaskStatusProduced,
				Outputs: map[string]any{"checks_status": "SUCCESS"},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	r := &sessionReactor{
		session: "o/r-1",
		cfg:     cfg,
		state:   st,
		log:     log,
		hub:     hub,
		tick:    config.TickConfig{On: []string{"resource.*"}},
	}
	stop := startReactor(t, r)
	defer stop()
	time.Sleep(50 * time.Millisecond)

	log.Append(event.Event{SessionName: "o/r-1", Type: "resource.updated"})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		evs, _, _, err := log.List("o/parent", 0, event.Filter{Types: []string{event.TypeTerminalDone}})
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) == 1 {
			return // reached the done_when consequence with no manual tick
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("declared event never drove the reactor to push tws.terminal.done to the parent")
}
