package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	"github.com/kecbigmt/plect/contracts/event"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// HealthReport is the outcome of one healthcheck observation.
type HealthReport struct {
	SessionName string `json:"session_name"`
	Healthy     bool   `json:"healthy"`
	// Declared reports whether any produced run-scoped task instance declares
	// a healthcheck at all — distinguishes "ran and passed" from "nothing to
	// evaluate" (State() surfaces the latter as HealthUndeclared).
	Declared bool `json:"declared"`
	// MovementExpected reports whether unmet run-scoped work exists that
	// this session is expected to act on next, derived from declared
	// done_when/task state, never from Message.
	MovementExpected bool `json:"movement_expected,omitempty"`
	// MovementDeclared reports whether at least one produced run-scoped
	// task instance declared a movement signal that reported itself
	// supported. False when no movement signal is declared at all, or when
	// every declared one explicitly reports unsupported (or fails to run) —
	// both read as "no basis to judge movement."
	MovementDeclared bool `json:"movement_declared,omitempty"`
	// MovementFresh reports whether any supported movement signal reported
	// evidence within the stall threshold. Meaningless unless
	// MovementDeclared and MovementExpected are both true.
	MovementFresh       bool      `json:"movement_fresh,omitempty"`
	Reason              string    `json:"reason,omitempty"`
	LastCheckedAt       time.Time `json:"last_checked_at,omitzero"`
	LastMovementAt      time.Time `json:"last_movement_at,omitzero"`
	MovementFingerprint string    `json:"-"`
	Pushed              bool      `json:"pushed,omitempty"`
	PushTarget          string    `json:"push_target,omitempty"`
	// WakeWarning is set when the dead event was recorded on PushTarget but
	// best-effort waking it (Up) failed — the push itself still succeeded.
	WakeWarning string `json:"wake_warning,omitempty"`
}

type HealthcheckParams struct {
	SessionName string
	Config      config.HealthcheckConfig
}

// State projects the report to the four-value display fact `plect status`
// reports.
//
//   - Undeclared beats everything else when there is no declared healthcheck
//     to evaluate at all.
//   - Unhealthy beats movement: a failing surface check is reported as such
//     regardless of movement evidence.
//   - When no movement is currently expected, a passing surface check is
//     healthy outright.
//   - When movement is expected but no movement signal is declared to judge it,
//     there is no basis to call it either healthy or stalled.
//   - Otherwise, fresh movement evidence is healthy and its absence is stalled.
func (r HealthReport) State() domain.HealthState {
	if !r.Declared && !r.MovementDeclared {
		return domain.HealthUndeclared
	}
	if !r.Healthy {
		return domain.HealthUnhealthy
	}
	if !r.MovementExpected {
		return domain.HealthHealthy
	}
	if !r.MovementDeclared {
		return domain.HealthUndeclared
	}
	if r.MovementFresh {
		return domain.HealthHealthy
	}
	return domain.HealthStalled
}

