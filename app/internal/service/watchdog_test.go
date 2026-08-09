package service

import (
	"strings"
	"testing"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/contracts/event"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// healthcheckFixtureConfig declares one run-scoped task "runner" whose
// healthcheck is the given shell command, plus a bare (no-outputs-required)
// "initial" node so LoadTaskDefinitions has a workflow to resolve against.
func healthcheckFixtureConfig(t *testing.T, healthcheck string) *config.Config {
	t.Helper()
	return writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{
		id:          "runner",
		scope:       contract.TaskScopeRun,
		healthcheck: healthcheck,
	}}, []nodeFixture{{id: "initial", uses: "runner"}})
}

func TestEvaluateHealth_NoRunScopedTasksIsHealthy(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeSession, TaskID: "runner", Status: contract.TaskStatusProduced},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if !report.Healthy {
		t.Fatalf("report = %+v, want healthy (nothing run-scoped to probe)", report)
	}
}

func TestEvaluateHealth_PassingHealthcheckIsHealthy(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "true")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if !report.Healthy {
		t.Fatalf("report = %+v, want healthy", report)
	}
}

func TestEvaluateHealth_FailingHealthcheckIsUnhealthy(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.Healthy || !strings.Contains(report.Reason, "initial") {
		t.Fatalf("report = %+v, want unhealthy naming the failing instance", report)
	}
}

// A healthcheck must see its own node's resolved inputs (e.g. tmux_session),
// not just .Self outputs — needed to re-derive stale pid/session_id/
// socket_path when the pane's process restarts under it.
func TestEvaluateHealth_HealthcheckCanReadNodeInputs(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, `[ "{{.Inputs.tmux_session}}" = "work:0" ]`)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:  contract.TaskScopeRun,
			TaskID: "runner",
			Status: contract.TaskStatusProduced,
			Inputs: map[string]any{"tmux_session": "work:0"},
		},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if !report.Healthy {
		t.Fatalf("report = %+v, want healthy (node inputs must be visible to healthcheck)", report)
	}
}

func TestSessionRunAndHealthState_HealthcheckBacked(t *testing.T) {
	tests := []struct {
		name        string
		healthcheck string
		tasks       map[string]*contract.TaskState
		wantRun     domain.RunState
		wantHealth  domain.HealthState
	}{
		{
			name:        "down when no run scoped task is produced",
			healthcheck: "false",
			tasks: map[string]*contract.TaskState{
				"initial": {Scope: contract.TaskScopeSession, TaskID: "runner", Status: contract.TaskStatusProduced},
			},
			wantRun:    domain.RunDown,
			wantHealth: domain.HealthUndeclared,
		},
		{
			name:        "healthy when produced run scoped healthcheck passes",
			healthcheck: "true",
			tasks: map[string]*contract.TaskState{
				"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
			},
			wantRun:    domain.RunUp,
			wantHealth: domain.HealthHealthy,
		},
		{
			name:        "unhealthy when produced run scoped healthcheck fails",
			healthcheck: "false",
			tasks: map[string]*contract.TaskState{
				"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
			},
			wantRun:    domain.RunUp,
			wantHealth: domain.HealthUnhealthy,
		},
		{
			name:        "undeclared when produced run scoped task has no healthcheck",
			healthcheck: "",
			tasks: map[string]*contract.TaskState{
				"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
			},
			wantRun:    domain.RunUp,
			wantHealth: domain.HealthUndeclared,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			cfg := healthcheckFixtureConfig(t, tt.healthcheck)
			seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", tt.tasks)

			s := store.Get("owner/repo-1")
			if gotRun := sessionRunState(s); gotRun != tt.wantRun {
				t.Errorf("sessionRunState = %q, want %q", gotRun, tt.wantRun)
			}
			if gotHealth := sessionHealthState(cfg, store, "owner/repo-1"); gotHealth != tt.wantHealth {
				t.Errorf("sessionHealthState = %q, want %q", gotHealth, tt.wantHealth)
			}
		})
	}
}

func TestWatchdogTick_PushesDeadToHealthyParent(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil) // no run tasks: vacuously healthy
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")

	reports, err := WatchdogTick(cfg, store)
	if err != nil {
		t.Fatalf("WatchdogTick: %v", err)
	}
	if len(reports) != 1 || reports[0].Healthy {
		t.Fatalf("reports = %+v, want one unhealthy report", reports)
	}
	if !reports[0].Pushed || reports[0].PushTarget != "owner/repo-orchestrator" {
		t.Fatalf("report = %+v, want pushed to the parent", reports[0])
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalDead}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 || evs[0].Metadata[event.MetaOriginSession] != "owner/repo-1" {
		t.Fatalf("parent dead events = %+v", evs)
	}

	ws := store.Get("owner/repo-1").Watchdog
	if ws == nil || ws.DeadAt.IsZero() || ws.CheckedAt.IsZero() {
		t.Fatalf("origin WatchdogState = %+v, want persisted dead result", ws)
	}
}

