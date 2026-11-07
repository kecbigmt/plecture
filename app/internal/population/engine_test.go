package population

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/admitstatus"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/service"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

type hookRecorder struct {
	store         *state.Store
	key           string
	ups           []string
	destroys      []string
	blockers      []string
	admissionSeen bool
	sessionUp     bool
}

func (h *hookRecorder) hooks() Hooks {
	return Hooks{
		Up: func(_ context.Context, resource string, _ map[string]any) (UpOutcome, error) {
			persisted, _ := h.store.Population(h.key)
			h.admissionSeen = persisted != nil && persisted.Members[memberName(resource)] != nil
			h.ups = append(h.ups, resource)
			return UpOutcome{SessionName: "session-" + resource, AlreadyUp: h.sessionUp}, nil
		},
		Destroy: func(_ context.Context, session, _ string, _ bool) error {
			h.destroys = append(h.destroys, session)
			return nil
		},
		Blockers: func(context.Context, string) ([]string, error) { return h.blockers, nil },
	}
}

func engineFixture(t *testing.T, autoDestroy bool) (*Engine, *hookRecorder, time.Time) {
	engine, recorder, now, _ := engineFixtureWithCache(t, autoDestroy)
	return engine, recorder, now
}

func engineFixtureWithCache(t *testing.T, autoDestroy bool) (*Engine, *hookRecorder, time.Time, *admitstatus.Cache) {
	t.Helper()
	cfg := populationConfig(t, `[source.query.poll]
type = "exec"
command = "true"
[source.query.subscribe]
type = "exec"
command = "true"
`, "uses = [\"poll\", \"subscribe\"]\npoll_every = \"1m\"", `resource = { from = "resource.id" }`)
	defs, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defs[0].Population.AutoDestroy = autoDestroy
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	recorder := &hookRecorder{store: store, key: populationKey(defs[0])}
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	cache := admitstatus.NewCache(logStore)
	engine := NewEngine(defs[0], store, logStore, cache, recorder.hooks())
	engine.now = func() time.Time { return now }
	return engine, recorder, now, cache
}

func TestAppearanceIsPersistedBeforeAdmission(t *testing.T) {
	engine, hooks, _ := engineFixture(t, false)
	if err := engine.ApplyAppearance(context.Background(), map[string]any{"resource": "urn:case:a"}); err != nil {
		t.Fatalf("ApplyAppearance: %v", err)
	}
	if !hooks.admissionSeen || len(hooks.ups) != 1 {
		t.Fatalf("persisted before admission = %v, ups = %v", hooks.admissionSeen, hooks.ups)
	}
}

