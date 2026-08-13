package service

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// SetOutputParams holds parameters for SetOutput.
type SetOutputParams struct {
	Identifier string
	// Node targets a workflow node's outputs by node id. Mutually exclusive
	// with Workflow / Task; exactly one target must be set.
	Node string
	// Workflow targets the workflow pseudo-node's outputs.
	Workflow bool
	// Task targets a runtime task's outputs by its displayed handle, e.g.
	// "review#1". Dynamic output refresh is the normal DoD path; this remains
	// for explicit trusted updates.
	Task string
	// Outputs is the merge payload: only the keys present are written.
	Outputs map[string]any
}

// SetOutputResult reports what was written.
type SetOutputResult struct {
	SessionName string   `json:"session_name"`
	Target      string   `json:"target"` // Tasks map key that was updated
	Keys        []string `json:"keys"`   // payload keys, sorted
}

// SetOutput merges the payload into a produced task's persisted outputs.
//
// Write policy (safe by default):
//   - merge only — keys absent from the payload are left untouched; there is
//     no full-replace mode
//   - only keys declared `mutable = true` in the target's outputs schema are
//     writable; with no such declaration nothing is writable
//   - the reserved `workdir` key is always immutable (schemas declaring it
//     mutable fail at load, before this check is ever reached)
//   - the merged result must still satisfy the outputs schema
//
// The read-modify-write runs under the state file lock so concurrent explicit
// updates and lifecycle commands cannot lose writes.
func SetOutput(cfg *config.Config, store *state.Store, params SetOutputParams) (*SetOutputResult, error) {
	selected := 0
	if params.Node != "" {
		selected++
	}
	if params.Workflow {
		selected++
	}
	if params.Task != "" {
		selected++
	}
	if selected != 1 {
		return nil, &Error{Code: ErrInvalidInput, Message: "exactly one of --node, --workflow, or --task is required"}
	}
	if len(params.Outputs) == 0 {
		return nil, &Error{Code: ErrInvalidInput, Message: "payload must be a non-empty JSON object"}
	}
	if _, ok := params.Outputs[contract.OutputKeyWorkdir]; ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("output key %q is reserved and always immutable", contract.OutputKeyWorkdir)}
	}

	sessionName, session, err := resolveSession(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}

	target, mutable, schema, resolveErr := resolveSetOutputTarget(cfg, session, params)
	if resolveErr != nil {
		return nil, resolveErr
	}

	keys := make([]string, 0, len(params.Outputs))
	for k := range params.Outputs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if !slices.Contains(mutable, k) {
			if len(mutable) == 0 {
				return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("output key %q is immutable: %s declares no mutable output keys (annotate outputs_schema properties with `mutable = true` to allow external updates)", k, target)}
			}
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("output key %q is immutable; mutable keys for %s: %s", k, target, strings.Join(mutable, ", "))}
		}
	}

	// Re-resolve inside the lock: the snapshot read above only served to
	// derive the schema/mutable-set, which depends on config, not state.
	updateErr := store.Update(sessionName, func(s *domain.Session) error {
		st, ok := s.Tasks[target]
		if !ok || st == nil {
			return &Error{Code: ErrNotProduced, Message: fmt.Sprintf("%s has no recorded state for session %q", target, sessionName)}
		}
		if st.Status != contract.TaskStatusProduced {
			return &Error{Code: ErrNotProduced, Message: fmt.Sprintf("%s is %q, not %q; outputs of non-live tasks are immutable", target, st.Status, contract.TaskStatusProduced)}
		}
		merged := make(map[string]any, len(st.Outputs)+len(params.Outputs))
		maps.Copy(merged, st.Outputs)
		maps.Copy(merged, params.Outputs)
		if schema != nil {
			if vErr := schema.Validate(merged); vErr != nil {
				return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("merged outputs violate schema: %v", vErr)}
			}
		}
		st.Outputs = merged
		s.UpdatedAt = time.Now()
		return nil
	})
	if updateErr != nil {
		if svcErr, ok := updateErr.(*Error); ok {
			return nil, svcErr
		}
		return nil, &Error{Code: ErrExecutionFailed, Message: updateErr.Error()}
	}

	return &SetOutputResult{SessionName: sessionName, Target: target, Keys: keys}, nil
}

// resolveSetOutputTarget maps the selected target to the Tasks map key plus
// the mutable-key set and compiled outputs schema that govern it.
func resolveSetOutputTarget(cfg *config.Config, session *domain.Session, params SetOutputParams) (target string, mutable []string, schema *jsonschema.Schema, err *Error) {
	if params.Workflow {
		workflows, loadErr := cfg.LoadWorkflows(session.WorkdirPath)
		if loadErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", loadErr)}
		}
		wf, ok := workflows[session.Workflow]
		if !ok {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("workflow %q not found in .plect/workflows or global config", session.Workflow)}
		}
		providers, loadErr := cfg.LoadProviders()
		if loadErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load providers: %v", loadErr)}
		}
		prov, ok, provErr := providerFor(wf, providers)
		if provErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: provErr.Error()}
		}
		if !ok {
			return "", nil, nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q declares no provider; the @workflow pseudo-node has no outputs contract", session.Workflow)}
		}
		mutableKeys, mErr := task.MutableOutputKeys(prov.OutputsSchema, prov.ResolvedOutputsSchemaPath())
		if mErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("provider %q: outputs schema: %v", prov.ID, mErr)}
		}
		compiled, cErr := task.CompileSchema(prov.OutputsSchema, prov.ResolvedOutputsSchemaPath(), "plect:provider:"+prov.ID+":outputs")
		if cErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("provider %q: outputs schema: %v", prov.ID, cErr)}
		}
		return contract.WorkflowPseudoNodeID, mutableKeys, compiled, nil
	}

	if params.Task != "" {
		handle := params.Task
		st, ok := session.Tasks[handle]
		if !ok || st == nil {
			return "", nil, nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task handle %q not found in session %q", handle, session.Name)}
		}
		if !st.Dynamic {
			return "", nil, nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("%q is a static workflow node, not a runtime task; use --node", handle)}
		}
		taskID := taskIDForInstance(handle, st)
		defs, loadErr := cfg.LoadTaskDefinitions(session.WorkdirPath)
		if loadErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", loadErr)}
		}
		def, ok := defs[taskID]
		if !ok {
			return "", nil, nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task %q for handle %q not found in any config layer", taskID, handle)}
		}
		mutableKeys, mErr := task.MutableOutputKeys(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
		if mErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("task %q: outputs schema: %v", taskID, mErr)}
		}
		compiled, cErr := task.CompileSchema(def.OutputsSchema, def.ResolvedOutputsSchemaPath(), "plect:task:"+taskID+":outputs")
		if cErr != nil {
			return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("task %q: outputs schema: %v", taskID, cErr)}
		}
		return handle, mutableKeys, compiled, nil
	}

	plan, planErr := buildPlanForSession(cfg, session.WorkdirPath, session)
	if planErr != nil {
		return "", nil, nil, &Error{Code: ErrExecutionFailed, Message: planErr.Error()}
	}
	for _, list := range [][]task.Resolved{plan.Session, plan.Run} {
		for i := range list {
			if list[i].NodeID == params.Node {
				return params.Node, list[i].MutableOutputs, list[i].OutputsSchema, nil
			}
		}
	}
	return "", nil, nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("node %q not found in workflow %q", params.Node, session.Workflow)}
}
