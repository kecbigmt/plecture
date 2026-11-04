package population

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/admitstatus"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func capacityFixture(t *testing.T) (*capacityCoordinator, Definition, *state.Store, *eventlog.Store, time.Time) {
	t.Helper()
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	def := Definition{
		Workflow:   config.WorkflowFile{Address: "agent"},
		Population: config.WorkflowPopulation{Name: "dispatch", AutoDown: true},
	}
	coordinator := newCapacityCoordinator(func() *config.Config { return &config.Config{} }, store, logStore, admitstatus.NewCache(logStore))
	coordinator.setDefinitions([]Definition{def})
	return coordinator, def, store, logStore, time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
}

func TestCapacityPriorityOnlyAppliesWhenVirtualRootCapIsFull(t *testing.T) {
	for _, tc := range []struct {
		name     string
		seedFull bool
		wantErr  bool
	}{
		{name: "under cap"},
		{name: "at cap", seedFull: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
			writeDefinition(t, cfg.BaseDir, "provider", fmt.Sprintf(`[provider]
kind = "workspace_provider"
match = "^urn:case:(?P<id>[A-Za-z0-9]+)$"
name = { from = "match.id" }
[provider.setup]
type = "exec"
command = "printf"
args = ['{"workspace_dir":"%s","branch":"main"}']
[provider.outputs_schema]
type = "object"
`, cfg.WorkspaceDirsRoot))
			limit := 1
			cfg.MaxUpChildren = &limit
			definitions, err := Load(cfg)
			if err != nil {
				t.Fatal(err)
			}
			def := definitions[0]
			store := state.NewStore(t.TempDir())
			logStore := eventlog.NewStore(store.Dir())
			coordinator := newCapacityCoordinator(func() *config.Config { return cfg }, store, logStore, admitstatus.NewCache(logStore))
			coordinator.setDefinitions(definitions)
			if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
				population.Members["urn:case:new"] = &state.PopulationMember{ResourceID: "urn:case:new", PendingUp: true}
				population.Members["urn:case:owned"] = &state.PopulationMember{
					ResourceID: "urn:case:owned", SessionName: "owned+agent", PendingUp: true,
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := store.Put(&contract.Session{
				Name: "owned+agent", ResourceID: "urn:case:owned",
				Population: &contract.PopulationProvenance{Workflow: def.Workflow.Address, Name: def.Population.Name},
			}); err != nil {
				t.Fatal(err)
			}
			if tc.seedFull {
				if err := store.Put(&contract.Session{Name: "manual", Tasks: map[string]*contract.TaskState{
					"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
				}}); err != nil {
					t.Fatal(err)
				}
			}

			outcome, err := coordinator.up(context.Background(), def, "urn:case:new", map[string]any{"resource": "urn:case:new"})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "takes priority") {
					t.Fatalf("up error = %v, want existing-member priority after a cap rejection", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("up under the virtual-root cap: %v", err)
			}
			if outcome.SessionName == "" || store.Get(outcome.SessionName) == nil {
				t.Fatalf("session = %q, want a newly admitted population session", outcome.SessionName)
			}
			if outcome.AlreadyUp {
				t.Fatal("a newly admitted session reported as already up")
			}
		})
	}
}