func TestFullStopMemberIsNotReadmittedByAppearanceOrInbound(t *testing.T) {
	engine, hooks, now := engineFixture(t, false)
	ctx := context.Background()
	item := map[string]any{"resource": "urn:case:a"}
	if err := engine.ApplyAppearance(ctx, item); err != nil {
		t.Fatal(err)
	}
	name := "session-urn:case:a"
	if err := engine.state.Put(&domain.Session{Name: name, Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := engine.log.Append(event.Event{SessionName: name, Type: event.TypeSessionStop, Direction: event.Internal}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ApplyAppearance(ctx, item); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := engine.log.Append(event.Event{SessionName: name, Type: "resource.changed", Direction: event.Inbound}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SweepExpiry(ctx); err != nil {
		t.Fatal(err)
	}
	if len(hooks.ups) != 1 {
		t.Fatalf("admissions = %v, want only the initial admission", hooks.ups)
	}
}

func TestFullStopMemberIsNotReadmittedAfterEarlierAdmitFailure(t *testing.T) {
	engine, hooks, now, cache := engineFixtureWithCache(t, false)
	ctx := context.Background()
	item := map[string]any{"resource": "urn:case:a"}
	if err := engine.ApplyAppearance(ctx, item); err != nil {
		t.Fatal(err)
	}
	name := "session-urn:case:a"
	if err := engine.state.Put(&domain.Session{Name: name, Status: contract.SessionStatusDown, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := engine.log.Append(event.Event{SessionName: name, Type: event.TypeSessionStop, Direction: event.Internal, Metadata: map[string]string{"origin": "migration"}}); err != nil {
		t.Fatal(err)
	}
	cache.Record(name, "urn:case:a", false, "task_setup", "earlier failure")
	if err := engine.state.UpdatePopulation(engine.key, func(population *state.PopulationState) error {
		population.Members["a"].PendingUp = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ApplyAppearance(ctx, item); err != nil {
		t.Fatal(err)
	}
	if len(hooks.ups) != 1 {
		t.Fatalf("admissions = %v, want no retry of a full stop", hooks.ups)
	}
}

func TestMalformedPollSnapshotChangesNoMembership(t *testing.T) {
	engine, hooks, _ := engineFixture(t, true)
	if err := engine.ApplyPoll(context.Background(), []map[string]any{{"resource": "urn:case:a"}}); err != nil {
		t.Fatal(err)
	}
	err := engine.ApplyPoll(context.Background(), []map[string]any{{"resource": "urn:case:b"}, {"context": "missing resource"}})
	if err == nil {
		t.Fatal("malformed snapshot succeeded")
	}
	if len(hooks.ups) != 1 || len(hooks.destroys) != 0 {
		t.Fatalf("mutations after invalid snapshot: ups=%v destroys=%v", hooks.ups, hooks.destroys)
	}
	state, _ := engine.state.Population(engine.key)
	if state.Members["a"].Tombstoned || state.Members["b"] != nil {
		t.Fatalf("membership changed: %+v", state.Members)
	}
}

func TestMissingOptionalItemProjectionInvalidatesCompletePollBeforeMutation(t *testing.T) {
	engine, hooks, _ := engineFixture(t, false)
	engine.definition.Population.Session.Inputs["resource"] = &lang.Value{Form: lang.FormFrom, From: "item.context"}
	err := engine.ApplyPoll(context.Background(), []map[string]any{
		{"resource": "urn:case:a", "context": "first"},
		{"resource": "urn:case:b"},
	})
	if err == nil {
		t.Fatal("poll with a missing optional projection succeeded")
	}
	population, stateErr := engine.state.Population(engine.key)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if population != nil || len(hooks.ups) != 0 {
		t.Fatalf("population = %+v, ups = %v, want no mutation", population, hooks.ups)
	}
}

func TestPollAbsenceTombstonesBeforeDryRunAndSuppressesSubscribe(t *testing.T) {
	engine, hooks, _ := engineFixture(t, false)
	var logs bytes.Buffer
	engine.logger = slog.New(slog.NewTextHandler(&logs, nil))
	ctx := context.Background()
	if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": "urn:case:a"}}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ApplyPoll(ctx, nil); err != nil {
		t.Fatal(err)
	}
	state, _ := engine.state.Population(engine.key)
	if !state.Members["a"].Tombstoned || len(hooks.destroys) != 0 {
		t.Fatalf("state=%+v destroys=%v", state.Members["a"], hooks.destroys)
	}
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err != nil {
		t.Fatal(err)
	}
	if len(hooks.ups) != 1 {
		t.Fatalf("tombstoned appearance admitted: %v", hooks.ups)
	}
	if !strings.Contains(logs.String(), "subscribe appearance suppressed by poll tombstone") {
		t.Fatalf("suppression log = %q", logs.String())
	}
	events, _, _, err := engine.log.List("session-urn:case:a", 0, event.Filter{Types: []string{event.TypeWorkflowPopulationDestroyDryRun}})
	if err != nil || len(events) != 1 {
		t.Fatalf("dry-run events = %v, %v", events, err)
	}
}

// TestUsesSwitchLiftsPollTombstoneSuppression covers the reload case where an
// entry's `uses` selection drops poll: a resource poll tombstoned under the
// old selection would otherwise never see another positive poll snapshot to
// reopen it, so the new selection's subscribe authority must be able to
// admit it directly instead of treating the stale poll tombstone as binding.
func TestUsesSwitchLiftsPollTombstoneSuppression(t *testing.T) {
	engine, hooks, _ := engineFixture(t, false)
	ctx := context.Background()
	if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": "urn:case:a"}}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ApplyPoll(ctx, nil); err != nil {
		t.Fatal(err)
	}
	state, _ := engine.state.Population(engine.key)
	if !state.Members["a"].Tombstoned {
		t.Fatalf("resource not tombstoned by poll absence: %+v", state.Members["a"])
	}

	// Simulates the supervisor recreating the evaluator after a config
	// reload that changes this entry's `uses`: same key, same persisted
	// state, only the selection differs.
	engine.definition.Population.Uses = []string{"subscribe"}
	before := len(hooks.ups)
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err != nil {
		t.Fatal(err)
	}
	if len(hooks.ups) != before+1 {
		t.Fatalf("switching away from poll did not admit the previously tombstoned resource: %v", hooks.ups)
	}
	state, _ = engine.state.Population(engine.key)
	if state.Members["a"].Tombstoned {
		t.Fatal("resource still tombstoned after admission under the new selection")
	}
}

func TestPollPositiveReopensTombstoneAndAutoDestroyHonorsBlockers(t *testing.T) {
	engine, hooks, _ := engineFixture(t, true)
	ctx := context.Background()
	if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": "urn:case:a"}}); err != nil {
		t.Fatal(err)
	}
	hooks.blockers = []string{"initial"}
	if err := engine.ApplyPoll(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if len(hooks.destroys) != 0 {
		t.Fatal("blocked member was destroyed")
	}
	hooks.blockers = nil
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(hooks.destroys) != 1 {
		t.Fatalf("destroys = %v", hooks.destroys)
	}
	if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": "urn:case:a"}}); err != nil {
		t.Fatal(err)
	}
	if len(hooks.ups) != 2 {
		t.Fatalf("positive poll did not reopen generation: %v", hooks.ups)
	}
}

func TestUpFailureLeavesAcceptedAppearancePending(t *testing.T) {
	engine, _, accepted := engineFixture(t, false)
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, errors.New("capacity full")
	}
	if err := engine.ApplyAppearance(context.Background(), map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected admission failure")
	}
	state, _ := engine.state.Population(engine.key)
	if !state.Members["a"].PendingUp {
		t.Fatalf("member = %+v, want pending", state.Members["a"])
	}
	if !state.Members["a"].AcceptedAt.IsZero() {
		t.Fatalf("AcceptedAt = %v, want zero while admission is pending", state.Members["a"].AcceptedAt)
	}

	created := accepted.Add(30 * time.Minute)
	engine.now = func() time.Time { return created }
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{SessionName: "session-urn:case:a"}, nil
	}
	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ = engine.state.Population(engine.key)
	if !state.Members["a"].AcceptedAt.Equal(created) {
		t.Fatalf("AcceptedAt = %v, want successful creation time %v", state.Members["a"].AcceptedAt, created)
	}
}

func TestTransientAdmitFailureBacksOffWithoutBlockingOtherMembers(t *testing.T) {
	engine, _, now := engineFixture(t, false)
	engine.now = func() time.Time { return now }
	attempts := map[string]int{}
	engine.hooks.Up = func(_ context.Context, resource string, _ map[string]any) (UpOutcome, error) {
		attempts[resource]++
		if resource == "urn:case:a" {
			return UpOutcome{}, errors.New("temporary up failure")
		}
		return UpOutcome{SessionName: "session-" + resource}, nil
	}

	if err := engine.ApplyPoll(context.Background(), []map[string]any{{"resource": "urn:case:a"}, {"resource": "urn:case:b"}}); err == nil {
		t.Fatal("expected one member admission to fail")
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	failed := population.Members["a"]
	if failed.ConsecutiveAdmitFailures != 1 || !failed.AdmitRetryAt.Equal(now.Add(5*time.Second)) || failed.AdmitSuspended {
		t.Fatalf("failed member = %+v, want first failure with a five-second retry gate", failed)
	}
	if member := population.Members["b"]; member.PendingUp || attempts["urn:case:b"] != 1 {
		t.Fatalf("healthy member = %+v, attempts = %v, want it admitted despite a's failure", member, attempts)
	}

	if err := engine.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile during backoff: %v", err)
	}
	if attempts["urn:case:a"] != 1 {
		t.Fatalf("attempts for failed member = %d, want no retry before backoff", attempts["urn:case:a"])
	}
}

func TestTransientAdmitRecoveryClearsDurableRetryState(t *testing.T) {
	engine, _, now := engineFixture(t, false)
	engine.now = func() time.Time { return now }
	attempts := 0
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		attempts++
		if attempts == 1 {
			return UpOutcome{}, errors.New("temporary up failure")
		}
		return UpOutcome{SessionName: "session-urn:case:a"}, nil
	}
	ctx := context.Background()
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected first admission to fail")
	}
	now = now.Add(5 * time.Second)
	if err := engine.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile after backoff: %v", err)
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members["a"]
	if member.PendingUp || member.ConsecutiveAdmitFailures != 0 || member.LastAdmitReason != "" || member.LastAdmitError != "" || !member.AdmitRetryAt.IsZero() || member.AdmitSuspended {
		t.Fatalf("recovered member = %+v, want all retry state cleared", member)
	}
}

func TestAdmitRetryStateSurvivesEngineRestart(t *testing.T) {
	engine, _, now := engineFixture(t, false)
	engine.now = func() time.Time { return now }
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, errors.New("temporary up failure")
	}
	ctx := context.Background()
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected first admission to fail")
	}

	attempts := 0
	restarted := NewEngine(engine.definition, engine.state, engine.log, engine.cache, Hooks{
		Up: func(context.Context, string, map[string]any) (UpOutcome, error) {
			attempts++
			return UpOutcome{SessionName: "session-urn:case:a"}, nil
		},
	})
	restarted.now = func() time.Time { return now }
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile before persisted retry time: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("attempts after restart before retry time = %d, want 0", attempts)
	}
	now = now.Add(5 * time.Second)
	if err := restarted.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile after persisted retry time: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts after retry time = %d, want 1", attempts)
	}
}

