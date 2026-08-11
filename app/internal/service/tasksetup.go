package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

// TaskSetupParams are the inputs to TaskSetup (the `sennit task setup` path).
// SessionName defaults to the ambient pane env ($SENNIT_SESSION_NAME) so a running
// agent can instantiate a task without naming itself.
type TaskSetupParams struct {
	TaskID      string
	SessionName string
	// Name is the instance identity: when set, the instance key IS the name
	// (session-global unique, no `<task>#` prefix), so a second setup of the
	// same name collides. Empty selects the numbered `<task>#<n>` form, which
	// never collides — a runtime add always gets a fresh number.
	Name string
	// Resource is bound to the instance (exposed to its setup/done_when as
	// .ResourceID) — it is no longer part of the instance key, so the same
	// task can be instantiated repeatedly regardless of resource.
	Resource          string
	Inputs            map[string]string // raw --input k=v bindings
	ExtraDoneWhenJSON string
	Observer          task.Observer
}

// TaskSetupResult reports the instantiated task instance.
type TaskSetupResult struct {
	SessionName string         `json:"session_name"`
	Instance    string         `json:"instance"` // instance key: <name> when named, else <task>#<number>
	TaskID      string         `json:"task_id"`
	Scope       string         `json:"scope"`
	Name        string         `json:"name,omitempty"`
	Resource    string         `json:"resource,omitempty"`
	Outputs     map[string]any `json:"outputs,omitempty"`
}

// TaskSetup instantiates a task definition at runtime against a live
// session. The same definition reachable as a
// workflow DAG node is instantiated here on demand: setup runs, the instance
// (outputs + cleanup + scope) is registered in session state, and teardown
// reclaims it in reverse-instantiation order.
//
// A `--name` instance keys on the name alone and so is a session-global
// singleton: a second setup of an existing name is a collision error (no
// state-based branch — recovery is the caller's explicit cleanup→setup, not an
// auto-recover here). A name-less instance takes the next `<task>#<n>` and
// never collides.
//
// Scope governs when instantiation is allowed and when cleanup runs:
//   - run-scoped tasks may only be instantiated while the session's run scope
//     is up (there is a live run-scoped task); they are cleaned at `down`.
//   - session-scoped tasks may be instantiated any time (even while down) and
//     are cleaned at `destroy`.
func TaskSetup(cfg *config.Config, store *state.Store, params TaskSetupParams) (*TaskSetupResult, error) {
	if strings.TrimSpace(params.TaskID) == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "task id is required"}
	}
	if params.Name != "" && !task.ValidInstanceName(params.Name) {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("invalid --name %q: must be a Go-template identifier with no '#' (so it can't collide with the numbered <task>#<n> namespace)", params.Name)}
	}

	sessionName := params.SessionName
	if sessionName == "" {
		sessionName = os.Getenv("SENNIT_SESSION_NAME")
	}
	if sessionName == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "no session in scope: run inside a sennit session pane (SENNIT_SESSION_NAME) or pass --session"}
	}

	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return nil, err
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState)
	}

	defs, err := cfg.LoadTaskDefinitions(session.WorktreePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	def, ok := defs[params.TaskID]
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task %q not found; add tasks/%s.toml to a trusted config layer", params.TaskID, params.TaskID)}
	}

	// The instance key (NodeID) is allocated under the lock below; ResolveDefinition
	// only needs a node id for the Resolved.NodeID field, which the setup executor
	// does not read, so the task id is a fine placeholder here.
	resolved, err := task.ResolveDefinition(def, params.TaskID)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	wf, wfErr := loadSessionWorkflow(cfg, session.WorktreePath, session)
	if wfErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}
	resolvedExecution, execErr := task.ResolveExecution(def.Execution, wf.Environment)
	if execErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: execErr.Error()}
	}
	resolved.Execution = resolvedExecution
	envExecutor, envErr := environmentExecutorForSession(cfg, wf, session)
	if envErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envErr.Error()}
	}

	if resolved.Scope == config.TaskScopeRun && !hasLiveRunTask(session.Tasks) {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task %q is run-scoped but the session's run scope is not up; run `sennit up %s` first", params.TaskID, resolvedName)}
	}

	inputs, bindErr := bindDynamicInputs(def, params.Inputs, session)
	if bindErr != nil {
		return nil, bindErr
	}
	extraDoneWhen, extraErr := parseExtraDoneWhen(params.ExtraDoneWhenJSON)
	if extraErr != nil {
		return nil, extraErr
	}

	// The bound resource is the instance's own resource, exposed to its setup /
	// done_when as .ResourceID (overriding the session's when present).
	vars := sessionVars(session)
	if params.Resource != "" {
		vars.ResourceID = params.Resource
	}

	// Phase 1 — reserve a per-task instance number + key under the state lock.
	// Allocating the number while holding the lock makes concurrent runs pick
	// distinct sequential keys; the placeholder also lets a step-6 setup learn
	// its own key before running. Seq is stamped here (instantiation order).
	now := time.Now()
	var key string
	var collision bool
	reserveErr := store.Update(resolvedName, func(s *domain.Session) error {
		if s.Tasks == nil {
			s.Tasks = make(map[string]*contract.TaskState)
		}
		if params.Name != "" {
			key = params.Name
			if _, exists := s.Tasks[key]; exists {
				collision = true
				return nil
			}
		} else {
			n := task.NextInstanceNumber(params.TaskID, s.Tasks)
			key = task.InstanceKey(params.TaskID, strconv.Itoa(n))
		}
		s.Tasks[key] = &contract.TaskState{
			Scope:  resolved.Scope,
			TaskID: params.TaskID,
			// Producing-equivalent placeholder; the merge below confirms produced
			// or flips to failed. An interrupted run leaves it produced-with-no-
			// outputs, which reads as not-done (done_when pending) rather than
			// claiming work that never ran.
			Status:        contract.TaskStatusProduced,
			Inputs:        inputs,
			Dynamic:       true,
			Resource:      params.Resource,
			Name:          params.Name,
			ExtraDoneWhen: extraDoneWhen,
			Seq:           task.NextSeq(s.Tasks),
			SetupAt:       now,
		}
		s.UpdatedAt = now
		return nil
	})
	if reserveErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to reserve instance: %v", reserveErr)}
	}
	if collision {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("instance %q already exists in session %s; run `sennit task cleanup %s` first to recreate it", key, resolvedName, key)}
	}

	obs := params.Observer
	if obs != nil {
		obs.OnStart(resolved.Scope, key)
	}
	start := time.Now()

	// Phase 2 — run setup WITHOUT the lock (it may shell out for a while). The
	// @workflow outputs come from the pre-reservation snapshot (stable).
	outputs, stderr, setupErr := task.ExecuteTaskSetup(context.Background(), resolved, inputs, vars, session.Tasks, envExecutor)

	// Phase 3 — merge the result back into the reserved key under the lock,
	// touching only that key (a blind store.Put would clobber concurrent writes
	// to other tasks).
	var resultOutputs map[string]any
	var tornDown bool
	mergeErr := store.Update(resolvedName, func(s *domain.Session) error {
		st, ok := s.Tasks[key]
		if !ok || st == nil || st.Status == contract.TaskStatusCleaned {
			// A concurrent down/destroy cleaned or removed the reservation during
			// the unlocked setup window. Do NOT resurrect it to produced — that
			// would leave a torn-down session with a live-looking instance whose
			// cleanup already ran. Setup may have left orphaned resources; the
			// caller surfaces that as an error.
			tornDown = true
			return nil
		}
		mergedNow := time.Now()
		if setupErr != nil {
			st.Status = contract.TaskStatusFailed
			st.Error = setupErr.Error()
			st.FailedAt = mergedNow
		} else {
			st.Status = contract.TaskStatusProduced
			st.Error = ""
			st.Outputs = outputs
			st.SetupAt = mergedNow
		}
		resultOutputs = st.Outputs
		s.UpdatedAt = mergedNow
		return nil
	})
	if mergeErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", mergeErr)}
	}

	elapsed := time.Since(start)
	if tornDown {
		err := fmt.Errorf("instance %q was torn down during setup (concurrent down/destroy); its setup may have left orphaned resources", key)
		if obs != nil {
			obs.OnFailure(resolved.Scope, key, elapsed, err, stderr)
		}
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if setupErr != nil {
		if obs != nil {
			obs.OnFailure(resolved.Scope, key, elapsed, setupErr, stderr)
		}
		return nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}
	if obs != nil {
		obs.OnSuccess(resolved.Scope, key, elapsed, stderr)
	}

	recordLifecycle(store, resolvedName, "task_setup", fmt.Sprintf("instantiated %s", key))
	// Turn an `instruction` output into the sennit.instruction event its workflow
	// [[event.channel]] delivers.
	appendInstruction(store, resolvedName, key, params.Resource, instructionOutput(resultOutputs))

	return &TaskSetupResult{
		SessionName: resolvedName,
		Instance:    key,
		TaskID:      params.TaskID,
		Scope:       resolved.Scope,
		Name:        params.Name,
		Resource:    params.Resource,
		Outputs:     resultOutputs,
	}, nil
}