// Both a broken and a genuinely idle member occupy the cap, so success here
// can only mean the broken one stopped blocking the idle one's eviction.
func TestCapacityPriorityIgnoresMemberWithNonCapacityFailure(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	writeDefinition(t, cfg.BaseDir, "provider", fmt.Sprintf(`[provider]
kind = "workspace_provider"
match = "^urn:case:(?P<id>[A-Za-z0-9]+)$"
name = { from = "match.id" }
[provider.setup]
type = "exec"
command = "printf"
args = ['{"workspace_dir":"%s","branch":"main"}']
[provider.outputs_schema]
type = "object"
`, cfg.WorkspaceDirsRoot))
	limit := 2
	cfg.MaxUpChildren = &limit
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	definitions[0].Population.AutoDown = true // required for the idle member below to be evictable
	def := definitions[0]
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	coordinator := newCapacityCoordinator(func() *config.Config { return cfg }, store, logStore, admitstatus.NewCache(logStore))
	coordinator.setDefinitions(definitions)

	base := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	addCapacityMember(t, def, store, logStore, "idle+agent", "urn:case:idle", base, base.Add(time.Minute))

	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Members["urn:case:new"] = &state.PopulationMember{ResourceID: "urn:case:new", PendingUp: true}
		population.Members["urn:case:broken"] = &state.PopulationMember{
			ResourceID: "urn:case:broken", SessionName: "broken+agent", PendingUp: true,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&contract.Session{
		Name: "broken+agent", ResourceID: "urn:case:broken",
		Workflow:   def.Workflow.Address,
		Population: &contract.PopulationProvenance{Workflow: def.Workflow.Address, Name: def.Population.Name},
		Nodes: map[string]*contract.TaskState{
			"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := logStore.Append(event.Event{
		SessionName: "broken+agent",
		Time:        base,
		Type:        event.TypeWorkflowPopulationFailure,
		Direction:   event.Internal,
		Metadata:    map[string]string{"reason": "input", "resource": "urn:case:broken"},
	}); err != nil {
		t.Fatal(err)
	}

	outcome, err := coordinator.up(context.Background(), def, "urn:case:new", map[string]any{"resource": "urn:case:new"})
	if err != nil {
		t.Fatalf("up: %v, want the chronically broken member to lose priority so the idle member is evicted instead", err)
	}
	if outcome.SessionName == "" || store.Get(outcome.SessionName) == nil {
		t.Fatalf("session = %q, want a newly admitted population session", outcome.SessionName)
	}
	if idle := store.Get("idle+agent"); idle == nil || cfg.RunScopeUp(idle) || idle.SleepSince.IsZero() {
		t.Fatalf("idle session = %+v, want it sleeping to free capacity", idle)
	}
}

func TestCapacityPriorityRetainedAfterCapacityRefusal(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Members["urn:case:new"] = &state.PopulationMember{ResourceID: "urn:case:new", PendingUp: true}
		population.Members["urn:case:queued"] = &state.PopulationMember{
			ResourceID: "urn:case:queued", SessionName: "queued+agent", PendingUp: true,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := logStore.Append(event.Event{
		SessionName: "queued+agent",
		Time:        base,
		Type:        event.TypeWorkflowPopulationFailure,
		Direction:   event.Internal,
		Metadata:    map[string]string{"reason": "capacity", "resource": "urn:case:queued"},
	}); err != nil {
		t.Fatal(err)
	}

	blocker, blocked := coordinator.pendingExistingAhead(def, "urn:case:new")
	if !blocked || blocker.session != "queued+agent" {
		t.Fatalf("blocker = %+v, blocked = %v, want the capacity-refused member to keep priority", blocker, blocked)
	}
}

// TestCapacityPriorityExcludesProvenanceConflict guards a real bug found in
// review: a provenance conflict is its own event type, not a failure, and
// was invisible to the priority classifier — a member stuck in an
// unresolvable naming conflict read as eligible and could hold priority
// forever, exactly like the original starvation bug this PR fixes.
func TestCapacityPriorityExcludesProvenanceConflict(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Members["urn:case:new"] = &state.PopulationMember{ResourceID: "urn:case:new", PendingUp: true}
		population.Members["urn:case:conflicted"] = &state.PopulationMember{
			ResourceID: "urn:case:conflicted", SessionName: "conflicted+agent", PendingUp: true,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := logStore.Append(event.Event{
		SessionName: "conflicted+agent",
		Time:        base,
		Type:        event.TypeWorkflowPopulationConflict,
		Direction:   event.Internal,
		Metadata:    map[string]string{"reason": "provenance", "resource": "urn:case:conflicted"},
	}); err != nil {
		t.Fatal(err)
	}

	if blocker, blocked := coordinator.pendingExistingAhead(def, "urn:case:new"); blocked {
		t.Fatalf("blocker = %+v, want a provenance-conflicted member to lose priority", blocker)
	}
}

// TestCapacityPriorityRecoversAfterAdmitOK guards the case
// TestCapacityPriorityIgnoresMemberWithNonCapacityFailure cannot: a member
// that failed once but has since admitted successfully must not stay
// disqualified by that stale failure the next time it goes pending.
func TestCapacityPriorityRecoversAfterAdmitOK(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Members["urn:case:new"] = &state.PopulationMember{ResourceID: "urn:case:new", PendingUp: true}
		population.Members["urn:case:recovered"] = &state.PopulationMember{
			ResourceID: "urn:case:recovered", SessionName: "recovered+agent", PendingUp: true,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := logStore.Append(event.Event{
		SessionName: "recovered+agent",
		Time:        base,
		Type:        event.TypeWorkflowPopulationFailure,
		Direction:   event.Internal,
		Metadata:    map[string]string{"reason": "input", "resource": "urn:case:recovered"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := logStore.Append(event.Event{
		SessionName: "recovered+agent",
		Time:        base.Add(time.Minute),
		Type:        event.TypeWorkflowPopulationAdmitOK,
		Direction:   event.Internal,
		Metadata:    map[string]string{"reason": "admit", "resource": "urn:case:recovered"},
	}); err != nil {
		t.Fatal(err)
	}

	blocker, blocked := coordinator.pendingExistingAhead(def, "urn:case:new")
	if !blocked || blocker.session != "recovered+agent" {
		t.Fatalf("blocker = %+v, blocked = %v, want a member that admitted successfully since its last failure to hold priority again", blocker, blocked)
	}
}

// TestCapacityRefusalSurvivesEmptyIdleCandidates guards a separate,
// pre-existing bug found while adding the test above: an empty (non-error)
// idleCandidates result must not clobber the original capacity refusal
// into a false success.
func TestCapacityRefusalSurvivesEmptyIdleCandidates(t *testing.T) {
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	writeDefinition(t, cfg.BaseDir, "provider", fmt.Sprintf(`[provider]
kind = "workspace_provider"
match = "^urn:case:(?P<id>[A-Za-z0-9]+)$"
name = { from = "match.id" }
[provider.setup]
type = "exec"
command = "printf"
args = ['{"workspace_dir":"%s","branch":"main"}']
[provider.outputs_schema]
type = "object"
`, cfg.WorkspaceDirsRoot))
	limit := 1
	cfg.MaxUpChildren = &limit
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	def := definitions[0]
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	coordinator := newCapacityCoordinator(func() *config.Config { return cfg }, store, logStore, admitstatus.NewCache(logStore))
	coordinator.setDefinitions(definitions)

	// Fills the cap with a session that is not a population member, so it
	// can never be an eviction candidate: the only path left is the
	// idleCandidates-empty fallback.
	if err := store.Put(&contract.Session{Name: "manual", Tasks: map[string]*contract.TaskState{
		"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	}}); err != nil {
		t.Fatal(err)
	}

	outcome, err := coordinator.up(context.Background(), def, "urn:case:new", map[string]any{"resource": "urn:case:new"})
	if err == nil {
		t.Fatalf("up returned no error with outcome %+v, want the capacity refusal preserved", outcome)
	}
	if outcome.SessionName != "" {
		t.Fatalf("outcome = %+v, want no session on a capacity refusal", outcome)
	}
}

func TestQueuedAdmissionWaitsForWakeBeforeInitialTask(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	writeDefinition(t, cfg.BaseDir, "provider", fmt.Sprintf(`[provider]
kind = "workspace_provider"
match = "^urn:case:(?P<id>[A-Za-z0-9]+)$"
name = { from = "match.id" }
[provider.setup]
type = "exec"
command = "printf"
args = ['{"workspace_dir":"%s","branch":"main"}']
[provider.outputs_schema]
type = "object"
`, cfg.WorkspaceDirsRoot))
	writeDefinition(t, cfg.BaseDir, "work", `[work]
kind = "effect"
scope = "run"
[work.setup]
type = "exec"
command = "printf"
args = ['{}']
[work.outputs_schema]
type = "object"
[work.health.alive]
type = "noop"
`)
	limit := 1
	cfg.MaxUpChildren = &limit
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	def := definitions[0]
	def.Population.Session.Task = "work"
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	coordinator := newCapacityCoordinator(func() *config.Config { return cfg }, store, logStore, admitstatus.NewCache(logStore))
	coordinator.setDefinitions([]Definition{def})
	engine := NewEngine(def, store, logStore, coordinator.cache, serviceHooks(func() *config.Config { return cfg }, store, def, coordinator))
	if err := store.Put(&contract.Session{Name: "manual", Tasks: map[string]*contract.TaskState{
		"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	resource := "urn:case:new"
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": resource}); err != nil {
		t.Fatalf("admit while cap is full: %v", err)
	}
	population, err := store.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members[resource]
	if member == nil || !member.PendingUp || member.AdmitSuspended || member.ConsecutiveAdmitFailures != 0 || member.AdmitRetryAt.IsZero() {
		t.Fatalf("queued member = %+v, want pending retry without a failure", member)
	}
	queued := store.Get(member.SessionName)
	if queued == nil || queued.SleepSince.IsZero() || cfg.RunScopeUp(queued) {
		t.Fatalf("queued session = %+v, want sleeping with run scope down", queued)
	}
	events, _, _, err := logStore.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeWorkflowPopulationUp}})
	if err != nil || len(events) != 0 {
		t.Fatalf("population up events before wake = %v, %v; want none", events, err)
	}
	if err := store.Update("manual", func(session *contract.Session) error {
		session.Tasks["runtime"].Status = contract.TaskStatusCleaned
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.WakeQueuedSessions(cfg, store); err != nil {
		t.Fatalf("wake queued session: %v", err)
	}
	events, _, _, err = logStore.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeWorkflowPopulationUp}})
	if err != nil || len(events) != 1 {
		t.Fatalf("population up events after wake = %v, %v; want one transition", events, err)
	}
	engine.now = func() time.Time { return member.AdmitRetryAt }
	if err := engine.Reconcile(ctx); err != nil {
		t.Fatalf("admit after wake: %v", err)
	}
	population, err = store.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	if got := population.Members[resource]; got.PendingUp || got.AdmitSuspended {
		t.Fatalf("member after wake = %+v, want completed admission", got)
	}
	session := store.Get(member.SessionName)
	if session == nil || session.Tasks["initial"] == nil || session.Tasks["initial"].Status != contract.TaskStatusProduced {
		t.Fatalf("initial task after wake = %+v, want produced", session)
	}
	events, _, _, err = logStore.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeWorkflowPopulationUp}})
	if err != nil || len(events) != 1 {
		t.Fatalf("population up events after admission = %v, %v; want one transition", events, err)
	}
	if _, err := service.Down(cfg, store, service.DownParams{Identifier: member.SessionName, Sleep: true}); err != nil {
		t.Fatalf("sleep admitted member: %v", err)
	}
	engine.hooks.EnsureInitial = nil
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": resource}); err != nil {
		t.Fatalf("re-admit sleeping member: %v", err)
	}
	events, _, _, err = logStore.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeWorkflowPopulationUp}})
	if err != nil || len(events) != 2 {
		t.Fatalf("population up events after direct re-admission = %v, %v; want one per transition", events, err)
	}
}