func TestPermanentAdmitFailureSuspendsImmediately(t *testing.T) {
	engine, _, _ := engineFixture(t, false)
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, &service.Error{Code: service.ErrInvalidInput, Message: "configuration is invalid"}
	}
	if err := engine.ApplyAppearance(context.Background(), map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected admission to fail")
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members["a"]
	if !member.AdmitSuspended || member.ConsecutiveAdmitFailures != 1 || !member.AdmitRetryAt.IsZero() {
		t.Fatalf("member = %+v, want an immediately suspended permanent failure", member)
	}
}

func TestFifthUnknownAdmitFailureSuspends(t *testing.T) {
	engine, _, now := engineFixture(t, false)
	engine.now = func() time.Time { return now }
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, errors.New("unknown failure")
	}
	ctx := context.Background()
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected first admission to fail")
	}
	for attempt := 2; attempt <= 5; attempt++ {
		population, err := engine.state.Population(engine.key)
		if err != nil {
			t.Fatal(err)
		}
		now = population.Members["a"].AdmitRetryAt
		if err := engine.Reconcile(ctx); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded", attempt)
		}
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members["a"]
	if !member.AdmitSuspended || member.ConsecutiveAdmitFailures != 5 || !member.AdmitRetryAt.IsZero() {
		t.Fatalf("member = %+v, want suspension after fifth unknown failure", member)
	}
}

