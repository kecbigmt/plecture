package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	"github.com/kecbigmt/plecture/contracts/event"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// HealthReport is the outcome of one health-cycle observation.
type HealthReport struct {
	SessionName string `json:"session_name"`
	Healthy     bool   `json:"healthy"`
	// Declared reports whether any produced run-scoped task instance declares
	// an alive probe at all — distinguishes "ran and passed" from "nothing to
	// evaluate" (State() surfaces the latter as HealthUndeclared).
	Declared bool `json:"declared"`
	// ActivityDue reports whether unmet run-scoped work exists that this
	// session is expected to act on next, derived from declared
	// done_when/task state, never from Message. This is the accusation side
	// of a stall: an activity probe can pardon silence but never raise it.
	ActivityDue bool `json:"activity_due,omitempty"`
	// ActivityDeclared reports whether at least one produced run-scoped
	// task instance ran an activity probe that produced an envelope. False
	// when no activity probe is declared at all, and when every declared one
	// reports no basis (or fails to produce a usable envelope) — both read
	// as "no basis to judge activity."
	ActivityDeclared bool `json:"activity_declared,omitempty"`
	// ActivityFresh reports whether any contributing activity probe reported
	// evidence within the stall threshold. Meaningless unless
	// ActivityDeclared and ActivityDue are both true.
	ActivityFresh bool `json:"activity_fresh,omitempty"`
	// ProbeErrors names the declared activity probes that failed to produce
	// an envelope this cycle. They contribute no evidence, exactly like a
	// probe reporting no basis, so without this list a persistently broken
	// probe would be indistinguishable from a quiet one.
	ProbeErrors         []ProbeError `json:"probe_errors,omitempty"`
	Reason              string       `json:"reason,omitempty"`
	LastCheckedAt       time.Time    `json:"last_checked_at,omitzero"`
	LastActivityAt      time.Time    `json:"last_activity_at,omitzero"`
	ActivityFingerprint string       `json:"-"`
	Pushed              bool         `json:"pushed,omitempty"`
	PushTarget          string       `json:"push_target,omitempty"`
	// WakeWarning is set when the dead event was recorded on PushTarget but
	// best-effort waking it (Up) failed — the push itself still succeeded.
	WakeWarning string `json:"wake_warning,omitempty"`
}

// ProbeError is one declared activity probe that failed to contribute this
// cycle. ExitCode and Stderr are populated only when the command itself failed
// to run to completion; a probe that ran and printed an unusable envelope
// carries Reason alone.
type ProbeError struct {
	Instance string `json:"instance"`
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Reason   string `json:"reason"`
}

type HealthcheckParams struct {
	SessionName string
	Config      config.HealthcheckConfig
}

// State projects the report to the four-value display fact `plect status`
// reports.
//
//   - Undeclared beats everything else when there is no declared alive probe
//     to evaluate at all and activity has never once been observed for this
//     session.
//   - Unhealthy beats activity: a failing alive probe is reported as such
//     regardless of activity evidence.
//   - When no activity is due, a passing alive probe is healthy outright.
//   - When activity is due but no activity probe contributes this tick and
//     none has ever been observed, there is no basis to call it either
//     healthy or stalled.
//   - Otherwise, fresh activity evidence is healthy and its absence is
//     stalled — once activity has been observed at least once, a probe
//     dying afterward is a stall, not a reversion to undeclared: the
//     basis to judge (LastActivityAt) survives the probe that produced it.
func (r HealthReport) State() domain.HealthState {
	everObserved := !r.LastActivityAt.IsZero()
	if !r.Declared && !r.ActivityDeclared && !everObserved {
		return domain.HealthUndeclared
	}
	if !r.Healthy {
		return domain.HealthUnhealthy
	}
	if !r.ActivityDue {
		return domain.HealthHealthy
	}
	if !r.ActivityDeclared && !everObserved {
		return domain.HealthUndeclared
	}
	if r.ActivityFresh {
		return domain.HealthHealthy
	}
	return domain.HealthStalled
}

