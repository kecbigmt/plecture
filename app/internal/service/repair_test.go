package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func seedRepairSession(t *testing.T, store *state.Store, name string, nodes map[string]*contract.TaskState) {
	t.Helper()
	now := time.Now()
	if err := store.Put(&domain.Session{
		Name: name, ResourceID: "resource:1", Workflow: "default", Nodes: nodes,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRepairAttemptState_StartedEventsCountUntilReset(t *testing.T) {
	tests := []struct {
		name        string
		events      [][2]string
		wantCount   int
		wantSuspend bool
	}{
		{"empty", nil, 0, false},
		{"outcomes do not consume extra attempts", [][2]string{{"started", "1"}, {"succeeded", "1"}, {"started", "2"}, {"failed", "2"}}, 2, false},
		{"suspended", [][2]string{{"started", "1"}, {"failed", "1"}, {"suspended", "1"}}, 1, true},
		{"human up resets", [][2]string{{"started", "1"}, {"failed", "1"}, {"succeeded", "0"}}, 0, false},
		{"healthy observation resets", [][2]string{{"started", "1"}, {"succeeded", "0"}, {"started", "1"}}, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			if err := store.Put(&domain.Session{Name: "s", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			for _, e := range tt.events {
				attempt := 0
				if e[1] == "1" {
					attempt = 1
				} else if e[1] == "2" {
					attempt = 2
				}
				if err := appendRepairEvent(store, "s", "health", attempt, e[0], "reason"); err != nil {
					t.Fatal(err)
				}
			}
			gotCount, gotSuspend, err := repairAttemptState(store, "s")
			if err != nil || gotCount != tt.wantCount || gotSuspend != tt.wantSuspend {
				t.Fatalf("attempt state = (%d, %t, %v), want (%d, %t)", gotCount, gotSuspend, err, tt.wantCount, tt.wantSuspend)
			}
		})
	}
}

func TestRepairCandidate_EligibilityMatrix(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		limit       int
		run         domain.RunState
		report      *HealthReport
		finalized   bool
		wantTrigger string
	}{
		{"healthy", contract.SessionStatusUp, 2, domain.RunUp, &HealthReport{Healthy: true}, false, ""},
		{"alive exit", contract.SessionStatusUp, 2, domain.RunUp, &HealthReport{Repairable: true, Reason: "agent: exit 1"}, false, "health"},
		{"structural failure", contract.SessionStatusUp, 2, domain.RunUp, &HealthReport{Repairable: true, Reason: "agent: missing"}, false, "health"},
		{"probe error", contract.SessionStatusUp, 2, domain.RunUp, &HealthReport{ProbeUnevaluable: true, Reason: "agent: timed out"}, false, ""},
		{"stalled", contract.SessionStatusUp, 2, domain.RunUp, &HealthReport{ActivityDue: true, Healthy: true}, false, ""},
		{"run down", contract.SessionStatusUp, 2, domain.RunDown, nil, false, "run_down"},
		{"sleeping down", contract.SessionStatusDown, 2, domain.RunDown, nil, false, ""},
		{"down intent with produced node", contract.SessionStatusDown, 2, domain.RunUp, &HealthReport{Repairable: true}, false, ""},
		{"not opted in", contract.SessionStatusUp, 0, domain.RunDown, nil, false, ""},
		{"finalized", contract.SessionStatusUp, 2, domain.RunDown, nil, true, ""},
		{"destroyed", contract.SessionStatusDestroyed, 2, domain.RunDown, nil, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &domain.Session{Status: tt.status}
			if tt.finalized {
				s.Tasks = map[string]*contract.TaskState{"initial": {FinalizedAt: time.Now()}}
			}
			trigger, _ := repairCandidate(s, tt.limit, tt.run, tt.report)
			if trigger != tt.wantTrigger {
				t.Fatalf("trigger = %q, want %q", trigger, tt.wantTrigger)
			}
		})
	}
}

func TestWithLifecycleLock_SerializesSameSession(t *testing.T) {
	store := state.NewStore(t.TempDir())
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- withLifecycleLock(store, "s", func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()
	<-firstEntered
	secondStarted := make(chan struct{})
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondDone <- withLifecycleLock(store, "s", func() error {
			close(secondEntered)
			return nil
		})
	}()
	<-secondStarted
	select {
	case <-secondEntered:
		t.Fatal("second lifecycle action entered while first held the lock")
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("second lifecycle action stayed blocked after first released the lock")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func TestDown_WaitsForSessionLifecycleLock(t *testing.T) {
	store := testStore(t)
	cfg := aliveFixtureConfig(t, "true")
	name := "session-1"
	seedRepairSession(t, store, name, map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	if err := store.Update(name, func(s *domain.Session) error {
		s.Status = contract.SessionStatusUp
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	locked := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- withLifecycleLock(store, name, func() error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	downDone := make(chan error, 1)
	go func() {
		_, err := Down(cfg, store, DownParams{Identifier: name})
		downDone <- err
	}()
	select {
	case err := <-downDone:
		t.Fatalf("Down returned while lock held: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if got := store.Get(name).Status; got != contract.SessionStatusUp {
		t.Fatalf("status while lock held = %q, want up", got)
	}
	close(release)
	if err := <-holderDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-downDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Down stayed blocked after lock was released")
	}
	if got := store.Get(name).Status; got != contract.SessionStatusDown {
		t.Fatalf("status after Down = %q, want down", got)
	}
}

func TestRepairAttemptState_SuccessfulHumanUpResetsStreak(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{id: "runner", scope: contract.TaskScopeRun, setup: "echo '{}'", alive: "true"}}, []nodeFixture{{id: "initial", uses: "runner"}})
	name := "session-1"
	seedRepairSession(t, store, name, map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	if err := appendRepairEvent(store, name, "health", 1, "started", "agent failed"); err != nil {
		t.Fatal(err)
	}
	if err := appendRepairEvent(store, name, "health", 1, "suspended", "automatic repair suspended"); err != nil {
		t.Fatal(err)
	}
	if _, err := Up(cfg, store, UpParams{Identifier: name}); err != nil {
		t.Fatal(err)
	}
	count, suspended, err := repairAttemptState(store, name)
	if err != nil || count != 0 || suspended {
		t.Fatalf("attempt state after human Up = (%d, %t, %v), want reset", count, suspended, err)
	}
}

func TestRepairAttemptState_HealthyObservationResetsStreak(t *testing.T) {
	store := testStore(t)
	cfg := aliveFixtureConfig(t, "true")
	path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
	workflow, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(workflow, []byte("\n[default.health]\nauto_repair_attempts = 2\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "session-1"
	seedRepairSession(t, store, name, map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	if err := store.Update(name, func(s *domain.Session) error {
		s.Status = contract.SessionStatusUp
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := appendRepairEvent(store, name, "health", 1, "started", "agent failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := HealthcheckSession(cfg, store, HealthcheckParams{SessionName: name}); err != nil {
		t.Fatal(err)
	}
	count, suspended, err := repairAttemptState(store, name)
	if err != nil || count != 0 || suspended {
		t.Fatalf("attempt state after healthy observation = (%d, %t, %v), want reset", count, suspended, err)
	}
}

func repairWorkflowFixture(t *testing.T, setup, alive string, attempts int) *config.Config {
	t.Helper()
	cfg := writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{id: "runner", scope: contract.TaskScopeRun, setup: setup, alive: alive}}, []nodeFixture{{id: "initial", uses: "runner"}})
	path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
	workflow, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	table := fmt.Sprintf("\n[default.health]\nauto_repair_attempts = %d\n", attempts)
	if err := os.WriteFile(path, append(workflow, []byte(table)...), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRepairSession_RunDownWithUpIntentStartsPlainUp(t *testing.T) {
	store := testStore(t)
	cfg := repairWorkflowFixture(t, "echo '{}'", "true", 2)
	name := "session-1"
	seedRepairSession(t, store, name, nil)
	if err := store.Update(name, func(s *domain.Session) error {
		s.Status = contract.SessionStatusUp
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	attempted, err := RepairSession(cfg, store, name, nil, NewRepairGate())
	if err != nil || !attempted {
		t.Fatalf("RepairSession = (%t, %v), want an attempted repair", attempted, err)
	}
	if got := store.Get(name).Nodes["initial"]; got == nil || got.Status != contract.TaskStatusProduced {
		t.Fatalf("initial after repair = %+v, want produced", got)
	}
	events, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionRepair}})
	if err != nil || len(events) != 2 || events[0].Metadata["result"] != "started" || events[1].Metadata["result"] != "succeeded" ||
		events[0].Metadata["trigger"] != "run_down" || events[0].Metadata["attempt"] != "1" {
		t.Fatalf("repair events = %+v, %v", events, err)
	}
}

func TestRepairSession_SuspendsAfterAttemptLimit(t *testing.T) {
	store := testStore(t)
	cfg := repairWorkflowFixture(t, "exit 1", "false", 2)
	name := "session-1"
	seedRepairSession(t, store, name, map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	if err := store.Update(name, func(s *domain.Session) error {
		s.Status = contract.SessionStatusUp
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	gate := NewRepairGate()
	report := &HealthReport{Repairable: true, Reason: "initial: alive exited 1"}
	for attempt := 1; attempt <= 2; attempt++ {
		attempted, err := RepairSession(cfg, store, name, report, gate)
		if !attempted || err == nil {
			t.Fatalf("attempt %d = (%t, %v), want failed Up attempt", attempt, attempted, err)
		}
		report = nil
	}
	attempted, err := RepairSession(cfg, store, name, nil, gate)
	if err != nil || attempted {
		t.Fatalf("after limit = (%t, %v), want suspended without Up", attempted, err)
	}
	if _, suspended, reason, err := repairAttemptObservation(store, name); err != nil || !suspended || reason == "" {
		t.Fatalf("repair observation after limit = (%t, %q, %v)", suspended, reason, err)
	}
	count, suspended, err := repairAttemptState(store, name)
	if err != nil || count != 2 || !suspended {
		t.Fatalf("attempt state = (%d, %t, %v), want (2, true)", count, suspended, err)
	}
	attempted, err = RepairSession(cfg, store, name, nil, gate)
	if err != nil || attempted {
		t.Fatalf("second suspended cycle = (%t, %v)", attempted, err)
	}
	events, _, _, err := eventlog.NewStore(store.Dir()).List(name, 0, event.Filter{Types: []string{event.TypeSessionRepair}})
	if err != nil || len(events) != 5 || events[4].Metadata["result"] != "suspended" {
		t.Fatalf("repair events = %+v, %v; want two pairs and one suspension", events, err)
	}
}

func TestRepairSession_RepeatedAttemptSkipsHealthyProducedNode(t *testing.T) {
	store := testStore(t)
	cfg := writeWorkflowFixture(t, t.TempDir(), "default",
		[]taskFixture{
			{id: "stable", scope: contract.TaskScopeRun, setup: "echo '{}'", alive: "true"},
			{id: "broken", scope: contract.TaskScopeRun, setup: "exit 1"},
		},
		[]nodeFixture{{id: "stable"}, {id: "broken"}},
	)
	path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
	workflow, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(workflow, []byte("\n[default.health]\nauto_repair_attempts = 3\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "session-1"
	seedRepairSession(t, store, name, map[string]*contract.TaskState{
		"stable": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusProduced, Outputs: map[string]any{}},
		"broken": {Scope: contract.TaskScopeRun, Status: contract.TaskStatusFailed},
	})
	if err := store.Update(name, func(s *domain.Session) error { s.Status = contract.SessionStatusUp; return nil }); err != nil {
		t.Fatal(err)
	}
	gate := NewRepairGate()
	for attempt := 1; attempt <= 2; attempt++ {
		report := &HealthReport{Repairable: true, Reason: "broken: setup failed"}
		attempted, err := RepairSession(cfg, store, name, report, gate)
		if !attempted || err == nil {
			t.Fatalf("attempt %d = (%t, %v), want failed repair", attempt, attempted, err)
		}
	}
	events := nodeResultEvents(t, store, name)
	var stableSkipped, stableSetup int
	for _, ev := range events {
		if ev.Metadata["node"] != "stable" {
			continue
		}
		if ev.Metadata["action"] == event.NodeResultActionAlive && ev.Metadata["result"] == event.NodeResultSkipped {
			stableSkipped++
		}
		if ev.Metadata["action"] == event.NodeResultActionSetup {
			stableSetup++
		}
	}
	if stableSkipped != 2 || stableSetup != 0 {
		t.Fatalf("stable node results = %+v, want two alive skips and no setup", events)
	}
}