func TestPollTombstoneCancelsAdmitRetry(t *testing.T) {
	engine, _, _ := engineFixture(t, false)
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, errors.New("temporary up failure")
	}
	ctx := context.Background()
	if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": "urn:case:a"}}); err == nil {
		t.Fatal("expected admission to fail")
	}
	if err := engine.ApplyPoll(ctx, nil); err != nil {
		t.Fatalf("ApplyPoll tombstone: %v", err)
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members["a"]
	if !member.Tombstoned || member.ConsecutiveAdmitFailures != 0 || member.LastAdmitReason != "" || member.LastAdmitError != "" || !member.AdmitRetryAt.IsZero() || member.AdmitSuspended {
		t.Fatalf("tombstoned member = %+v, want retry state cancelled", member)
	}
}

func TestPopulationRetryAllowsProducedInitialTask(t *testing.T) {
	engine, _, _ := engineFixture(t, false)
	member := &state.PopulationMember{
		Resource:                 sourceResource("urn:case:a"),
		SessionName:              "session-urn:case:a",
		PendingUp:                true,
		ConsecutiveAdmitFailures: 5,
		AdmitSuspended:           true,
		LastAdmitReason:          "task_setup",
	}
	if err := engine.state.UpdatePopulation(engine.key, func(population *state.PopulationState) error {
		population.Members[member.Resource.Name] = member
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.state.Put(&domain.Session{
		Name: "session-urn:case:a",
		Tasks: map[string]*contract.TaskState{
			"initial": {Name: "initial", Scope: "session", TaskID: "work", Resource: sourceRef(member.Resource.Locator), Status: contract.TaskStatusFailed},
		},
	}); err != nil {
		t.Fatal(err)
	}

	initialTaskID := func() (string, error) { return "work", nil }
	if err := RetryPopulationMember(engine.state, engine.log, engine.definition.Workflow.Address, engine.definition.Population.Name, member.Resource.Locator, initialTaskID); err == nil {
		t.Fatal("retry succeeded before initial task cleanup")
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	if !population.Members[member.Resource.Name].AdmitSuspended {
		t.Fatal("failed cleanup check cleared the suspension")
	}
	if err := engine.state.Update(member.SessionName, func(session *domain.Session) error {
		session.Tasks["initial"].Status = contract.TaskStatusProduced
		session.Tasks["initial"].TaskID = "different"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := RetryPopulationMember(engine.state, engine.log, engine.definition.Workflow.Address, engine.definition.Population.Name, member.Resource.Locator, initialTaskID); err == nil {
		t.Fatal("retry succeeded with a conflicting initial task")
	}
	if err := engine.state.Update(member.SessionName, func(session *domain.Session) error {
		session.Tasks["initial"].TaskID = "work"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := RetryPopulationMember(engine.state, engine.log, engine.definition.Workflow.Address, engine.definition.Population.Name, member.Resource.Locator, initialTaskID); err != nil {
		t.Fatalf("retry with produced initial task: %v", err)
	}
	population, err = engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	if got := population.Members[member.Resource.Name]; got.AdmitSuspended || got.ConsecutiveAdmitFailures != 0 || got.LastAdmitReason != "" {
		t.Fatalf("member after explicit retry = %+v, want retry state cleared", got)
	}
	events, _, _, err := engine.log.List(member.SessionName, 0, event.Filter{Types: []string{event.TypeWorkflowPopulationRetry}})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Metadata["reason"] != "operator_retry" {
		t.Fatalf("retry events = %+v, want one operator retry event", events)
	}
}

func TestFailedInitialTaskDoesNotRunSetupAgainBeforeCleanup(t *testing.T) {
	engine, _, _ := engineFixture(t, false)
	engine.definition.Population.Session.Task = "work"
	if err := engine.state.Put(&domain.Session{
		Name: "session-urn:case:a",
		Tasks: map[string]*contract.TaskState{
			"initial": {Name: "initial", Scope: "session", Status: contract.TaskStatusFailed},
		},
	}); err != nil {
		t.Fatal(err)
	}
	setupCalls := 0
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{SessionName: "session-urn:case:a", AlreadyUp: true}, nil
	}
	engine.hooks.EnsureInitial = func(context.Context, string, string, string) error {
		setupCalls++
		return &service.Error{Code: service.ErrInvalidInput, Message: "initial task needs cleanup"}
	}
	ctx := context.Background()
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected initial task failure")
	}
	if err := engine.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile while suspended: %v", err)
	}
	if setupCalls != 1 {
		t.Fatalf("initial setup calls before cleanup = %d, want 1", setupCalls)
	}
	if err := RetryPopulationMember(engine.state, engine.log, engine.definition.Workflow.Address, engine.definition.Population.Name, "urn:case:a", func() (string, error) { return "work", nil }); err == nil {
		t.Fatal("retry succeeded before initial task cleanup")
	}
	if setupCalls != 1 {
		t.Fatalf("initial setup calls after rejected retry = %d, want 1", setupCalls)
	}
}

// The reason tag this asserts is read back by pendingExistingAhead
// (capacity.go), not consumed anywhere in this file.
func TestAdmitFailureReasonsDrivePriorityClassification(t *testing.T) {
	engine, _, _ := engineFixture(t, false)
	ctx := context.Background()

	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.state.Update("session-urn:case:a", func(s *domain.Session) error { s.Status = contract.SessionStatusUp; return nil }); err != nil {
		t.Fatal(err)
	}

	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, &service.Error{Code: service.ErrChildCapExceeded, Message: "cap"}
	}
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected capacity failure")
	}
	events, _, _, err := engine.log.List("session-urn:case:a", 0, event.Filter{Types: []string{event.TypeWorkflowPopulationFailure}})
	if err != nil || len(events) != 1 || events[0].Metadata["reason"] != "capacity" {
		t.Fatalf("failure events = %v, %v, want a single capacity-tagged failure", events, err)
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	if member := population.Members["a"]; member.ConsecutiveAdmitFailures != 0 || member.AdmitRetryAt.IsZero() {
		t.Fatalf("capacity-refused member = %+v, want a retry gate without consuming its failure budget", member)
	}
	engine.now = func() time.Time { return population.Members["a"].AdmitRetryAt }

	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, errors.New("boom")
	}
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected non-capacity failure")
	}
	events, _, _, err = engine.log.List("session-urn:case:a", 0, event.Filter{Types: []string{event.TypeWorkflowPopulationFailure}})
	if err != nil || len(events) != 2 || events[1].Metadata["reason"] != "up" {
		t.Fatalf("failure events = %v, %v, want the second failure tagged \"up\"", events, err)
	}

	// ApplyAppearance validates Session.Inputs before persisting, so the
	// only way admit() itself hits this failure is a definition change
	// after the item was already accepted — a direct Reconcile, not another
	// ApplyAppearance, which would re-validate and never reach admit().
	engine.definition.Population.Session.Inputs["blocked"] = &lang.Value{Form: lang.FormFrom, From: "item.missing_field"}
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		t.Fatal("hooks.Up called despite a session-input resolution failure")
		return UpOutcome{}, nil
	}
	population, err = engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return population.Members["a"].AdmitRetryAt }
	if err := engine.Reconcile(ctx); err == nil {
		t.Fatal("expected session-input resolution failure")
	}
	events, _, _, err = engine.log.List("session-urn:case:a", 0, event.Filter{Types: []string{event.TypeWorkflowPopulationFailure}})
	if err != nil || len(events) != 3 || events[2].Metadata["reason"] != "input" {
		t.Fatalf("failure events = %v, %v, want the third failure tagged \"input\"", events, err)
	}
}

