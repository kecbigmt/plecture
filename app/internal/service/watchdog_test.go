package service

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/contracts/event"
	contract "github.com/kecbigmt/plect/contracts/state"
)

func TestPersistHealthState_UpdateFailureLogsWarning(t *testing.T) {
	store := testStore(t)
	// No session named this in the store, so store.Update fails with "no
	// state entry" — this pins the best-effort swallow at
	// persistHealthState: the healthcheck cycle must not abort, but the
	// failure must not be silent either.
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	persistHealthState(store, "owner/does-not-exist", HealthReport{SessionName: "owner/does-not-exist", Healthy: true}, time.Now())

	if !bytes.Contains(logs.Bytes(), []byte("persist health state failed")) {
		t.Errorf("expected a warning about the failed persist, got log output: %q", logs.String())
	}
}

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

// TestEvaluateHealth_MessageDoesNotAffectHealthOutcome characterizes the
// current gap the health-deepening plan targets: message is a latched
// self-report that health evaluation never reads. A session that reports
// "working" and one that reports "waiting" (or none at all) with the same
// healthcheck outcome must produce the same health report, because
// evaluateHealthFor only consults the declared healthcheck, never Message.
func TestEvaluateHealth_MessageDoesNotAffectHealthOutcome(t *testing.T) {
	messages := []*domain.Message{
		nil,
		{Text: "working"},
		{Text: "waiting"},
		{Text: "anything at all"},
	}

	for _, healthcheck := range []string{"true", "false"} {
		for _, msg := range messages {
			store := testStore(t)
			cfg := healthcheckFixtureConfig(t, healthcheck)
			seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
				"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
			})
			if err := store.Update("owner/repo-1", func(s *domain.Session) error {
				s.Message = msg
				return nil
			}); err != nil {
				t.Fatalf("set message: %v", err)
			}

			report, err := EvaluateHealth(cfg, store, "owner/repo-1")
			if err != nil {
				t.Fatalf("EvaluateHealth: %v", err)
			}
			wantHealthy := healthcheck == "true"
			if report.Healthy != wantHealthy {
				t.Fatalf("healthcheck=%q message=%+v: report.Healthy = %v, want %v (message must not influence health)", healthcheck, msg, report.Healthy, wantHealthy)
			}
		}
	}
}

// movementSignalFixtureConfig declares one run-scoped task "runner" whose
// healthcheck is fixed at "true" and whose movement_signal command is the
// given shell snippet, plus a bare "initial" node so LoadTaskDefinitions has
// a workflow to resolve against. The task's done_when has a single check
// leaf against the "done" output, so seeding it as anything but "yes" leaves
// unmet work.
func movementSignalFixtureConfig(t *testing.T, movementSignal string) *config.Config {
	t.Helper()
	return writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{
		id:             "runner",
		scope:          contract.TaskScopeRun,
		healthcheck:    "true",
		movementSignal: movementSignal,
		extra:          "[[done_when.all]]\ncheck = \"done\"\neq = \"yes\"\n",
	}}, []nodeFixture{{id: "initial", uses: "runner"}})
}

// movementSignalCmd renders a fixed JSON movement-signal fact as the
// command's entire behavior — a fake/stub signal source, standing in for
// whatever concrete provider a later PR wires up.
func movementSignalCmd(t *testing.T, supported, movementExpected bool, fingerprint string, observedAt time.Time) string {
	t.Helper()
	observed := ""
	if !observedAt.IsZero() {
		observed = observedAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf(
		`echo '{"supported":%t,"movement_expected":%t,"fingerprint":%q,"observed_at":%q}'`,
		supported, movementExpected, fingerprint, observed,
	)
}

// TestEvaluateHealth_WedgedButHealthcheckPassingReadsStalled replaces the
// prior known gap this test used to pin: once a movement signal is declared
// and reports stale evidence while unmet done_when work remains, evaluation
// now reads stalled rather than healthy — a present-but-wedged session is
// finally distinguishable from a genuinely healthy one.
func TestEvaluateHealth_WedgedButHealthcheckPassingReadsStalled(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := movementSignalFixtureConfig(t, movementSignalCmd(t, true, true, "fp-1", longAgo))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})
	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		// LastTickAt far in the past and a stale "waiting" message stand in
		// for a wedged-but-present execution surface. Neither is read by
		// health evaluation (see TestEvaluateHealth_MessageDoesNotAffectHealthOutcome)
		// — the stale movement-signal evidence is what actually drives the
		// stalled outcome here.
		s.LastTickAt = longAgo
		s.Message = &domain.Message{Text: "waiting", UpdatedAt: longAgo}
		s.Health = &contract.HealthState{LastFingerprint: "initial:fp-1", LastMovementAt: longAgo}
		return nil
	}); err != nil {
		t.Fatalf("seed wedged state: %v", err)
	}

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthStalled {
		t.Fatalf("report = %+v, state = %q, want stalled (surface present, movement expected, evidence stale)", report, report.State())
	}
}