// A dead sibling reviewer whose ParentSession is a "root:X" pseudo-parent
// (domain.ImplicitRootParent) must still deliver to the real session X, not
// fall through as undeliverable because "root:X" isn't itself a session.
func TestWatchdogTick_PushesDeadToRootPrefixedParent(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "", nil) // no run tasks: vacuously healthy
	seedSession(t, store, "owner/repo-reviewer", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "owner/repo-reviewer", "root:owner/repo-1")

	reports, err := WatchdogTick(cfg, store)
	if err != nil {
		t.Fatalf("WatchdogTick: %v", err)
	}
	if len(reports) != 1 || reports[0].Healthy {
		t.Fatalf("reports = %+v, want one unhealthy report", reports)
	}
	if !reports[0].Pushed || reports[0].PushTarget != "owner/repo-1" {
		t.Fatalf("report = %+v, want pushed to owner/repo-1 (the root: target)", reports[0])
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{Types: []string{event.TypeTerminalDead}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 || evs[0].Metadata[event.MetaOriginSession] != "owner/repo-reviewer" {
		t.Fatalf("target dead events = %+v", evs)
	}
	if evs[0].Metadata["undeliverable"] == "true" {
		t.Fatal("event recorded as undeliverable, want delivered to the resolved root: target")
	}
}

func TestWatchdogTick_SkipsDeadIntermediateParent(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-grandparent", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-parent", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "owner/repo-parent", "owner/repo-grandparent")
	setParent(t, store, "owner/repo-1", "owner/repo-parent")

	reports, err := WatchdogTick(cfg, store)
	if err != nil {
		t.Fatalf("WatchdogTick: %v", err)
	}
	// Both owner/repo-parent and owner/repo-1 fail the same "false"
	// healthcheck and are each independently probed and pushed: the parent
	// reports its own death directly to the grandparent, and owner/repo-1's
	// death skips over its dead immediate parent (D4) straight to the
	// grandparent too — so the grandparent ends up with two dead events.
	if len(reports) != 2 {
		t.Fatalf("reports = %+v, want 2", reports)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-grandparent", 0, event.Filter{Types: []string{event.TypeTerminalDead}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("grandparent dead events = %+v, want 2 (direct from parent + skip-delivered from origin)", evs)
	}
	origins := map[string]string{}
	for _, ev := range evs {
		origins[ev.Metadata[event.MetaOriginSession]] = ev.Metadata[event.MetaRelation]
	}
	if origins["owner/repo-parent"] != string(domain.RelationChild) {
		t.Fatalf("origins = %+v, want owner/repo-parent delivered directly as child", origins)
	}
	if origins["owner/repo-1"] != string(domain.RelationDescendant) {
		t.Fatalf("origins = %+v, want owner/repo-1 skip-delivered as descendant (its immediate parent was dead)", origins)
	}
}

func TestWatchdogTick_FallsBackToLocalRecordWhenNoLiveAncestor(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	// No parent at all.

	reports, err := WatchdogTick(cfg, store)
	if err != nil {
		t.Fatalf("WatchdogTick: %v", err)
	}
	if len(reports) != 1 || reports[0].Pushed {
		t.Fatalf("reports = %+v, want one unpushed (no live ancestor) report", reports)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-1", 0, event.Filter{Types: []string{event.TypeTerminalDead}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 || evs[0].Metadata["undeliverable"] != "true" {
		t.Fatalf("local dead record = %+v, want one undeliverable record on the origin's own log", evs)
	}
}

func TestWatchdogTick_DoesNotRepushOnRepeatedTick(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")

	if _, err := WatchdogTick(cfg, store); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if _, err := WatchdogTick(cfg, store); err != nil {
		t.Fatalf("second tick: %v", err)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalDead}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("dead events after 2 ticks = %d, want 1 (deduped)", len(evs))
	}
}

func TestWatchdogTick_SkipsSessionsWithNoRunScopeUp(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusCleaned},
	})

	reports, err := WatchdogTick(cfg, store)
	if err != nil {
		t.Fatalf("WatchdogTick: %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("reports = %+v, want none (session is down, not dead)", reports)
	}
}