// TestAdmitUpdatesTheSharedCacheInPlace is the round-5 contract this PR's
// review settled on: pendingExistingAhead must read a coordinator-owned
// in-memory record, not rescan a session's event log on every pass. This
// checks that admit() keeps that record correct via Cache.Record — never
// forcing a rescan — for every outcome kind, ending in a successful admit
// that resets it.
func TestAdmitUpdatesTheSharedCacheInPlace(t *testing.T) {
	engine, _, _, cache := engineFixtureWithCache(t, false)
	ctx := context.Background()

	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err != nil {
		t.Fatal(err)
	}
	if err := engine.state.Update("session-urn:case:a", func(s *domain.Session) error { s.Status = contract.SessionStatusUp; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := cache.Get("session-urn:case:a", "urn:case:a").LastReason; got != "" {
		t.Fatalf("cache after a successful admit = %q, want eligible", got)
	}

	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, errors.New("boom")
	}
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected a failure")
	}
	if status := cache.Get("session-urn:case:a", "urn:case:a"); status.LastReason != "up" || status.Consecutive != 1 {
		t.Fatalf("cache after one failure = %+v, want reason \"up\", consecutive 1", status)
	}

	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return population.Members["a"].AdmitRetryAt }
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected a second failure")
	}
	if status := cache.Get("session-urn:case:a", "urn:case:a"); status.Consecutive != 2 {
		t.Fatalf("cache after two failures = %+v, want consecutive 2", status)
	}

	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{SessionName: "session-urn:case:a"}, nil
	}
	population, err = engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return population.Members["a"].AdmitRetryAt }
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err != nil {
		t.Fatal(err)
	}
	if status := cache.Get("session-urn:case:a", "urn:case:a"); status.LastReason != "" || status.Consecutive != 0 {
		t.Fatalf("cache after recovering = %+v, want a full reset", status)
	}
}

