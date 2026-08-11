package service

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/eventlog"
	"github.com/kecbigmt/sennit/contracts/event"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

func TestPersistWatchdogState_UpdateFailureLogsWarning(t *testing.T) {
	store := testStore(t)
	// No session named this in the store, so store.Update fails with "no
	// state entry" — this pins the best-effort swallow at
	// persistWatchdogState: the tick must not abort (that path is exercised
	// by the WatchdogTick tests below), but the failure must not be silent
	// either.
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	persistWatchdogState(store, "owner/does-not-exist", HealthReport{SessionName: "owner/does-not-exist", Healthy: true})

	if !bytes.Contains(logs.Bytes(), []byte("persist watchdog state failed")) {
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

// progressSignalFixtureConfig declares one run-scoped task "runner" whose
// healthcheck is fixed at "true" and whose progress_signal command is the
// given shell snippet, plus a bare "initial" node so LoadTaskDefinitions has
// a workflow to resolve against. The task's done_when has a single check
// leaf against the "done" output, so seeding it as anything but "yes" leaves
// unmet work (progress_expected=true).
func progressSignalFixtureConfig(t *testing.T, progressSignal string) *config.Config {
	t.Helper()
	return writeWorkflowFixture(t, t.TempDir(), "default", []taskFixture{{
		id:             "runner",
		scope:          contract.TaskScopeRun,
		healthcheck:    "true",
		progressSignal: progressSignal,
		extra:          "[[done_when.all]]\ncheck = \"done\"\neq = \"yes\"\n",
	}}, []nodeFixture{{id: "initial", uses: "runner"}})
}

// progressSignalCmd renders a fixed JSON progress-signal fact as the
// command's entire behavior — a fake/stub signal source, standing in for
// whatever concrete provider a later PR wires up.
func progressSignalCmd(t *testing.T, supported, progressExpected bool, fingerprint string, observedAt time.Time) string {
	t.Helper()
	observed := ""
	if !observedAt.IsZero() {
		observed = observedAt.UTC().Format(time.RFC3339)
	}
	return fmt.Sprintf(
		`echo '{"supported":%t,"progress_expected":%t,"fingerprint":%q,"observed_at":%q}'`,
		supported, progressExpected, fingerprint, observed,
	)
}

// TestEvaluateHealth_WedgedButHealthcheckPassingReadsStalled replaces the
// prior known gap this test used to pin: once a progress signal is declared
// and reports stale evidence while unmet done_when work remains, evaluation
// now reads stalled rather than healthy — a present-but-wedged session is
// finally distinguishable from a genuinely healthy one.
func TestEvaluateHealth_WedgedButHealthcheckPassingReadsStalled(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := progressSignalFixtureConfig(t, progressSignalCmd(t, true, true, "fp-1", longAgo))
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
		// — the stale progress-signal evidence is what actually drives the
		// stalled outcome here.
		s.LastTickAt = longAgo
		s.Message = &domain.Message{Text: "waiting", UpdatedAt: longAgo}
		return nil
	}); err != nil {
		t.Fatalf("seed wedged state: %v", err)
	}

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthStalled {
		t.Fatalf("report = %+v, state = %q, want stalled (surface present, progress expected, evidence stale)", report, report.State())
	}
}

// TestEvaluateHealth_SignalNarrowsProgressExpectedToHealthy pins the signal's
// own progress_expected contribution: done_when leaves unmet work, but the
// declared signal itself reports progress_expected=false (e.g. "the turn
// already ended") with stale evidence. The signal can only narrow — never
// widen — done_when's expectation, so this instance's contribution drops out
// and the session reads healthy, not stalled.
func TestEvaluateHealth_SignalNarrowsProgressExpectedToHealthy(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := progressSignalFixtureConfig(t, progressSignalCmd(t, true, false, "fp-1", longAgo))
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
		t.Fatalf("report = %+v, state = %q, want healthy (signal narrowed progress_expected despite unmet done_when work)", report, report.State())
	}
}

// TestEvaluateHealth_NoProgressSignalDeclaredStaysUndeclaredNotStalled pins
// the explicit no-signal path: unmet done_when work exists (progress is
// expected) but no progress signal is declared to judge it, so evaluation
// has no basis to call the session either healthy or stalled.
func TestEvaluateHealth_NoProgressSignalDeclaredStaysUndeclaredNotStalled(t *testing.T) {
	store := testStore(t)
	cfg := progressSignalFixtureConfig(t, "") // no progress_signal declared at all
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
		t.Fatalf("report = %+v, state = %q, want undeclared (progress expected, nothing declared to judge it)", report, report.State())
	}
}

// TestEvaluateHealth_ExplicitNoSignalDeclarationStaysUndeclared covers the
// second no-basis path: a progress-signal command is declared but explicitly
// reports "supported": false at evaluation time — the same as if nothing
// were declared, not a stale/fresh judgment either way.
func TestEvaluateHealth_ExplicitNoSignalDeclarationStaysUndeclared(t *testing.T) {
	store := testStore(t)
	cfg := progressSignalFixtureConfig(t, progressSignalCmd(t, false, true, "fp-1", time.Now()))
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

// TestEvaluateHealth_FreshProgressEvidenceReadsHealthy is the positive
// counterpart to the stalled test: the same unmet work, but the declared
// progress signal reports evidence timestamped within the freshness window.
func TestEvaluateHealth_FreshProgressEvidenceReadsHealthy(t *testing.T) {
	store := testStore(t)
	cfg := progressSignalFixtureConfig(t, progressSignalCmd(t, true, true, "fp-1", time.Now()))
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
		t.Fatalf("report = %+v, state = %q, want healthy (fresh progress evidence)", report, report.State())
	}
}

// TestEvaluateHealth_NoUnmetWorkStaysHealthyRegardlessOfSignal is the
// orchestrator-idle-between-ticks case: done_when is already satisfied, so
// progress is not currently expected of this session at all. It must read
// healthy even though a progress signal is declared and its evidence is
// stale — the session legitimately has nothing to progress right now, and
// must never be flagged stalled for that.
func TestEvaluateHealth_NoUnmetWorkStaysHealthyRegardlessOfSignal(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := progressSignalFixtureConfig(t, progressSignalCmd(t, true, true, "fp-1", longAgo))
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
		t.Fatalf("report = %+v, state = %q, want healthy (no unmet work, progress not expected)", report, report.State())
	}
}

// TestEvaluateHealth_EscalatedWorkIsNotExpectedFromThisSession covers the
// other "not expected to act" path: done_when is unsatisfied but this
// session has escalated to an independent reviewer, so it is not this
// session's turn to progress — it must read healthy, not stalled, even with
// stale progress-signal evidence.
func TestEvaluateHealth_EscalatedWorkIsNotExpectedFromThisSession(t *testing.T) {
	store := testStore(t)
	longAgo := time.Now().Add(-24 * time.Hour)
	cfg := progressSignalFixtureConfig(t, progressSignalCmd(t, true, true, "fp-1", longAgo))
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

	report, err := EvaluateHealth(cfg, store, "owner/repo-1")
	if err != nil {
		t.Fatalf("EvaluateHealth: %v", err)
	}
	if report.State() != domain.HealthHealthy {
		t.Fatalf("report = %+v, state = %q, want healthy (escalated: not this session's turn to progress)", report, report.State())
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
