package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
	"github.com/kecbigmt/sennit/contracts/event"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

// HealthReport is the outcome of one Layer-2 liveness probe (ADR: cross-session
// terminal event propagation, D4/D8).
type HealthReport struct {
	SessionName string `json:"session_name"`
	Healthy     bool   `json:"healthy"`
	// Declared reports whether any produced run-scoped task instance declares
	// a healthcheck at all — distinguishes "ran and passed" from "nothing to
	// evaluate" (State() surfaces the latter as HealthUndeclared).
	Declared bool `json:"declared"`
	// ProgressExpected reports whether unmet run-scoped work exists that
	// this session is expected to act on next, derived from declared
	// done_when/task state — never from Message. False both when there is
	// no unmet work and when the session has handed the remaining work off
	// (e.g. escalated to an independent reviewer), since in either case the
	// session legitimately has nothing to progress right now.
	ProgressExpected bool `json:"progress_expected,omitempty"`
	// ProgressDeclared reports whether at least one produced run-scoped
	// task instance declared a progress signal that reported itself
	// supported. False when no progress signal is declared at all, or when
	// every declared one explicitly reports unsupported (or fails to run) —
	// both read as "no basis to judge progress."
	ProgressDeclared bool `json:"progress_declared,omitempty"`
	// ProgressFresh reports whether any supported progress signal reported
	// evidence timestamped within the freshness window. Meaningless unless
	// ProgressDeclared and ProgressExpected are both true.
	ProgressFresh bool   `json:"progress_fresh,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Pushed        bool   `json:"pushed,omitempty"`
	PushTarget    string `json:"push_target,omitempty"`
	// WakeWarning is set when the dead event was recorded on PushTarget but
	// best-effort waking it (Up) failed — the push itself still succeeded.
	WakeWarning string `json:"wake_warning,omitempty"`
}

// State projects the report to the four-value display fact `sennit status`
// reports.
//
//   - Undeclared beats everything else when there is no declared healthcheck
//     to evaluate at all — nothing to run.
//   - Unhealthy beats progress: a failing surface check is reported as such
//     regardless of progress evidence.
//   - When no progress is currently expected, a passing surface check is
//     healthy outright — most sessions never declare a progress signal and
//     must not regress to undeclared just because one doesn't exist for work
//     that was never expected of them.
//   - When progress is expected but no progress signal is declared to judge
//     it, there is no basis to call it either healthy or stalled: undeclared.
//   - Otherwise, fresh progress evidence is healthy and its absence is
//     stalled — the surface is present but wedged.
func (r HealthReport) State() domain.HealthState {
	if !r.Declared {
		return domain.HealthUndeclared
	}
	if !r.Healthy {
		return domain.HealthUnhealthy
	}
	if !r.ProgressExpected {
		return domain.HealthHealthy
	}
	if !r.ProgressDeclared {
		return domain.HealthUndeclared
	}
	if r.ProgressFresh {
		return domain.HealthHealthy
	}
	return domain.HealthStalled
}

// EvaluateHealth runs every produced run-scoped task's declared healthcheck
// for name. A session with no produced run-scoped tasks (already `sennit down`,
// or a purely session-scoped resource) has nothing to probe and reports
// healthy — L2 only detects a process/runtime/subscription that died while it
// was supposed to be up, not a session that was deliberately brought down.
// The first failing healthcheck wins; Reason names the failing task.
func EvaluateHealth(cfg *config.Config, store *state.Store, name string) (HealthReport, error) {
	s := store.Get(name)
	if s == nil {
		return HealthReport{}, &Error{Code: ErrWorkspaceNotFound, Message: fmt.Sprintf("session %q not found", name)}
	}
	defs, err := cfg.LoadTaskDefinitions(s.WorktreePath)
	if err != nil {
		return HealthReport{}, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	return evaluateHealthFor(name, s.Tasks, defs, sessionVars(s), progressFreshnessWindow(cfg, s.Workflow, s.WorktreePath)), nil
}

// defaultProgressFreshnessWindow applies when a workflow declares no
// `[tick].heartbeat` — a session with no heartbeat has no natural cadence to
// derive a window from, so this is a fixed fallback rather than an unbounded
// (always-fresh) window.
const defaultProgressFreshnessWindow = 30 * time.Minute

// progressFreshnessWindow derives the progress-evidence freshness window
// from the workflow's declared `[tick].heartbeat` rather than introducing a
// dedicated config key: a session already declares how often it expects to
// be revisited, and evidence older than a couple of those cycles is by
// definition evidence the session's own cadence should have refreshed.
// Doubling the heartbeat tolerates one missed cycle before calling a session
// stalled. A workflow that cannot be loaded (or declares no tick/heartbeat)
// falls back to defaultProgressFreshnessWindow.
func progressFreshnessWindow(cfg *config.Config, workflowID, worktreePath string) time.Duration {
	workflows, err := cfg.LoadWorkflows(worktreePath)
	if err != nil {
		return defaultProgressFreshnessWindow
	}
	wf, ok := workflows[workflowID]
	if !ok || wf.Tick == nil || wf.Tick.Heartbeat.Duration <= 0 {
		return defaultProgressFreshnessWindow
	}
	return 2 * wf.Tick.Heartbeat.Duration
}

// evaluateHealthFor is EvaluateHealth's pure core, taking already-loaded task
// state/defs instead of fetching them from store/cfg. Shared with GC, which
// loads taskDefs once per session for done_when aggregation and reuses it here
// rather than probing the runtime directly. window is the progress-evidence
// freshness window; GC's caller passes zero since it only reads
// Healthy/Declared, never the progress fields.
func evaluateHealthFor(name string, tasks map[string]*contract.TaskState, defs map[string]config.TaskDefinition, vars task.SessionVars, window time.Duration) HealthReport {
	declared := false
	progressExpected := false
	progressDeclared := false
	progressFresh := false
	now := time.Now()

	for _, key := range sortedTaskKeys(tasks) {
		st := tasks[key]
		if st == nil || st.Scope != contract.TaskScopeRun || st.Status != contract.TaskStatusProduced {
			continue
		}
		def := defs[taskIDForInstance(key, st)]

		if def.Healthcheck != "" {
			declared = true
			if hcErr := task.RunHealthcheck(context.Background(), def.Healthcheck, st.Outputs, st.Inputs, vars); hcErr != nil {
				return HealthReport{SessionName: name, Healthy: false, Declared: true, Reason: fmt.Sprintf("%s: %v", key, hcErr)}
			}
		}

		// progress_expected is derived only from done_when/task state
		// (never Message): unmet work exists for this instance and the
		// session has not handed it off (e.g. by escalating to an
		// independent reviewer), in which case it is legitimately not this
		// session's turn to act.
		if def.DoneWhen != nil {
			handedOff := st.DoneWhen != nil && st.DoneWhen.LastAction == "escalate"
			if !handedOff && task.EvaluateTaskDoneWhen(def.DoneWhen, st.Outputs).Overall != task.DoneSatisfied {
				progressExpected = true
			}
		}

		if def.ProgressSignal == "" {
			continue
		}
		sig, sigErr := task.RunProgressSignal(context.Background(), def.ProgressSignal, st.Outputs, st.Inputs, vars)
		if sigErr != nil || !sig.Supported {
			// A failed run or an explicit "supported: false" both mean this
			// instance has no basis to contribute progress evidence right
			// now — the same as if no progress signal were declared.
			continue
		}
		progressDeclared = true
		if !sig.ObservedAt.IsZero() && now.Sub(sig.ObservedAt) <= window {
			progressFresh = true
		}
	}

	return HealthReport{
		SessionName:      name,
		Healthy:          true,
		Declared:         declared,
		ProgressExpected: progressExpected,
		ProgressDeclared: progressDeclared,
		ProgressFresh:    progressFresh,
	}
}

// WatchdogTick probes every session with a produced run-scoped task and
// pushes a `dead` terminal event for each unhealthy one, persisting the
// result on the session's own state (WatchdogState). Push targets the
// immediate parent (D1); if the immediate parent is itself unhealthy, the
// watchdog skips it and delivers to the grandparent instead, applying the
// same rule recursively (D4). When no ancestor in the chain is healthy, the
// `dead` fact is recorded on the origin session's own log instead — core has
// no generic owner-notification primitive; surfacing an undeliverable dead
// fact to a human is a policy decision that lives above sennit (an owner's own
// orchestrator loop noticing the record on its next poll, for instance).
func WatchdogTick(cfg *config.Config, store *state.Store) ([]HealthReport, error) {
	all := store.All()
	names := make([]string, 0, len(all))
	for name, s := range all {
		if s != nil && runScopeUp(s.Tasks) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	reports := make([]HealthReport, 0, len(names))
	for _, name := range names {
		report, err := EvaluateHealth(cfg, store, name)
		if err != nil {
			return reports, err
		}
		persistWatchdogState(store, name, report)
		if !report.Healthy {
			pushDeadReport(cfg, store, name, &report)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func persistWatchdogState(store *state.Store, name string, report HealthReport) {
	now := time.Now()
	err := store.Update(name, func(s *domain.Session) error {
		ws := s.Watchdog
		if ws == nil {
			ws = &contract.WatchdogState{}
			s.Watchdog = ws
		}
		ws.CheckedAt = now
		if report.Healthy {
			ws.DeadAt = time.Time{}
			ws.Reason = ""
		} else {
			ws.DeadAt = now
			ws.Reason = report.Reason
		}
		s.UpdatedAt = now
		return nil
	})
	if err != nil {
		// Best-effort: the watchdog tick's next pass will retry this write, so
		// a transient store failure here must not abort the whole tick (other
		// sessions' health still needs evaluating) — but it must not be
		// invisible either.
		slog.Warn("persist watchdog state failed", "session", name, "error", err)
	}
}

// pushDeadReport pushes `dead` up the ancestor chain, skipping any ancestor
// found unhealthy (D4's single-hop-skip rule applied recursively) until it
// reaches a healthy one or runs out of tree. It mutates report in place with
// the outcome.
func pushDeadReport(cfg *config.Config, store *state.Store, origin string, report *HealthReport) {
	dedupKey := origin + "|dead|" + report.Reason
	// recordUndeliverable: no live ancestor to deliver to, so record locally
	// for observability. wakeIfDown=false — origin just failed its own
	// healthcheck, so waking it would only re-run a setup Up's
	// already-produced skip won't actually heal.
	recordUndeliverable := func() {
		_, _, _ = publishTerminalTo(cfg, store, origin, origin, false, TerminalParams{
			Type:     event.TypeTerminalDead,
			Summary:  fmt.Sprintf("%s is dead: %s", origin, report.Reason),
			Body:     report.Reason,
			Metadata: map[string]string{"undeliverable": "true"},
			DedupKey: dedupKey,
		})
	}
	visited := map[string]bool{origin: true}
	target := origin
	for {
		s := store.Get(target)
		if s == nil || s.ParentSession == "" {
			recordUndeliverable()
			return
		}
		// resolveTerminalTarget: a "root:" pseudo-parent is not itself a
		// session to probe/deliver into — resolve it to the real session it
		// names (the same resolution PublishTerminalToParent applies for
		// done/escalate), or this ancestor walk would treat "root:X" as a
		// literal (missing) session name and fall through as undeliverable.
		parent := resolveTerminalTarget(s.ParentSession)
		if parent == "" || visited[parent] {
			recordUndeliverable()
			return
		}
		visited[parent] = true
		parentHealth, err := EvaluateHealth(cfg, store, parent)
		if err != nil || !parentHealth.Healthy {
			// Parent is dead too (or unprobeable): skip one hop, per D4.
			target = parent
			continue
		}
		id, wakeErr, err := publishTerminalTo(cfg, store, origin, parent, true, TerminalParams{
			Type:     event.TypeTerminalDead,
			Summary:  fmt.Sprintf("%s is dead: %s", origin, report.Reason),
			Body:     report.Reason,
			DedupKey: dedupKey,
		})
		if err == nil && id != "" {
			report.Pushed = true
			report.PushTarget = parent
			if wakeErr != nil {
				report.WakeWarning = wakeErr.Error()
			}
		}
		return
	}
}