func TestAdmissionProvenanceConflictEmitsConflictEvent(t *testing.T) {
	engine, _, _ := engineFixture(t, false)
	engine.hooks.Up = func(context.Context, string, map[string]any) (UpOutcome, error) {
		return UpOutcome{}, &populationConflictError{session: "preexisting", reason: "owned elsewhere"}
	}
	if err := engine.ApplyAppearance(context.Background(), map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected provenance conflict")
	}
	events, _, _, err := engine.log.List("preexisting", 0, event.Filter{Types: []string{event.TypeWorkflowPopulationConflict}})
	if err != nil || len(events) != 1 {
		t.Fatalf("conflict events = %v, %v", events, err)
	}
}

func TestInitialTaskFailureRetainsOwnedSessionForRetry(t *testing.T) {
	engine, _, _ := engineFixture(t, false)
	engine.definition.Population.Session.Task = "work"
	engine.hooks.EnsureInitial = func(context.Context, string, string, string) error {
		return errors.New("task setup failed")
	}
	if err := engine.ApplyAppearance(context.Background(), map[string]any{"resource": "urn:case:a"}); err == nil {
		t.Fatal("expected initial task failure")
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members["a"]
	if member.SessionName != "session-urn:case:a" || !member.PendingUp || member.AcceptedAt.IsZero() {
		t.Fatalf("member = %+v, want persisted owned session pending task retry", member)
	}
}

func TestSubscribeExpiryUsesAppearanceAndInboundClock(t *testing.T) {
	cfg := populationConfig(t, `[source.query.subscribe]
type = "exec"
command = "true"
`, "uses = [\"subscribe\"]\nexpire_after = \"1h\"", `resource = { from = "resource.id" }`)
	defs, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defs[0].Population.AutoDestroy = true
	store := state.NewStore(t.TempDir())
	logStore := eventlog.NewStore(store.Dir())
	recorder := &hookRecorder{store: store, key: populationKey(defs[0])}
	base := time.Now().Add(-2 * time.Hour)
	engine := NewEngine(defs[0], store, logStore, admitstatus.NewCache(logStore), recorder.hooks())
	engine.now = func() time.Time { return base }
	ctx := context.Background()
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": "urn:case:a"}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = engine.log.Append(event.Event{SessionName: "session-urn:case:a", Type: "external.message", Direction: event.Inbound})
	if err != nil {
		t.Fatal(err)
	}
	engine.now = time.Now
	if err := engine.SweepExpiry(ctx); err != nil {
		t.Fatal(err)
	}
	if len(recorder.destroys) != 0 {
		t.Fatal("fresh inbound event did not reset expiry")
	}
	engine.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if err := engine.SweepExpiry(ctx); err != nil {
		t.Fatal(err)
	}
	if len(recorder.destroys) != 1 {
		t.Fatalf("destroys = %v, want one after inbound quiescence", recorder.destroys)
	}
}

func upEventCount(t *testing.T, engine *Engine, session string) int {
	t.Helper()
	events, _, _, err := engine.log.List(session, 0, event.Filter{Types: []string{event.TypeWorkflowPopulationUp}})
	if err != nil {
		t.Fatalf("list up events: %v", err)
	}
	return len(events)
}

func admitOKEventCount(t *testing.T, engine *Engine, session string) int {
	t.Helper()
	events, _, _, err := engine.log.List(session, 0, event.Filter{Types: []string{event.TypeWorkflowPopulationAdmitOK}})
	if err != nil {
		t.Fatalf("list admit_ok events: %v", err)
	}
	return len(events)
}

// An inbound event on a member re-admits it, which re-runs the idempotent Up
// hook; only a hook call that actually took the session from not-up to up is
// a presence change a relay downstream should see.
func TestInboundDoesNotReadmitPopulationMember(t *testing.T) {
	tests := []struct {
		name         string
		sessionWasUp bool
	}{
		{name: "session already up", sessionWasUp: true},
		{name: "session down", sessionWasUp: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, hooks, _ := engineFixture(t, false)
			ctx := context.Background()
			if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": "urn:case:a"}}); err != nil {
				t.Fatal(err)
			}
			if got := upEventCount(t, engine, "session-urn:case:a"); got != 1 {
				t.Fatalf("first admission recorded %d up events, want 1", got)
			}

			hooks.sessionUp = tt.sessionWasUp
			if _, _, _, err := engine.log.Append(event.Event{
				SessionName: "session-urn:case:a",
				Type:        "external.message",
				Direction:   event.Inbound,
			}); err != nil {
				t.Fatal(err)
			}
			if err := engine.SweepExpiry(ctx); err != nil {
				t.Fatal(err)
			}
			if len(hooks.ups) != 1 {
				t.Fatalf("Up hook calls = %v, want no inbound readmission", hooks.ups)
			}
			if got := upEventCount(t, engine, "session-urn:case:a"); got != 1 {
				t.Fatalf("up events = %d, want initial admission only", got)
			}
			if got := admitOKEventCount(t, engine, "session-urn:case:a"); got != 1 {
				t.Fatalf("admit_ok events = %d, want initial admission only", got)
			}
		})
	}
}

