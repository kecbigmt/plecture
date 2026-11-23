package service

import (
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

const (
	deliverySession = "review-1"
	deliveryParent  = "review-parent"
)

func recordDeliveryAt(t *testing.T, store *state.Store, name, eventID string, at time.Time) {
	t.Helper()
	rec := domain.UnansweredDelivery{EventID: eventID, EventType: event.TypeUserEmit, DeliveredAt: at}
	if err := store.RecordUnansweredDelivery(name, rec, time.Time{}); err != nil {
		t.Fatalf("record delivery %s: %v", eventID, err)
	}
}

func appendStatusAt(t *testing.T, store *state.Store, name, text string, at time.Time) {
	t.Helper()
	cleared := "false"
	if text == "" {
		cleared = "true"
	}
	if _, _, _, err := eventlog.NewStore(store.Dir()).Append(event.Event{
		SessionName: name, Time: at, Type: event.TypeStatusMessage, Source: event.SourcePlect,
		Direction: event.Outbound, Summary: text, Metadata: map[string]string{"text": text, "cleared": cleared},
	}); err != nil {
		t.Fatalf("append status %q: %v", text, err)
	}
}

func readUnansweredDelivery(t *testing.T, store *state.Store, name string) *domain.UnansweredDelivery {
	t.Helper()
	rec, err := store.UnansweredDelivery(name)
	if err != nil {
		t.Fatalf("read unanswered delivery: %v", err)
	}
	return rec
}

// seedIdleRuntime is an up session whose turn ended: its probe pardons the
// silence, and its latest status is the clear written at turn end.
func seedIdleRuntime(t *testing.T, store *state.Store, done string, turnEnded time.Time) *config.Config {
	t.Helper()
	cfg := activityFixtureConfig(t, activityProbeCmd(t, "waiting:1", true, turnEnded))
	seedSessionWithNodes(t, store, deliverySession, "review", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:    contract.TaskScopeRun,
			TaskID:   "runner",
			Status:   contract.TaskStatusProduced,
			SetupAt:  turnEnded.Add(-time.Hour),
			Observed: observedFacts(map[string]any{"done": done}),
		},
		"gate": gateInstance(done),
	})
	appendStatusAt(t, store, deliverySession, "", turnEnded)
	return cfg
}

func evaluateDeliveryHealth(t *testing.T, cfg *config.Config, store *state.Store) HealthReport {
	t.Helper()
	report, err := EvaluateHealth(cfg, store, deliverySession)
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	return report
}

func TestSetMessage_WorkingStatusAnswersTheDelivery(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	store.Put(&domain.Session{Name: deliverySession, CreatedAt: now, UpdatedAt: now})
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-time.Minute))

	if err := SetMessage(nil, store, deliverySession, "working", nil); err != nil {
		t.Fatalf("SetMessage(working): %v", err)
	}

	if rec := readUnansweredDelivery(t, store, deliverySession); rec != nil {
		t.Fatalf("unanswered delivery = %+v, want none once a turn started", rec)
	}
}

func TestSetMessage_RepeatedWorkingStatusStillAnswersTheDelivery(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	store.Put(&domain.Session{Name: deliverySession, CreatedAt: now, UpdatedAt: now})
	if err := SetMessage(nil, store, deliverySession, "working", nil); err != nil {
		t.Fatalf("SetMessage(working): %v", err)
	}
	recordDeliveryAt(t, store, deliverySession, "emit-1", time.Now())

	if err := SetMessage(nil, store, deliverySession, "working", nil); err != nil {
		t.Fatalf("SetMessage(working again): %v", err)
	}

	if rec := readUnansweredDelivery(t, store, deliverySession); rec != nil {
		t.Fatalf("unanswered delivery = %+v, want none: the runtime reported work after the delivery", rec)
	}
}

func TestSetMessage_ClearedStatusDoesNotAnswerTheDelivery(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	store.Put(&domain.Session{Name: deliverySession, CreatedAt: now, UpdatedAt: now})
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-time.Minute))

	if err := SetMessage(nil, store, deliverySession, "", nil); err != nil {
		t.Fatalf("SetMessage(\"\"): %v", err)
	}

	if rec := readUnansweredDelivery(t, store, deliverySession); rec == nil || rec.EventID != "emit-1" {
		t.Fatalf("unanswered delivery = %+v, want emit-1 kept: a clear is not a turn", rec)
	}
}

