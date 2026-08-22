package service

import (
	"fmt"
	"os"
	"slices"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// CheckParams has no refresh option: check always reads persisted state (see
// CheckSession). Only tick refreshes dynamic outputs before evaluating.
type CheckParams struct {
	SessionName string
}

type CheckAction struct {
	SessionName    string `json:"session_name"`
	Instance       string `json:"instance"`
	Action         string `json:"action"`
	HeartbeatTicks int    `json:"heartbeat_ticks,omitempty"`
	// Layer names the layer of a nesting chain a budget escalation is
	// attributed to, empty for an instance-level one. LayerTicks carries
	// every layer's next tick count, since a chain advances each layer's
	// patience independently.
	Layer               string           `json:"layer,omitempty"`
	LayerIndex          int              `json:"-"`
	LayerTicks          []int            `json:"layer_ticks,omitempty"`
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
// record-time facts needed only by the actuator (plect tick): which instance
// key it belongs to, and the action/fingerprint the previous tick persisted
// for it (what an unchanged re-evaluation is recognized against).
// CheckSession discards these extras and reports only the action; TickSession
// consumes all of them. result is the raw leaf-level evaluation the action was
// derived from — Status's work layer reuses it instead of re-evaluating
// done_when for the same instance.
type computedAction struct {
	instance        string
	action          CheckAction
	lastAction      string
	lastFingerprint string
	result          task.DoneWhenResult
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
	allSessions, err := store.AllE()
	if err != nil {
		return "", nil, nil, nil, err
	}
	defs, err := cfg.LoadTaskDefinitions(session.WorkspaceDirPath)
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
		if chainDrifted(def, st) {
			// The gate degrades to the outermost layer's own conditions,
			// which is a quieter answer than the instance actually owes —
			// so it is said out loud rather than left to be inferred from a
			// suddenly shorter done_when.
			legacyWarnings = append(legacyWarnings, fmt.Sprintf("instance %q was set up with %d nesting layers but task %q now declares %d; its inner layers' conditions are not being evaluated", key, len(st.Layers), taskID, len(def.InnerChain)+1))
		}
		comp, compErr := composeInstance(def, st, sessionVars(cfg, session, nil))
		if compErr != nil {
			return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: compErr.Error()}
		}
		layerDoneWhen, leafOwner := instanceDoneWhen(def, comp)
		live := instanceCompletionState(st, comp)
		dw, err := effectiveDoneWhen(layerDoneWhen, st)
		if err != nil {
			return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
		}
		if dw == nil {
			continue
		}
		// Captured before a tick's persist overwrites them, so the actuator can
		// tell a state it has already acted on from a fresh one: `done` fires
		// exactly once per instance (the goal loop's actuator layer owns `done`
		// emission) and an unchanged unmet state does not re-announce itself on
		// every poll.
		lastAction, lastFingerprint := "", ""
		if st.DoneWhen != nil {
			lastAction, lastFingerprint = st.DoneWhen.LastAction, st.DoneWhen.LastFingerprint
		}
		eval := task.EvaluateTaskDoneWhenWithContext(dw, live, doneWhenEvalContext(resolvedName, st, allSessions))
		budgets := newLayerBudgets(comp, st, leafOwner, len(eval.Leaves))
		action := checkActionForResult(resolvedName, key, sessionResourceForCheck(session, st), dw, st, eval, trigger, budgets)
		if budgets != nil {
			action.LayerTicks = budgets.NextTicks
		}
		if action.Action != "" {
			computed = append(computed, computedAction{instance: key, action: action, lastAction: lastAction, lastFingerprint: lastFingerprint, result: eval})
		}
		if len(chains) == 0 && comp == nil {
			continue
		}
		resource := sessionResourceForCheck(session, st)
		// The upstream output contract: the published output keys a chain's
		// `{{.Work.outputs.X}}` bindings may reference. Empty when the task
		// declares no outputs schema (then wiring is unconstrained).
		upstreamOutputs, schemaErr := task.SchemaPropertyNames(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
		if schemaErr != nil {
			return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("task %q: outputs schema: %v", taskID, schemaErr)}
		}
		if comp == nil {
			facts := buildChainFacts(live, eval)
			for _, ch := range chains {
				if ch.TaskID != "" && ch.TaskID != taskID {
					continue
				}
				sp := evalChain(cfg, store, ch, resolvedName, session, key, resource, facts, upstreamOutputs)
				sp.Task = taskID
				chainPlan = append(chainPlan, sp)
			}
			continue
		}
		// A chain declared by any layer fires against the composed instance,
		// and names the outputs by its own layer's names for them — so each
		// layer sees the composed contract re-keyed into its own namespace,
		// carrying exactly the values that reached it.
		exposure := task.LayerExposure(comp.Layers)
		for i, layer := range comp.Layers {
			if len(layer.Chains) == 0 {
				continue
			}
			facts := buildChainFacts(layerCompletionState(live, exposure, i), eval)
			// The declared contract is what the layer's keys can reach, not
			// what they happen to carry right now: a reachable key with no
			// value yet is a chain waiting on an output, not a chain wired to
			// something that was never published.
			published := make([]string, 0, len(exposure[i]))
			for name := range exposure[i] {
				published = append(published, name)
			}
			slices.Sort(published)
			for _, ch := range layer.Chains {
				sp := evalChain(cfg, store, ch, resolvedName, session, key, resource, facts, published)
				sp.Task = taskID
				chainPlan = append(chainPlan, sp)
			}
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