func TestAppearanceDuringRepairRetriesAdmissionWithoutFailureBudget(t *testing.T) {
	engine, hooks, now := engineFixture(t, false)
	ctx := context.Background()
	const resource = "urn:case:a"
	const session = "session-urn:case:a"
	if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": resource}}); err != nil {
		t.Fatal(err)
	}

	up := engine.hooks.Up
	repairing := true
	engine.hooks.Up = func(ctx context.Context, resource string, inputs map[string]any) (UpOutcome, error) {
		if repairing {
			return UpOutcome{}, &service.Error{Code: service.ErrChildUpInProgress, Message: "repair holds the lifecycle lock"}
		}
		return up(ctx, resource, inputs)
	}
	if err := engine.ApplyAppearance(ctx, map[string]any{"resource": resource}); err != nil {
		t.Fatal(err)
	}
	currentTime := now
	engine.now = func() time.Time { return currentTime }
	for attempt := 0; attempt <= admitFailureLimit; attempt++ {
		if err := engine.SweepExpiry(ctx); err != nil {
			t.Fatalf("admission while repair holds the lock: %v", err)
		}
		population, err := engine.state.Population(engine.key)
		if err != nil {
			t.Fatal(err)
		}
		member := population.Members[memberName(resource)]
		if !member.PendingUp || member.ConsecutiveAdmitFailures != 0 || member.AdmitSuspended {
			t.Fatalf("member after busy admission = %+v", member)
		}
		if !member.AdmitRetryAt.After(currentTime) {
			t.Fatalf("retry time = %s, want after %s", member.AdmitRetryAt, currentTime)
		}
		currentTime = member.AdmitRetryAt
	}

	repairing = false
	hooks.sessionUp = true
	if err := engine.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members[memberName(resource)]
	if member.PendingUp || member.ConsecutiveAdmitFailures != 0 || member.AdmitSuspended {
		t.Fatalf("member after repair completes = %+v", member)
	}
	if got := admitOKEventCount(t, engine, session); got != 2 {
		t.Fatalf("admit_ok events = %d, want initial and post-repair admissions", got)
	}
}