// TestEvaluateHealth_SignalNarrowsMovementExpectedToHealthy pins the signal's
// own movement_expected contribution: done_when leaves unmet work, but the
// declared signal itself reports movement_expected=false (e.g. "the turn
// already ended") with stale evidence. The signal can only narrow — never
// widen — done_when's expectation, so this instance's contribution drops out
// and the session reads healthy, not stalled.
func TestEvaluateHealth_SignalNarrowsMovementExpectedToHealthy(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := movementSignalFixtureConfig(t, movementSignalCmd(t, true, false, "fp-1", longAgo))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthHealthy {
		t.Fatalf("report = %+v, state = %q, want healthy (signal narrowed movement_expected despite unmet done_when work)", report, report.State())
	}
}

// TestEvaluateHealth_NoMovementSignalDeclaredStaysUndeclaredNotStalled pins
// the explicit no-signal path: unmet done_when work exists (progress is
// expected) but no movement signal is declared to judge it, so evaluation
// has no basis to call the session either healthy or stalled.
func TestEvaluateHealth_NoMovementSignalDeclaredStaysUndeclaredNotStalled(t *testing.T) {
	store := testStore(t)
	cfg := movementSignalFixtureConfig(t, "") // no movement_signal declared at all
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthUndeclared {
		t.Fatalf("report = %+v, state = %q, want undeclared (movement expected, nothing declared to judge it)", report, report.State())
	}
}

// TestEvaluateHealth_ExplicitNoSignalDeclarationStaysUndeclared covers the
// second no-basis path: a movement-signal command is declared but explicitly
// reports "supported": false at evaluation time — the same as if nothing
// were declared, not a stale/fresh judgment either way.
func TestEvaluateHealth_ExplicitNoSignalDeclarationStaysUndeclared(t *testing.T) {
	store := testStore(t)
	cfg := movementSignalFixtureConfig(t, movementSignalCmd(t, false, true, "fp-1", time.Now()))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthUndeclared {
		t.Fatalf("report = %+v, state = %q, want undeclared (explicit no-signal declaration)", report, report.State())
	}
}

// TestEvaluateHealth_FreshMovementEvidenceReadsHealthy is the positive
// counterpart to the stalled test: the same unmet work, but the declared
// movement signal reports evidence timestamped within the stall threshold.
func TestEvaluateHealth_FreshMovementEvidenceReadsHealthy(t *testing.T) {
	store := testStore(t)
	cfg := movementSignalFixtureConfig(t, movementSignalCmd(t, true, true, "fp-1", time.Now()))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthHealthy {
		t.Fatalf("report = %+v, state = %q, want healthy (fresh movement evidence)", report, report.State())
	}
}

// TestEvaluateHealth_NoUnmetWorkStaysHealthyRegardlessOfSignal is the
// orchestrator-idle-between-ticks case: done_when is already satisfied, so
// movement is not currently expected of this session at all. It must read
// healthy even though a movement signal is declared and its evidence is
// stale — the session legitimately has nothing to move on right now, and
// must never be flagged stalled for that.
func TestEvaluateHealth_NoUnmetWorkStaysHealthyRegardlessOfSignal(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := movementSignalFixtureConfig(t, movementSignalCmd(t, true, true, "fp-1", longAgo))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "yes"}, // done_when already satisfied
		},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthHealthy {
		t.Fatalf("report = %+v, state = %q, want healthy (no unmet work, movement not expected)", report, report.State())
	}
}