// EvaluateHealth runs every produced run-scoped task's declared healthcheck
// for name. A session with no produced run-scoped tasks (already `plect down`,
// or a purely session-scoped resource) has nothing to probe and reports
// healthy — L2 only detects a process/runtime/subscription that died while it
// was supposed to be up, not a session that was deliberately brought down.
// The first failing healthcheck wins; Reason names the failing task.
func EvaluateHealth(cfg *config.Config, store *state.Store, name string) (HealthReport, error) {
	s := store.Get(name)
	if s == nil {
		return HealthReport{}, &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q not found", name)}
	}
	defs, err := cfg.LoadTaskDefinitions(s.WorkdirPath)
	if err != nil {
		return HealthReport{}, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	wf := sessionWorkflowConfig(cfg, s.Workflow, s.WorkdirPath)
	healthCfg := config.DefaultHealthcheckConfig()
	var tick *config.TickConfig
	if wf != nil {
		healthCfg = config.NormalizeHealthcheckConfig(wf.Healthcheck)
		tick = wf.Tick
	}
	now := time.Now()
	report := evaluateHealthFor(name, s.Tasks, defs, sessionVars(s), healthCfg.StallThreshold.Duration, s.Health, now)
	applyMovementSource(cfg, s, tick, &report)
	finalizeMovementObservation(&report, s.Health, healthCfg.StallThreshold.Duration, now)
	persistHealthState(store, name, report, now)
	return report, nil
}

func sessionWorkflowConfig(cfg *config.Config, workflowID, workdirPath string) *config.WorkflowFile {
	workflows, err := cfg.LoadWorkflows(workdirPath)
	if err != nil {
		return nil
	}
	wf, ok := workflows[workflowID]
	if !ok {
		return nil
	}
	return &wf
}

// applyMovementSource layers a declared session-scoped movement source on
// top of the per-task movement-signal evidence evaluateHealthFor already
// computed. Fetched via the same task.FetchOutput plumbing a task instance's
// dynamic outputs use, scoped to the session rather than any instance. Core
// only compares the fetched value as an opaque fingerprint against the last
// one it persisted for this session. It never interprets what the source
// script actually observed, and freshness is judged against core's own clock.
func applyMovementSource(cfg *config.Config, s *domain.Session, tick *config.TickConfig, report *HealthReport) {
	if tick == nil || tick.MovementSource == nil {
		return
	}
	src := *tick.MovementSource
	names := src.OutputNames()
	if len(names) == 0 {
		return
	}
	values, err := task.FetchOutput(context.Background(), cfg, src, task.RenderContext{Session: sessionVars(s)})
	if err != nil {
		// A fetch failure leaves this source with no basis to contribute
		// right now — the same as if nothing were declared, not a stale
		// judgment either way.
		return
	}
	fingerprint := values[names[0]]
	if fingerprint == "" {
		return
	}
	report.MovementDeclared = true
	addMovementFingerprint(report, "workflow:"+fingerprint)
}

func addMovementFingerprint(report *HealthReport, fingerprint string) {
	if strings.TrimSpace(fingerprint) == "" {
		return
	}
	report.MovementDeclared = true
	if report.MovementFingerprint == "" {
		report.MovementFingerprint = fingerprint
	} else {
		report.MovementFingerprint += "\x00" + fingerprint
	}
}

func persistHealthState(store *state.Store, name string, report HealthReport, checkedAt time.Time) {
	err := store.Update(name, func(s *domain.Session) error {
		prev := s.Health
		if prev == nil {
			prev = &contract.HealthState{}
		}
		fingerprint := report.MovementFingerprint
		lastMovementAt := prev.LastMovementAt
		if report.MovementDeclared && fingerprint != "" && fingerprint != prev.LastFingerprint {
			lastMovementAt = checkedAt
		}
		stateText := string(report.State())
		s.Health = &contract.HealthState{
			LastCheckedAt:   checkedAt,
			LastMovementAt:  lastMovementAt,
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
	movementExpected := false
	movementDeclared := false
	movementFingerprintParts := []string{}

	for _, key := range sortedTaskKeys(tasks) {
		st := tasks[key]
		if st == nil || st.Scope != contract.TaskScopeRun || st.Status != contract.TaskStatusProduced {
			continue
		}
		def := defs[taskIDForInstance(key, st)]

		if def.Healthcheck != "" {
			declared = true
			if hcErr := task.RunHealthcheck(context.Background(), def.Healthcheck, st.Outputs, st.Inputs, vars); hcErr != nil {
				return HealthReport{SessionName: name, Healthy: false, Declared: true, Reason: fmt.Sprintf("%s: %v", key, hcErr), LastCheckedAt: now}
			}
		}

		// instanceExpected is this instance's own contribution to
		// movement_expected, derived only from done_when/task state and never
		// from the free-text message.
		instanceExpected := false
		if def.DoneWhen != nil {
			if task.EvaluateTaskDoneWhen(def.DoneWhen, st.Outputs).Overall != task.DoneSatisfied {
				instanceExpected = true
			}
		}

		if def.MovementSignal != "" {
			sig, sigErr := task.RunMovementSignal(context.Background(), def.MovementSignal, st.Outputs, st.Inputs, vars)
			if sigErr == nil && sig.Supported {
				movementDeclared = true
				// A supported signal's own MovementExpected can only narrow
				// this instance's contribution (e.g. "the turn already
				// ended"), never manufacture an expectation done_when does
				// not already see.
				if !sig.MovementExpected {
					instanceExpected = false
				}
				if sig.Fingerprint != "" {
					movementFingerprintParts = append(movementFingerprintParts, key+":"+sig.Fingerprint)
				}
			}
			// A failed run or an explicit "supported: false" both mean this
			// instance has no basis to contribute movement evidence right
			// now — the same as if no movement signal were declared.
		}

		if instanceExpected {
			movementExpected = true
		}
	}

	report := HealthReport{
		SessionName:      name,
		Healthy:          true,
		Declared:         declared,
		MovementExpected: movementExpected,
		MovementDeclared: movementDeclared,
		LastCheckedAt:    now,
	}
	if len(movementFingerprintParts) > 0 {
		slices.Sort(movementFingerprintParts)
		report.MovementFingerprint = strings.Join(movementFingerprintParts, "\x00")
	}
	return report
}

func finalizeMovementObservation(report *HealthReport, prev *contract.HealthState, stallThreshold time.Duration, now time.Time) {
	lastMovementAt := time.Time{}
	lastFingerprint := ""
	if prev != nil {
		lastMovementAt = prev.LastMovementAt
		lastFingerprint = prev.LastFingerprint
	}
	if report.MovementDeclared && report.MovementFingerprint != "" && report.MovementFingerprint != lastFingerprint {
		lastMovementAt = now
	}
	report.LastMovementAt = lastMovementAt
	if !report.MovementExpected || !report.MovementDeclared {
		report.MovementFresh = true
		return
	}
	if !lastMovementAt.IsZero() && now.Sub(lastMovementAt) <= stallThreshold {
		report.MovementFresh = true
	}
}

func HealthcheckSession(cfg *config.Config, store *state.Store, params HealthcheckParams) (*HealthReport, error) {
	healthCfg := config.NormalizeHealthcheckConfig(&params.Config)
	before := store.Get(params.SessionName)
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
	if !report.LastMovementAt.IsZero() {
		meta["last_movement_at"] = report.LastMovementAt.UTC().Format(time.RFC3339)
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
		return false
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
		s := store.Get(current)
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
	if !report.LastMovementAt.IsZero() {
		lines = append(lines, "last_movement_at: "+report.LastMovementAt.UTC().Format(time.RFC3339))
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