// Initial-task setup runs after the session is already up, and its failure
// leaves the member pending for a retry whose Up hook then reports no
// transition — so a record deferred until after setup is lost for good.
func TestUpSurvivesAnInitialTaskFailureAndItsRetry(t *testing.T) {
	engine, hooks, _ := engineFixture(t, false)
	engine.definition.Population.Session.Task = "work"
	engine.hooks.EnsureInitial = func(context.Context, string, string, string) error {
		return errors.New("task setup failed")
	}
	ctx := context.Background()
	if err := engine.ApplyPoll(ctx, []map[string]any{{"resource": "urn:case:a"}}); err == nil {
		t.Fatal("expected initial task failure")
	}

	// The retry's Up hook finds the session the failed attempt left running.
	hooks.sessionUp = true
	engine.hooks.EnsureInitial = func(context.Context, string, string, string) error { return nil }
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	engine.now = func() time.Time { return population.Members["a"].AdmitRetryAt }
	if err := engine.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	population, err = engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	if population.Members["a"].PendingUp {
		t.Fatal("retry did not complete the admission")
	}
	if got := upEventCount(t, engine, "session-urn:case:a"); got != 1 {
		t.Fatalf("up events = %d, want exactly one across the failure and its retry", got)
	}
}

func TestTwoSpellingsOfOneResourceInOnePollAreOneMember(t *testing.T) {
	engine, hooks, _ := engineFixture(t, true)
	if err := engine.ApplyPoll(context.Background(), []map[string]any{{"resource": "urn:case:a"}, {"resource": "urn:case:a#reply"}}); err != nil {
		t.Fatal(err)
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	if len(population.Members) != 1 || population.Members["a"] == nil {
		t.Fatalf("members = %+v, want the one resource a", population.Members)
	}
	if len(hooks.ups) != 1 || hooks.ups[0] != "urn:case:a" {
		t.Errorf("ups = %v, want one admission through the first spelling", hooks.ups)
	}
}

func TestAnotherSpellingInALaterPollKeepsTheMember(t *testing.T) {
	engine, hooks, _ := engineFixture(t, true)
	if err := engine.ApplyPoll(context.Background(), []map[string]any{{"resource": "urn:case:a"}}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ApplyPoll(context.Background(), []map[string]any{{"resource": "urn:case:a#reply"}}); err != nil {
		t.Fatal(err)
	}
	population, err := engine.state.Population(engine.key)
	if err != nil {
		t.Fatal(err)
	}
	member := population.Members["a"]
	if len(population.Members) != 1 || member == nil || member.Tombstoned {
		t.Fatalf("members = %+v, want resource a still present", population.Members)
	}
	if member.Resource.Locator != "urn:case:a" {
		t.Errorf("locator = %q, want the first spelling", member.Resource.Locator)
	}
	if len(hooks.destroys) != 0 {
		t.Errorf("destroys = %v, want none for a resource still returned", hooks.destroys)
	}
}

func TestAPollItemWhoseNameAnotherTypeHoldsRejectsTheSnapshot(t *testing.T) {
	engine, hooks, _ := engineFixture(t, true)
	now := time.Now()
	if err := engine.state.Put(&domain.Session{Name: "other", Workflow: "wf", Resource: &contract.Resource{Type: "elsewhere", Name: "a", Locator: "elsewhere://a"}, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	err := engine.ApplyPoll(context.Background(), []map[string]any{{"resource": "urn:case:a"}})
	if err == nil || !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("err = %v, want the type conflict", err)
	}
	if len(hooks.ups) != 0 {
		t.Errorf("ups = %v, want none", hooks.ups)
	}
}