// EvaluateHealth runs every produced run-scoped task's declared `[health]`
// probes for name. A session with no produced run-scoped tasks (already
// `plect down`, or a purely session-scoped resource) has nothing to probe and
// reports healthy — L2 only detects a process/runtime/subscription that died
// while it was supposed to be up, not a session that was deliberately brought
// down. The first failing alive probe wins; Reason names the failing task.
func EvaluateHealth(cfg *config.Config, store *state.Store, name string) (HealthReport, error) {
	s, err := store.GetE(name)
	if err != nil {
		return HealthReport{}, err
	}
	if s == nil {
		return HealthReport{}, &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q not found", name)}
	}
	defs, err := cfg.LoadTaskDefinitions(s.WorkspaceDirPath)
	if err != nil {
		return HealthReport{}, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	wf := sessionWorkflowConfig(cfg, s.Workflow, s.WorkspaceDirPath)
	healthCfg := config.DefaultHealthcheckConfig()
	if wf != nil {
		healthCfg = config.NormalizeHealthcheckConfig(wf.Healthcheck)
	}
	now := time.Now()
	report := evaluateHealthFor(name, s.Tasks, defs, sessionVars(cfg, s, nil), healthCfg.StallThreshold.Duration, s.Health, now)
	finalizeActivityObservation(&report, s.Health, healthCfg.StallThreshold.Duration, now)
	persistHealthState(store, name, report, now)
	return report, nil
}

func sessionWorkflowConfig(cfg *config.Config, workflowID, workspaceDirPath string) *config.WorkflowFile {
	workflows, err := cfg.LoadWorkflows(workspaceDirPath)
	if err != nil {
		return nil
	}
	wf, ok := workflows[workflowID]
	if !ok {
		return nil
	}
	return &wf
}

func persistHealthState(store *state.Store, name string, report HealthReport, checkedAt time.Time) {
	err := store.Update(name, func(s *domain.Session) error {
		prev := s.Health
		if prev == nil {
			prev = &contract.HealthState{}
		}
		fingerprint := report.ActivityFingerprint
		lastActivityAt := prev.LastActivityAt
		if report.ActivityDeclared && fingerprint != "" && fingerprint != prev.LastFingerprint {
			lastActivityAt = checkedAt
		}
		stateText := string(report.State())
		s.Health = &contract.HealthState{
			LastCheckedAt:   checkedAt,
			LastActivityAt:  lastActivityAt,
			LastFingerprint: fingerprint,
			LastState:       stateText,
			LastReason:      reportReason(report),
			LastNotifiedAt:  prev.LastNotifiedAt,
			NotifyCount:     prev.NotifyCount,
		}
		s.UpdatedAt = checkedAt
		return nil
	})
	if err != nil {
		slog.Warn("persist health state failed", "session", name, "error", err)
	}
}

func reportReason(report HealthReport) string {
	if report.State() == domain.HealthUnhealthy {
		return report.Reason
	}
	return ""
}

// evaluateHealthFor is EvaluateHealth's pure core, taking already-loaded task
// state/defs instead of fetching them from store/cfg so callers that already
// hold that data avoid a redundant load.
func evaluateHealthFor(name string, tasks map[string]*contract.TaskState, defs map[string]config.TaskDefinition, vars task.SessionVars, stallThreshold time.Duration, prev *contract.HealthState, now time.Time) HealthReport {
	declared := false
	activityDue := false
	activityDeclared := false
	activityFingerprintParts := []string{}
	probeErrors := []ProbeError{}

	for _, key := range sortedTaskKeys(tasks) {
		st := tasks[key]
		if st == nil || st.Scope != contract.TaskScopeRun || st.Status != contract.TaskStatusProduced {
			continue
		}
		def := defs[taskIDForInstance(key, st)]
		comp, compErr := composeInstance(def, st, vars)
		if compErr != nil {
			probeErrors = append(probeErrors, ProbeError{Instance: key, Reason: compErr.Error()})
			continue
		}

		// instanceDue is this instance's own contribution to activity_due,
		// derived only from done_when/task state and never from the
		// free-text message. A chain's layers share it: the composed
		// done_when is one conjunction, so any layer's unmet condition means
		// the instance still owes progress.
		instanceDue := false
		if dw, _ := instanceDoneWhen(def, comp); dw != nil {
			if task.EvaluateTaskDoneWhen(dw, instanceCompletionState(st, comp)).Overall != task.DoneSatisfied {
				instanceDue = true
			}
		}

		// alive composes by AND and activity by OR across the layers of a
		// chain exactly as they compose across instances, so both fall out of
		// walking one flat target list.
		for _, target := range probeTargets(key, def, st, comp) {
			if target.Alive != nil {
				declared = true
				if aliveErr := task.RunAliveProbe(context.Background(), target.probe(target.Alive), vars); aliveErr != nil {
					return HealthReport{SessionName: name, Healthy: false, Declared: true, ProbeErrors: probeErrors, Reason: fmt.Sprintf("%s: %v", target.Label, aliveErr), LastCheckedAt: now}
				}
			}
			if target.Activity == nil {
				continue
			}
			sig, sigErr := task.RunActivityProbe(context.Background(), target.probe(target.Activity), vars)
			switch {
			case sigErr != nil:
				probeErrors = append(probeErrors, activityProbeError(target.Label, target.Activity.Source(), sigErr))
			case sig != nil:
				activityDeclared = true
				// A probe may only lower this instance's expectation: no
				// envelope can manufacture an expectation done_when does not
				// already see.
				if sig.SilenceExpected {
					instanceDue = false
				}
				activityFingerprintParts = append(activityFingerprintParts, target.Label+":"+sig.Fingerprint)
			}
		}

		if instanceDue {
			activityDue = true
		}
	}

	report := HealthReport{
		SessionName:      name,
		Healthy:          true,
		Declared:         declared,
		ActivityDue:      activityDue,
		ActivityDeclared: activityDeclared,
		LastCheckedAt:    now,
	}
	if len(probeErrors) > 0 {
		report.ProbeErrors = probeErrors
	}
	// Every declaring instance's fingerprint contributes: activity composes
	// by OR, so a change anywhere in the session is evidence the session is
	// moving. Sorting keeps the composite stable against map iteration order.
	if len(activityFingerprintParts) > 0 {
		slices.Sort(activityFingerprintParts)
		report.ActivityFingerprint = strings.Join(activityFingerprintParts, "\x00")
	}
	return report
}

func activityProbeError(instance, cmd string, err error) ProbeError {
	entry := ProbeError{Instance: instance, Command: cmd, Reason: err.Error()}
	var execErr *task.ActivityProbeExecError
	if errors.As(err, &execErr) {
		entry.ExitCode = execErr.ExitCode
		entry.Stderr = stderrDigest(execErr.Stderr)
	}
	return entry
}

// probeStderrDigestLimit bounds how much of a runaway probe's stderr the
// report carries. The head is what survives, because a script's first error
// line is the one that names the cause.
const probeStderrDigestLimit = 512

func stderrDigest(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if len(stderr) <= probeStderrDigestLimit {
		return stderr
	}
	return strings.ToValidUTF8(stderr[:probeStderrDigestLimit], "") + "..."
}

func finalizeActivityObservation(report *HealthReport, prev *contract.HealthState, stallThreshold time.Duration, now time.Time) {
	lastActivityAt := time.Time{}
	lastFingerprint := ""
	if prev != nil {
		lastActivityAt = prev.LastActivityAt
		lastFingerprint = prev.LastFingerprint
	}
	if report.ActivityDeclared && report.ActivityFingerprint != "" && report.ActivityFingerprint != lastFingerprint {
		lastActivityAt = now
	}
	report.LastActivityAt = lastActivityAt
	// A probe that never contributed evidence (this tick or ever) has no
	// basis to judge freshness at all, so it defaults true rather than
	// false — but once LastActivityAt is on record, that basis survives the
	// current tick's probe dying, and freshness must be judged against it
	// rather than defaulted.
	if !report.ActivityDue || (!report.ActivityDeclared && lastActivityAt.IsZero()) {
		report.ActivityFresh = true
		return
	}
	if !lastActivityAt.IsZero() && now.Sub(lastActivityAt) <= stallThreshold {
		report.ActivityFresh = true
	}
}

func HealthcheckSession(cfg *config.Config, store *state.Store, params HealthcheckParams) (*HealthReport, error) {
	healthCfg := config.NormalizeHealthcheckConfig(&params.Config)
	before, err := store.GetE(params.SessionName)
	if err != nil {
		return nil, err
	}
	var prev *contract.HealthState
	if before != nil && before.Health != nil {
		cp := *before.Health
		prev = &cp
	}
	report, err := EvaluateHealth(cfg, store, params.SessionName)
	if err != nil {
		return nil, err
	}
	stateText := string(report.State())
	if stateText != string(domain.HealthUnhealthy) && stateText != string(domain.HealthStalled) {
		return &report, nil
	}
	notify, notifyCount := shouldNotifyHealth(prev, stateText, healthCfg)
	if !notify {
		return &report, nil
	}
	report.Pushed = pushHealthEscalation(cfg, store, params.SessionName, &report, notifyCount)
	if report.Pushed {
		persistHealthNotification(store, params.SessionName, notifyCount)
	}
	return &report, nil
}

func shouldNotifyHealth(prev *contract.HealthState, stateText string, healthCfg config.HealthcheckConfig) (bool, int) {
	if prev == nil {
		return true, 1
	}
	if prev.LastState != stateText {
		return true, prev.NotifyCount + 1
	}
	if prev.LastNotifiedAt.IsZero() {
		return true, prev.NotifyCount + 1
	}
	renotifyEvery := healthCfg.RenotifyEvery
	if renotifyEvery <= 0 {
		renotifyEvery = config.DefaultHealthcheckConfig().RenotifyEvery
	}
	period := healthCfg.Period.Duration
	if period <= 0 {
		period = config.DefaultHealthcheckConfig().Period.Duration
	}
	if time.Since(prev.LastNotifiedAt) >= time.Duration(renotifyEvery)*period {
		return true, prev.NotifyCount + 1
	}
	return false, prev.NotifyCount
}

func pushHealthEscalation(cfg *config.Config, store *state.Store, origin string, report *HealthReport, notifyCount int) bool {
	stateText := string(report.State())
	meta := map[string]string{
		"escalation_kind": "health." + stateText,
		"health_state":    stateText,
	}
	if !report.LastCheckedAt.IsZero() {
		meta["last_checked_at"] = report.LastCheckedAt.UTC().Format(time.RFC3339)
	}
	if !report.LastActivityAt.IsZero() {
		meta["last_activity_at"] = report.LastActivityAt.UTC().Format(time.RFC3339)
	}
	id, wakeErr, err := publishHealthEscalationToLiveAncestor(cfg, store, origin, TerminalParams{
		Type:     event.TypeTerminalEscalate,
		Summary:  fmt.Sprintf("%s healthcheck is %s", origin, stateText),
		Body:     healthEscalationBody(origin, report),
		Metadata: meta,
		DedupKey: fmt.Sprintf("%s|health|%s|%d", origin, stateText, notifyCount),
	})
	if err != nil {
		slog.Warn("publish health escalation failed", "session", origin, "error", err)
		return false
	}
	if id == "" {
		localID, _, localErr := publishTerminalTo(cfg, store, origin, origin, false, TerminalParams{
			Type:     event.TypeTerminalDead,
			Summary:  fmt.Sprintf("%s health escalation is undeliverable", origin),
			Body:     healthEscalationBody(origin, report),
			Metadata: meta,
			DedupKey: fmt.Sprintf("%s|health|%s|%d|undeliverable", origin, stateText, notifyCount),
		})
		if localErr != nil {
			slog.Warn("record undeliverable health escalation failed", "session", origin, "error", localErr)
			return false
		}
		if localID == "" {
			return false
		}
		report.PushTarget = origin
		return true
	}
	if wakeErr != nil {
		report.WakeWarning = wakeErr.Error()
	}
	report.PushTarget = id
	return true
}

func publishHealthEscalationToLiveAncestor(cfg *config.Config, store *state.Store, origin string, p TerminalParams) (target string, wakeErr error, err error) {
	visited := map[string]bool{origin: true}
	current := origin
	for {
		s, err := store.GetE(current)
		if err != nil {
			return "", nil, err
		}
		if s == nil {
			return "", nil, &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q not found", current)}
		}
		if s.ParentSession == "" {
			return "", nil, nil
		}
		parent := resolveTerminalTarget(s.ParentSession)
		if parent == "" || visited[parent] {
			return "", nil, nil
		}
		visited[parent] = true
		parentHealth, healthErr := EvaluateHealth(cfg, store, parent)
		if healthErr != nil || parentHealth.State() == domain.HealthUnhealthy || parentHealth.State() == domain.HealthStalled {
			current = parent
			continue
		}
		storedID, wakeErr, publishErr := publishTerminalTo(cfg, store, origin, parent, true, p)
		if publishErr != nil || storedID == "" {
			return "", wakeErr, publishErr
		}
		return parent, wakeErr, nil
	}
}

func healthEscalationBody(origin string, report *HealthReport) string {
	lines := []string{
		fmt.Sprintf("%s healthcheck is %s.", origin, report.State()),
	}
	if report.Reason != "" {
		lines = append(lines, "", report.Reason)
	}
	if !report.LastCheckedAt.IsZero() {
		lines = append(lines, "", "last_checked_at: "+report.LastCheckedAt.UTC().Format(time.RFC3339))
	}
	if !report.LastActivityAt.IsZero() {
		lines = append(lines, "last_activity_at: "+report.LastActivityAt.UTC().Format(time.RFC3339))
	}
	return strings.Join(lines, "\n")
}

func persistHealthNotification(store *state.Store, name string, notifyCount int) {
	now := time.Now()
	if err := store.Update(name, func(s *domain.Session) error {
		if s.Health == nil {
			s.Health = &contract.HealthState{}
		}
		s.Health.LastNotifiedAt = now
		s.Health.NotifyCount = notifyCount
		s.UpdatedAt = now
		return nil
	}); err != nil {
		slog.Warn("persist health notification failed", "session", name, "error", err)
	}
}