// TestEvaluateHealth_EscalatedWorkStillExpectsMovement covers the watchdog
// interaction from the rewritten issue: done_when escalation must not make the
// child disappear from stall monitoring.
func TestEvaluateHealth_EscalatedWorkStillExpectsMovement(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := movementSignalFixtureConfig(t, movementSignalCmd(t, true, true, "fp-1", longAgo))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
			DoneWhen: &contract.DoneWhenState{
				LastAction: "escalate",
			},
		},
	})
	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Health = &contract.HealthState{LastFingerprint: "initial:fp-1", LastMovementAt: longAgo}
		return nil
	}); err != nil {
		t.Fatalf("seed movement state: %v", err)
	}

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthStalled {
		t.Fatalf("report = %+v, state = %q, want stalled (escalation must not suppress movement expectation)", report, report.State())
	}
}

// movementSourceFixtureConfig declares one run-scoped task "runner" with a
// fixed-passing healthcheck and unmet done_when, plus a workflow-level
// `[tick.movement_source]` dynamic output whose script is the given shell
// snippet.
func movementSourceFixtureConfig(t *testing.T, script string) *config.Config {
	t.Helper()
	cfg := writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{
		id:          "runner",
		scope:       contract.TaskScopeRun,
		healthcheck: "true",
		extra:       "[[done_when.all]]\ncheck = \"done\"\neq = \"yes\"\n",
	}}, []nodeFixture{{id: "initial", uses: "runner"}})

	path := filepath.Join(cfg.BaseDir, "workflows", "default.toml")
	extra := fmt.Sprintf("\n[tick]\nheartbeat = \"1m\"\n\n[tick.movement_source]\nname = \"fp\"\nscript = %q\n", script)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open workflow fixture: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(extra); err != nil {
		t.Fatalf("append tick.movement_source: %v", err)
	}
	return cfg
}

// movementSourceCmd is a stub movement-source script whose entire stdout is
// the given opaque fingerprint string, standing in for a concrete source
// declared entirely in config — core never interprets what the fingerprint
// means or how it was produced.
func movementSourceCmd(fingerprint string) string {
	return fmt.Sprintf("echo %q", fingerprint)
}

// setMovementState seeds the session's persisted HealthState movement fields,
// standing in for a prior tick's fetch having already advanced (or not) the
// fingerprint core has on record.
func setMovementState(t *testing.T, store *state.Store, name, fingerprint string, observedAt time.Time) {
	t.Helper()
	if err := store.Update(name, func(s *domain.Session) error {
		s.Health = &contract.HealthState{LastFingerprint: "workflow:" + fingerprint, LastMovementAt: observedAt}
		return nil
	}); err != nil {
		t.Fatalf("seed movement state: %v", err)
	}
}

// TestEvaluateHealth_MovementSourceFingerprintUnchangedPastWindowReadsStalled
// pins the core-side comparison the issue asks for: the declared movement
// source keeps reporting the same fingerprint core already has on record,
// and core's own ObservedAt for that fingerprint is far outside the
// freshness window — unmet done_when work makes this stalled, not healthy.
func TestEvaluateHealth_MovementSourceFingerprintUnchangedPastWindowReadsStalled(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := movementSourceFixtureConfig(t, movementSourceCmd("fp-1"))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})
	setMovementState(t, store, "owner/repo-1", "fp-1", longAgo)

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthStalled {
		t.Fatalf("report = %+v, state = %q, want stalled (fingerprint unchanged past freshness window)", report, report.State())
	}
}