func TestAdmitDoesNotRearmWakeSuspendedMember(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
`, "uses = [\"poll\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	writeDefinition(t, cfg.BaseDir, "provider", fmt.Sprintf(`[provider]
kind = "workspace_provider"
match = "^urn:case:(?P<id>[A-Za-z0-9]+)$"
name = { from = "match.id" }
[provider.setup]
type = "exec"
command = "printf"
args = ['{"workspace_dir":"%s","branch":"main"}']
[provider.outputs_schema]
type = "object"
`, cfg.WorkspaceDirsRoot))
	writeDefinition(t, cfg.BaseDir, "work", `[work]
kind = "effect"
scope = "run"
[work.setup]
type = "exec"
command = "printf"
args = ['{}']
[work.outputs_schema]
type = "object"
[work.health.alive]
type = "noop"
`)
	limit := 1
	cfg.MaxUpChildren = &limit
	definitions, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	def := definitions[0]
	def.Population.Session.Task = "work"
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	coordinator := newCapacityCoordinator(func() *config.Config { return cfg }, store, logStore, admitstatus.NewCache(logStore))
	coordinator.setDefinitions(definitions)
	engine := NewEngine(def, store, logStore, coordinator.cache, serviceHooks(func() *config.Config { return cfg }, store, def, coordinator))
	if err := store.Put(&contract.Session{Name: "manual", Tasks: map[string]*contract.TaskState{
		"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
	}}); err != nil {
		t.Fatal(err)
	}
	earlier, err := service.Up(cfg, store, service.UpParams{Identifier: "urn:case:earlier", Workflow: def.Workflow.Address,
		Inputs: map[string]any{"resource": "urn:case:earlier"}})
	if err != nil || earlier.Result != "sleeping" || earlier.QueuePosition != 1 {
		t.Fatalf("earlier up = %+v, %v; want first queued sleeper", earlier, err)
	}
	resource := "urn:case:later"
	if err := engine.ApplyAppearance(context.Background(), map[string]any{"resource": resource}); err != nil {
		t.Fatal(err)
	}
	population, err := store.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members[resource]
	if member == nil || member.SessionName == "" || member.AdmitRetryAt.IsZero() {
		t.Fatalf("pending member = %+v, want queued admission", member)
	}
	position, err := service.SleepQueuePosition(cfg, store, member.SessionName)
	if err != nil || position != 2 {
		t.Fatalf("member queue position = %d, %v; want behind earlier sleeper", position, err)
	}
	for i := 0; i < 3; i++ {
		if _, _, _, err := logStore.Append(event.Event{SessionName: member.SessionName, Type: event.TypeSessionWake,
			Direction: event.Internal, Metadata: map[string]string{"result": "failed"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, _, err := logStore.Append(event.Event{SessionName: member.SessionName, Type: event.TypeSessionWake,
		Direction: event.Internal, Summary: "wake attempts exhausted", Metadata: map[string]string{"result": "suspended"}}); err != nil {
		t.Fatal(err)
	}
	sleepsBefore, _, _, err := logStore.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeSessionSleep}})
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return member.AdmitRetryAt }
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("admission wait while wake is suspended: %v", err)
	}
	sleepsAfter, _, _, err := logStore.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeSessionSleep}})
	if err != nil || len(sleepsAfter) != len(sleepsBefore) {
		t.Fatalf("sleep events after admission retry = %v, %v; want unchanged count %d", sleepsAfter, err, len(sleepsBefore))
	}
	status, err := service.Status(cfg, store, member.SessionName)
	if err != nil || status.Runtime.WakeReason != "wake attempts exhausted" {
		t.Fatalf("wake suspension after admission retry = %+v, %v", status, err)
	}
	for attempt := 1; attempt < admitFailureLimit; attempt++ {
		population, err = store.Population(engine.key)
		if err != nil {
			t.Fatal(err)
		}
		retryAt := population.Members[resource].AdmitRetryAt
		engine.now = func() time.Time { return retryAt }
		if err := engine.Reconcile(context.Background()); err != nil {
			t.Fatalf("admission wait %d while wake is suspended: %v", attempt+1, err)
		}
	}
	population, err = store.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	if got := population.Members[resource]; !got.PendingUp || got.AdmitSuspended || got.ConsecutiveAdmitFailures != 0 || got.AdmitRetryAt.IsZero() {
		t.Fatalf("member after suspended admission waits = %+v, want pending without failures", got)
	}
	sleepsAfter, _, _, err = logStore.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeSessionSleep}})
	if err != nil || len(sleepsAfter) != len(sleepsBefore) {
		t.Fatalf("sleep events after admission waits = %v, %v; want unchanged count %d", sleepsAfter, err, len(sleepsBefore))
	}
	if _, err := service.Down(cfg, store, service.DownParams{Identifier: earlier.SessionName}); err != nil {
		t.Fatalf("remove earlier sleeper from queue: %v", err)
	}
	if err := store.Update("manual", func(session *contract.Session) error {
		session.Tasks["runtime"].Status = contract.TaskStatusCleaned
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := logStore.Append(event.Event{SessionName: member.SessionName, Type: "resource.changed", Direction: event.Inbound}); err != nil {
		t.Fatal(err)
	}
	requested, err := service.RequestWake(store, member.SessionName, time.Now())
	if err != nil || !requested {
		t.Fatalf("inbound wake request = %t, %v; want queued", requested, err)
	}
	if err := service.WakeQueuedSessions(cfg, store); err != nil {
		t.Fatalf("wake after inbound event: %v", err)
	}
	population, err = store.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	retryAt := population.Members[resource].AdmitRetryAt
	engine.now = func() time.Time { return retryAt }
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("admit after inbound wake: %v", err)
	}
	completed := store.Get(member.SessionName)
	if completed == nil || completed.Tasks["initial"] == nil || completed.Tasks["initial"].Status != contract.TaskStatusProduced {
		t.Fatalf("initial task after inbound wake = %+v, want produced", completed)
	}
}

func addCapacityMember(t *testing.T, def Definition, store *state.Store, logStore *eventlog.Store, name, resource string, created, cleared time.Time) {
	t.Helper()
	// The population row must exist before a session can reference it
	// (sessions.population_workflow/population_name FK), matching
	// production's own order: ApplyPoll/ApplyAppearance always upserts the
	// population before Reconcile ever admits a session into it.
	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Workflow = def.Workflow.Address
		population.Name = def.Population.Name
		population.Members[resource] = &state.PopulationMember{ResourceID: resource, SessionName: name, Generation: 1}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(&contract.Session{
		Name:       name,
		ResourceID: resource,
		Workflow:   def.Workflow.Address,
		Population: &contract.PopulationProvenance{Workflow: def.Workflow.Address, Name: def.Population.Name},
		Nodes: map[string]*contract.TaskState{
			"runtime": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced},
		},
		CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
	if !cleared.IsZero() {
		if _, _, _, err := logStore.Append(event.Event{
			SessionName: name,
			Time:        cleared,
			Type:        event.TypeStatusMessage,
			Metadata:    map[string]string{"cleared": "true"}, Direction: event.Internal,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCapacityCandidatesRequireExplicitIdleAndUseOldestActivity(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	addCapacityMember(t, def, store, logStore, "newer", "urn:case:newer", base, base.Add(3*time.Minute))
	addCapacityMember(t, def, store, logStore, "older", "urn:case:older", base, base.Add(time.Minute))
	addCapacityMember(t, def, store, logStore, "never-reported", "urn:case:never", base, time.Time{})

	candidates, err := coordinator.idleCandidates()
	if err != nil {
		t.Fatalf("idleCandidates: %v", err)
	}
	if len(candidates) != 2 || candidates[0].session != "older" || candidates[1].session != "newer" {
		t.Fatalf("candidates = %+v, want explicitly idle members oldest first", candidates)
	}
}

func TestCapacityCandidateIdleEvidenceIsInvalidatedByInboundEvent(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	addCapacityMember(t, def, store, logStore, "member", "urn:case:member", base, base.Add(time.Minute))
	if _, _, _, err := logStore.Append(event.Event{
		SessionName: "member",
		Time:        base.Add(2 * time.Minute),
		Type:        "external.message",
		Direction:   event.Inbound,
	}); err != nil {
		t.Fatal(err)
	}

	candidates, err := coordinator.idleCandidates()
	if err != nil {
		t.Fatalf("idleCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want inbound activity to invalidate earlier idle evidence", candidates)
	}
}

func TestCapacityCandidatesExcludeAutoDownFalse(t *testing.T) {
	coordinator, def, store, logStore, base := capacityFixture(t)
	def.Population.AutoDown = false
	coordinator.setDefinitions([]Definition{def})
	addCapacityMember(t, def, store, logStore, "member", "urn:case:member", base, base.Add(time.Minute))

	candidates, err := coordinator.idleCandidates()
	if err != nil {
		t.Fatalf("idleCandidates: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("candidates = %+v, want auto_down=false excluded", candidates)
	}
}

func setMemberAppearance(t *testing.T, def Definition, store *state.Store, resource string, at time.Time) {
	t.Helper()
	if err := store.UpdatePopulation(populationKey(def), func(population *state.PopulationState) error {
		population.Members[resource].LastAppearance = at
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityCandidatesAfterRealSetMessage(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	for _, tc := range []struct {
		name          string
		initialClear  bool
		reappear      bool
		reportMessage *string
		wantCandidate bool
	}{
		{name: "empty, new appearance, empty re-report", initialClear: true, reappear: true, reportMessage: ptr(""), wantCandidate: true},
		{name: "empty, new appearance, no re-report", initialClear: true, reappear: true},
		{name: "working message, then empty", reportMessage: ptr(""), wantCandidate: true},
		{name: "empty, repeated empty without new activity", initialClear: true, reportMessage: ptr(""), wantCandidate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			coordinator, def, store, logStore, base := capacityFixture(t)
			cleared := time.Time{}
			if tc.initialClear {
				cleared = base.Add(time.Minute)
			}
			addCapacityMember(t, def, store, logStore, "member", "urn:case:member", base, cleared)
			if !tc.initialClear {
				if _, _, _, err := logStore.Append(event.Event{
					SessionName: "member",
					Time:        base.Add(time.Minute),
					Type:        event.TypeStatusMessage,
					Direction:   event.Outbound,
					Summary:     "working",
					Metadata:    map[string]string{"text": "working", "cleared": "false"},
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.reappear {
				setMemberAppearance(t, def, store, "urn:case:member", base.Add(2*time.Minute))
			}
			if tc.reportMessage != nil {
				if err := service.SetMessage(&config.Config{}, store, "member", *tc.reportMessage, nil); err != nil {
					t.Fatalf("SetMessage: %v", err)
				}
			}

			candidates, err := coordinator.idleCandidates()
			if err != nil {
				t.Fatalf("idleCandidates: %v", err)
			}
			if got := len(candidates) == 1; got != tc.wantCandidate {
				t.Fatalf("candidates = %+v, want candidate=%v", candidates, tc.wantCandidate)
			}
		})
	}
}

func TestCapacityRepeatedEmptyReportLeavesOtherMembersUntouched(t *testing.T) {
	t.Setenv("PLECT_SESSION_NAME", "")
	coordinator, def, store, logStore, base := capacityFixture(t)
	addCapacityMember(t, def, store, logStore, "reporter", "urn:case:reporter", base, base.Add(time.Minute))
	addCapacityMember(t, def, store, logStore, "other", "urn:case:other", base, time.Time{})

	for range 2 {
		if err := service.SetMessage(&config.Config{}, store, "reporter", "", nil); err != nil {
			t.Fatalf("SetMessage: %v", err)
		}
	}

	if got := service.LatestStatusMessage(store, "other"); got != nil {
		t.Fatalf("other member message = %+v, want none", got)
	}
	candidates, err := coordinator.idleCandidates()
	if err != nil {
		t.Fatalf("idleCandidates: %v", err)
	}
	if len(candidates) != 1 || candidates[0].session != "reporter" {
		t.Fatalf("candidates = %+v, want only the reporting member", candidates)
	}
}

func ptr[T any](v T) *T { return &v }