func parseExtraDoneWhen(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var dw config.DoneWhen
	if err := json.Unmarshal([]byte(raw), &dw); err != nil {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("invalid --done-when-json: %v", err)}
	}
	if err := dw.Validate(); err != nil {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("invalid --done-when-json: %v", err)}
	}
	normalized, err := json.Marshal(dw)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("normalize --done-when-json: %v", err)}
	}
	return normalized, nil
}

// bindDynamicInputs resolves each input the task declares to a value, using
// the precedence: --input > @workflow (provider) outputs > session inputs.
// When the task declares an inputs schema, only its declared properties are
// bound and an --input key it does not declare is rejected (a typo surfaces
// instead of being silently dropped); required-but-unbound inputs are caught by
// schema validation inside RunTaskInstance. A schema-less task passes the
// explicit --input bindings through verbatim.
func bindDynamicInputs(def config.TaskDefinition, cliInputs map[string]string, session *domain.Session) (map[string]any, *Error) {
	names, err := task.SchemaPropertyNames(def.InputsSchema, def.ResolvedInputsSchemaPath())
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("task %q: inputs schema: %v", def.ID, err)}
	}

	if len(names) == 0 {
		out := make(map[string]any, len(cliInputs))
		for k, v := range cliInputs {
			out[k] = v
		}
		return out, nil
	}

	declared := make(map[string]bool, len(names))
	for _, n := range names {
		declared[n] = true
	}
	for k := range cliInputs {
		if !declared[k] {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task %q does not declare input %q (declared: %s)", def.ID, k, strings.Join(names, ", "))}
		}
	}

	var wfOutputs map[string]any
	if st, ok := session.Tasks[contract.WorkflowPseudoNodeID]; ok && st != nil {
		wfOutputs = st.Outputs
	}

	out := make(map[string]any, len(names))
	for _, name := range names {
		switch {
		case cliInputs[name] != "":
			out[name] = cliInputs[name]
		case wfOutputs[name] != nil:
			out[name] = wfOutputs[name]
		case session.Inputs[name] != nil:
			out[name] = session.Inputs[name]
		}
	}
	return out, nil
}