// TestEvaluateHealth_MovementSourceFingerprintAdvancedReadsHealthy is the
// positive counterpart: the source now reports a fingerprint different from
// the one core has on record, even though the record is stale — an advance
// always reads healthy and resets core's own ObservedAt clock for it.
func TestEvaluateHealth_MovementSourceFingerprintAdvancedReadsHealthy(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := movementSourceFixtureConfig(t, movementSourceCmd("fp-2"))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})
	setMovementState(t, store, "owner/repo-1", "fp-1", longAgo)

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthHealthy {
		t.Fatalf("report = %+v, state = %q, want healthy (fingerprint advanced)", report, report.State())
	}

	s := store.Get("owner/repo-1")
	if s == nil || s.Health == nil || s.Health.LastFingerprint != "workflow:fp-2" {
		t.Fatalf("persisted health = %+v, want fingerprint %q", s.Health, "workflow:fp-2")
	}
	if s.Health.LastMovementAt.Before(longAgo.Add(23 * time.Hour)) {
		t.Fatalf("persisted LastMovementAt = %v, want reset to roughly now on advance", s.Health.LastMovementAt)
	}
}

// TestEvaluateHealth_MovementSourceFingerprintUnchangedWithinWindowReadsHealthy
// pins the within-window boundary: same fingerprint core already has on
// record, but core's own ObservedAt for it is recent — still healthy.
func TestEvaluateHealth_MovementSourceFingerprintUnchangedWithinWindowReadsHealthy(t *testing.T) {
	store := testStore(t)
	cfg := movementSourceFixtureConfig(t, movementSourceCmd("fp-1"))
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})
	setMovementState(t, store, "owner/repo-1", "fp-1", time.Now())

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthHealthy {
		t.Fatalf("report = %+v, state = %q, want healthy (fingerprint unchanged but within freshness window)", report, report.State())
	}
}

// TestEvaluateHealth_NoMovementSourceDeclaredFallsBackToUndeclared confirms a
// workflow declaring no `[tick.movement_source]` at all behaves exactly like
// no movement signal being declared: undeclared, not stalled, despite unmet
// done_when work — the dynamic-output source is optional, not mandatory.
func TestEvaluateHealth_NoMovementSourceDeclaredFallsBackToUndeclared(t *testing.T) {
	store := testStore(t)
	cfg := movementSignalFixtureConfig(t, "") // no movement_signal, no movement_source
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthUndeclared {
		t.Fatalf("report = %+v, state = %q, want undeclared (no movement source declared)", report, report.State())
	}
}

func TestHealthcheckSession_PushesHealthEscalationAndRenotifies(t *testing.T) {
	store := testStore(t)
	cfg := healthcheckFixtureConfig(t, "false")
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {Scope: contract.TaskScopeRun, TaskID: "runner", Status: contract.TaskStatusProduced},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	healthCfg := config.HealthcheckConfig{
		Period:         config.Duration{Duration: time.Minute},
		StallThreshold: config.Duration{Duration: 3 * time.Minute},
		RenotifyEvery:  2,
	}

	report, err := HealthcheckSession(cfg, store, HealthcheckParams{SessionName: "owner/repo-1", Config: healthCfg})
	if err != nil {
		t.Fatalf("HealthcheckSession: %v", err)
	}
	if report.State() != domain.HealthUnhealthy || !report.Pushed {
		t.Fatalf("report = %+v, want pushed unhealthy report", report)
	}
	assertHealthEscalations := func(want int) []event.Event {
		t.Helper()
		evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalEscalate}})
		if err != nil {
			t.Fatalf("list health escalations: %v", err)
		}
		if len(evs) != want {
			t.Fatalf("health escalation events = %+v, want %d", evs, want)
		}
		return evs
	}
	evs := assertHealthEscalations(1)
	if evs[0].Metadata["escalation_kind"] != "health.unhealthy" || evs[0].Metadata["health_state"] != "unhealthy" || evs[0].Metadata["last_checked_at"] == "" {
		t.Fatalf("health escalation metadata = %+v", evs[0].Metadata)
	}

	report, err = HealthcheckSession(cfg, store, HealthcheckParams{SessionName: "owner/repo-1", Config: healthCfg})
	if err != nil {
		t.Fatalf("second HealthcheckSession: %v", err)
	}
	if report.Pushed {
		t.Fatalf("second report = %+v, want immediate duplicate suppressed", report)
	}
	assertHealthEscalations(1)

	if err := store.Update("owner/repo-1", func(s *domain.Session) error {
		s.Health.LastNotifiedAt = time.Now().Add(-3 * time.Minute)
		return nil
	}); err != nil {
		t.Fatalf("age notification state: %v", err)
	}
	report, err = HealthcheckSession(cfg, store, HealthcheckParams{SessionName: "owner/repo-1", Config: healthCfg})
	if err != nil {
		t.Fatalf("renotify HealthcheckSession: %v", err)
	}
	if !report.Pushed {
		t.Fatalf("renotify report = %+v, want pushed report after renotify window", report)
	}
	assertHealthEscalations(2)
}