func TestHealthcheckSession_DeliveryNoTurnFollowedIsStalledDespiteThePardon(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := seedIdleRuntime(t, store, "no", now.Add(-time.Hour))
	seedSessionWithNodes(t, store, deliveryParent, "review", 2, "", nil)
	setParent(t, store, deliverySession, deliveryParent)
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-20*time.Minute))

	report, err := HealthcheckSession(cfg, store, HealthcheckParams{
		SessionName: deliverySession,
		Config:      config.HealthcheckConfig{Period: config.Duration{Duration: time.Minute}, RenotifyEvery: 3},
	})
	if err != nil {
		t.Fatalf("HealthcheckSession: %v", err)
	}
	if report.State() != domain.HealthStalled || !report.Pushed || report.PushTarget != deliveryParent {
		t.Fatalf("report = %+v, state %q, want stalled pushed to the parent", report, report.State())
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List(deliveryParent, 0, event.Filter{Types: []string{event.TypeTerminalEscalate}})
	if err != nil {
		t.Fatalf("list escalations: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("escalations = %+v, want 1", evs)
	}
	meta := evs[0].Metadata
	if meta["escalation_kind"] != "health.stalled" || meta["event_id"] != "emit-1" || meta["event_type"] != event.TypeUserEmit {
		t.Fatalf("escalation metadata = %+v, want health.stalled naming emit-1 (user.emit)", meta)
	}
}

func TestEvaluateHealth_UnansweredDeliveryAccusesWithNoUnmetWork(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := seedIdleRuntime(t, store, "yes", now.Add(-time.Hour))
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-20*time.Minute))

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthStalled {
		t.Fatalf("state = %q, want stalled: the delivered event is the expectation", report.State())
	}
}

func TestEvaluateHealth_TurnStartedAfterTheDeliveryStaysHealthy(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := seedIdleRuntime(t, store, "no", now.Add(-time.Hour))
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-20*time.Minute))
	if err := SetMessage(nil, store, deliverySession, "working", nil); err != nil {
		t.Fatalf("SetMessage(working): %v", err)
	}
	if err := SetMessage(nil, store, deliverySession, "", nil); err != nil {
		t.Fatalf("SetMessage(\"\"): %v", err)
	}

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthHealthy {
		t.Fatalf("state = %q, want healthy once a turn followed the delivery", report.State())
	}
}

func TestEvaluateHealth_DeliveryWithinTheThresholdStaysHealthy(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := seedIdleRuntime(t, store, "no", now.Add(-time.Hour))
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-time.Minute))

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthHealthy {
		t.Fatalf("state = %q, want healthy while the runtime still has time to start a turn", report.State())
	}
}

func TestEvaluateHealth_NoDeliveryKeepsTheSilencePardoned(t *testing.T) {
	store := testStore(t)
	cfg := seedIdleRuntime(t, store, "no", time.Now().Add(-time.Hour))

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthHealthy {
		t.Fatalf("state = %q, want healthy: waiting after a turn is pardoned", report.State())
	}
}

func TestEvaluateHealth_DeliveryWithoutAnActivityProbeStaysUndeclared(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := activityFixtureConfig(t, "")
	seedSessionWithNodes(t, store, deliverySession, "review", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced, Observed: observedFacts(map[string]any{"done": "no"})},
		"gate":    gateInstance("no"),
	})
	appendStatusAt(t, store, deliverySession, "", now.Add(-time.Hour))
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-20*time.Minute))

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthUndeclared {
		t.Fatalf("state = %q, want undeclared: no activity probe declares status writes", report.State())
	}
}

func TestEvaluateHealth_DeliveryDuringARunningTurnIsNotStalled(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := seedIdleRuntime(t, store, "no", now.Add(-time.Hour))
	appendStatusAt(t, store, deliverySession, "working: a long build", now.Add(-30*time.Minute))
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-20*time.Minute))

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() == domain.HealthStalled {
		t.Fatalf("state = %q, want no stall from the delivery: the turn it arrived in has not ended", report.State())
	}
}

func TestEvaluateHealth_ClockStartsWhenTheTurnThatHeldTheDeliveryEnds(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := seedIdleRuntime(t, store, "no", now.Add(-time.Hour))
	appendStatusAt(t, store, deliverySession, "working: a long build", now.Add(-30*time.Minute))
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-20*time.Minute))
	appendStatusAt(t, store, deliverySession, "", now.Add(-5*time.Minute))

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthHealthy {
		t.Fatalf("state = %q, want healthy: the runtime has been idle for less than the threshold", report.State())
	}
}

func TestEvaluateHealth_DeliveryToAPreviousRunAccusesNoRuntime(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := seedIdleRuntime(t, store, "no", now.Add(-3*time.Hour))
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-2*time.Hour-30*time.Minute))
	if err := store.Update(deliverySession, func(s *domain.Session) error {
		s.Nodes["initial"].SetupAt = now.Add(-time.Hour)
		return nil
	}); err != nil {
		t.Fatalf("restart the run: %v", err)
	}

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthHealthy {
		t.Fatalf("state = %q, want healthy: the event went to a runtime that no longer exists", report.State())
	}
}

func TestEvaluateHealth_RuntimeThatNeverWroteAStatusIsNotAccused(t *testing.T) {
	store := testStore(t)
	now := time.Now()
	cfg := activityFixtureConfig(t, activityProbeCmd(t, "pane:1", false, now))
	seedSessionWithNodes(t, store, deliverySession, "review", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced, Observed: observedFacts(map[string]any{"done": "yes"})},
		"gate":    gateInstance("yes"),
	})
	recordDeliveryAt(t, store, deliverySession, "emit-1", now.Add(-20*time.Minute))

	if report := evaluateDeliveryHealth(t, cfg, store); report.State() != domain.HealthHealthy {
		t.Fatalf("state = %q, want healthy: without a status there is no turn record to judge", report.State())
	}
}
