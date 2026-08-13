package service

import (
	"fmt"
	"os"
	"slices"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// CheckParams has no refresh option: check always reads persisted state (see
// CheckSession). Only tick refreshes dynamic outputs before evaluating.
type CheckParams struct {
	SessionName string
}

type CheckAction struct {
	SessionName         string           `json:"session_name"`
	Instance            string           `json:"instance"`
	Action              string           `json:"action"`
	HeartbeatTicks      int              `json:"heartbeat_ticks,omitempty"`
	HeartbeatBudget     int              `json:"heartbeat_budget,omitempty"`
	HeartbeatEscalation int              `json:"heartbeat_escalation,omitempty"`
	Items               []string         `json:"items,omitempty"`
	UnmetItems          []CheckUnmetItem `json:"unmet_items,omitempty"`
	Warnings            []string         `json:"warnings,omitempty"`
	Summary             string           `json:"summary,omitempty"`
	Body                string           `json:"body,omitempty"`
	ReviewerCommand     string           `json:"reviewer_command,omitempty"`
	JudgeCommands       []string         `json:"judge_commands,omitempty"`
	Fingerprint         string           `json:"fingerprint,omitempty"`
	EscalationKind      string           `json:"escalation_kind,omitempty"`
	HeartbeatChanged    bool             `json:"-"`
}

type CheckResult struct {
	Actions []CheckAction `json:"actions,omitempty"`
	// Chains is the [[chains]] evaluation for this same tick/check — fired /
	// already-active / blocked, with the reason — evaluated against the same
	// facts as Actions. CheckSession (and plect status, which shares the same
	// evaluation) always reports it as a dry-run plan (Spawned is always
	// false); TickSession spawns each fired, not-already-active entry.
	Chains []ChainSpawn `json:"chains,omitempty"`
	// Warnings carries config-level notices unrelated to any one instance —
	// currently just a surviving legacy chains/*.toml file, which the retired
	// dual-read no longer reads (config.LegacyChainsDirNotice).
	Warnings []string `json:"warnings,omitempty"`
}

type CheckUnmetItem struct {
	Kind             string          `json:"kind"`
	Expr             string          `json:"expr"`
	Status           task.DoneStatus `json:"status"`
	ID               string          `json:"id,omitempty"`
	Output           string          `json:"output,omitempty"`
	Value            string          `json:"value,omitempty"`
	Observed         bool            `json:"observed,omitempty"`
	Action           string          `json:"action,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	PendingReason    string          `json:"pending_reason,omitempty"`
	Revision         string          `json:"revision,omitempty"`
	CurrentRevision  string          `json:"current_revision,omitempty"`
	ReviewerSession  string          `json:"reviewer_session,omitempty"`
	ReviewerWorkflow string          `json:"reviewer_workflow,omitempty"`
	Relation         string          `json:"relation,omitempty"`
}

// computedAction bundles one instance's done_when evaluation with the
// record-time facts needed only by the actuator (plect tick): whether the last
// persisted action was already "satisfied" (so a repeated `done` push can be
// skipped) and which instance key it belongs to. CheckSession discards these
// extras and reports only the action; TickSession consumes all three. result
// is the raw leaf-level evaluation the action was derived from — Status's work
// layer reuses it instead of re-evaluating done_when for the same instance.
type computedAction struct {
	instance         string
	action           CheckAction
	alreadySatisfied bool
	result           task.DoneWhenResult
}

// evaluateSessionActions runs the read-only half shared by CheckSession (plect
// status / plect_check) and TickSession (plect tick): optionally refresh outputs,
// resolve the session, and evaluate
// done_when — and, against those same facts, [[chains]] — for every produced
// task instance. It never writes state, spawns a session, or publishes events:
// CheckSession returns its result verbatim (a dry-run chain plan), while
// TickSession additionally publishes/persists per action and spawns each
// fired, not-already-active chain. refresh is false for every CheckSession
// call: check reads persisted state only, so that repeated calls cannot
// themselves change what a session reports. Only tick refreshes dynamic
// outputs.
func evaluateSessionActions(cfg *config.Config, store *state.Store, sessionName string, refresh bool, trigger TickTrigger) (string, []computedAction, []ChainSpawn, []string, error) {
	if sessionName == "" {
		sessionName = os.Getenv("PLECT_SESSION_NAME")
	}
	if sessionName == "" {
		return "", nil, nil, nil, &Error{Code: ErrInvalidInput, Message: "no session in scope: pass a session or run inside a plect session pane"}
	}
	if refresh {
		if _, err := RefreshSessionOutputs(cfg, store, sessionName); err != nil {
			return "", nil, nil, nil, err
		}
	}
	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return "", nil, nil, nil, err
	}
	allSessions := store.All()
	defs, err := cfg.LoadTaskDefinitions(session.WorkdirPath)
	if err != nil {
		return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	legacyWarnings, err := cfg.LegacyChainsDirNotice()
	if err != nil {
		return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("legacy chains dir: %v", err)}
	}
	chains := config.TaskChains(defs)

	var computed []computedAction
	var chainPlan []ChainSpawn
	for _, key := range sortedTaskKeys(session.Tasks) {
		st := session.Tasks[key]
		if st == nil || st.Status != contract.TaskStatusProduced || key == contract.WorkflowPseudoNodeID {
			continue
		}
		taskID := taskIDForInstance(key, st)
		def := defs[taskID]
		dw, err := effectiveDoneWhen(def.DoneWhen, st)
		if err != nil {
			return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
		}
		if dw == nil {
			continue
		}
		// Captured before a tick's persist overwrites LastAction, so the
		// `done` push it may issue fires exactly once per instance (the goal
		// loop's actuator layer owns `done` emission) rather than on every
		// poll.
		alreadySatisfied := st.DoneWhen != nil && st.DoneWhen.LastAction == "satisfied"
		eval := task.EvaluateTaskDoneWhenWithContext(dw, st.Outputs, doneWhenEvalContext(resolvedName, st, allSessions))
		action := checkActionForResult(resolvedName, key, sessionResourceForCheck(session, st), dw, st, eval, trigger)
		if action.Action != "" {
			computed = append(computed, computedAction{instance: key, action: action, alreadySatisfied: alreadySatisfied, result: eval})
		}
		if len(chains) == 0 {
			continue
		}
		facts := buildChainFacts(st.Outputs, eval)
		resource := sessionResourceForCheck(session, st)
		// The upstream output contract: the published output keys a chain's
		// `{{.Work.outputs.X}}` bindings may reference. Empty when the task
		// declares no outputs schema (then wiring is unconstrained).
		upstreamOutputs, schemaErr := task.SchemaPropertyNames(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
		if schemaErr != nil {
			return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("task %q: outputs schema: %v", taskID, schemaErr)}
		}
		for _, ch := range chains {
			if ch.TaskID != "" && ch.TaskID != taskID {
				continue
			}
			sp := evalChain(cfg, store, ch, resolvedName, session, key, resource, facts, upstreamOutputs)
			sp.Task = taskID
			chainPlan = append(chainPlan, sp)
		}
	}
	return resolvedName, computed, chainPlan, legacyWarnings, nil
}

// CheckSession reports the same done_when/chain evaluation tick would act on,
// but only reports it: no heartbeat budget advances, no event is published,
// no session is woken or spawned, and no dynamic output is refreshed. It reads
// whatever tick or initial produce last persisted. Calling it any number of
// times leaves state, event log, and session list unchanged. Use plect tick to
// actually advance the gate, refresh outputs, and fire chains.
func CheckSession(cfg *config.Config, store *state.Store, params CheckParams) (*CheckResult, error) {
	_, computed, chainPlan, warnings, err := evaluateSessionActions(cfg, store, params.SessionName, false, "")
	if err != nil {
		return nil, err
	}
	actions := make([]CheckAction, 0, len(computed))
	for _, c := range computed {
		actions = append(actions, c.action)
	}
	return &CheckResult{Actions: actions, Chains: chainPlan, Warnings: warnings}, nil
}

func sessionResourceForCheck(session *domain.Session, st *contract.TaskState) string {
	if st.Resource != "" {
		return st.Resource
	}
	return session.ResourceID
}

func sortedTaskKeys(tasks map[string]*contract.TaskState) []string {
	keys := make([]string, 0, len(tasks))
	for key := range tasks {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