func TestHealthcheckSession_PushesStalledEscalationWithMovementTimestamp(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := movementSourceFixtureConfig(t, movementSourceCmd("fp-1"))
	seedSession(t, store, "owner/repo-orchestrator", "owner/repo", 1, "", nil)
	seedSession(t, store, "owner/repo-1", "owner/repo", 1, "default", map[string]*contract.TaskState{
		"initial": {
			Scope:   contract.TaskScopeRun,
			TaskID:  "runner",
			Status:  contract.TaskStatusProduced,
			Outputs: map[string]any{"done": "no"},
		},
	})
	setParent(t, store, "owner/repo-1", "owner/repo-orchestrator")
	setMovementState(t, store, "owner/repo-1", "fp-1", longAgo)

	report, err := HealthcheckSession(cfg, store, HealthcheckParams{
		SessionName: "owner/repo-1",
		Config: config.HealthcheckConfig{
			Period:         config.Duration{Duration: time.Minute},
			StallThreshold: config.Duration{Duration: 5 * time.Minute},
			RenotifyEvery:  3,
		},
	})
	if err != nil {
		t.Fatalf("HealthcheckSession: %v", err)
	}
	if report.State() != domain.HealthStalled || !report.Pushed {
		t.Fatalf("report = %+v, want pushed stalled report", report)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-orchestrator", 0, event.Filter{Types: []string{event.TypeTerminalEscalate}})
	if err != nil {
		t.Fatalf("list health escalations: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("health escalation events = %+v, want 1", evs)
	}
	if evs[0].Metadata["escalation_kind"] != "health.stalled" || evs[0].Metadata["health_state"] != "stalled" || evs[0].Metadata["last_movement_at"] == "" {
		t.Fatalf("stalled health escalation metadata = %+v", evs[0].Metadata)
	}
}

func TestHealthcheckSession_SkipsDeadIntermediateParent(t *testing.T) {
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

	report, err := HealthcheckSession(cfg, store, HealthcheckParams{
		SessionName: "owner/repo-1",
		Config: config.HealthcheckConfig{
			Period:         config.Duration{Duration: time.Minute},
			StallThreshold: config.Duration{Duration: 3 * time.Minute},
			RenotifyEvery:  2,
		},
	})
	if err != nil {
		t.Fatalf("HealthcheckSession: %v", err)
	}
	if !report.Pushed || report.PushTarget != "owner/repo-grandparent" {
		t.Fatalf("report = %+v, want health escalation pushed past the dead parent", report)
	}

	evs, _, _, err := eventlog.NewStore(store.Dir()).List("owner/repo-grandparent", 0, event.Filter{Types: []string{event.TypeTerminalEscalate}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("grandparent escalations = %+v, want one", evs)
	}
	if evs[0].Metadata[event.MetaOriginSession] != "owner/repo-1" {
		t.Fatalf("escalation origin = %q, want owner/repo-1", evs[0].Metadata[event.MetaOriginSession])
	}
	if evs[0].Metadata[event.MetaRelation] != string(domain.RelationDescendant) {
		t.Fatalf("escalation relation = %q, want descendant", evs[0].Metadata[event.MetaRelation])
	}
}
