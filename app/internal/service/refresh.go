package service

import (
	"context"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// OutputRefreshResult is one dynamic output's refresh outcome.
type OutputRefreshResult struct {
	Name    string `json:"name"`
	Value   string `json:"value,omitempty"`
	Fetched bool   `json:"fetched"`
	Error   string `json:"error,omitempty"`
}

// InstanceRefreshResult groups one instance's dynamic-output refresh outcomes.
type InstanceRefreshResult struct {
	Instance string                `json:"instance"`
	Outputs  []OutputRefreshResult `json:"outputs"`
}

// RefreshSessionOutputs refreshes every instance in the session that declares
// dynamic outputs. This is the session-level unit a user or reactor cares about
// ("re-verify this work's DoD"), keyed off the session rather than an opaque
// instance id.
func RefreshSessionOutputs(cfg *config.Config, store *state.Store, sessionName string) ([]InstanceRefreshResult, error) {
	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return nil, err
	}
	defs, err := cfg.LoadTaskDefinitions(session.WorktreePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	var out []InstanceRefreshResult
	for key, st := range session.Tasks {
		if st == nil || st.Status == contract.TaskStatusCleaned {
			continue
		}
		if len(defs[taskIDForInstance(key, st)].DynamicOutputs) == 0 {
			continue
		}
		results, rerr := RefreshInstanceOutputs(cfg, store, resolvedName, key)
		if rerr != nil {
			return out, rerr
		}
		out = append(out, InstanceRefreshResult{Instance: key, Outputs: results})
	}
	return out, nil
}

// RefreshInstanceOutputs runs an instance's dynamic-output scripts and persists
// the fetched values into its outputs. `plect tick`, `plect status --refresh`, and
// `plect task finalize` call this at a decision point; display never does —
// it reads the persisted values, so a sweep can't shell out per session and
// hit the rate limit. A fetch failure leaves the
// prior value untouched and is surfaced in the result.
func RefreshInstanceOutputs(cfg *config.Config, store *state.Store, sessionName, instanceKey string) ([]OutputRefreshResult, error) {
	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return nil, err
	}
	st := session.Tasks[instanceKey]
	if st == nil {
		return nil, &Error{Code: ErrInvalidInput, Message: "instance " + instanceKey + " not found in session " + resolvedName}
	}
	defs, err := cfg.LoadTaskDefinitions(session.WorktreePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	def := defs[taskIDForInstance(instanceKey, st)]
	if len(def.DynamicOutputs) == 0 {
		return nil, nil
	}

	vars := sessionVars(session)
	if st.Resource != "" {
		vars.ResourceID = st.Resource
	}
	ctx := task.RenderContext{Self: st.Outputs, Inputs: st.Inputs, Session: vars}
	if w := session.Tasks[contract.WorkflowPseudoNodeID]; w != nil {
		ctx.Workflow = w.Outputs
	}

	fetched := map[string]any{}
	var results []OutputRefreshResult
	for _, src := range def.DynamicOutputs {
		values, ferr := task.FetchOutput(context.Background(), cfg, src, ctx)
		for _, name := range src.OutputNames() {
			if ferr != nil {
				results = append(results, OutputRefreshResult{Name: name, Error: ferr.Error()})
				continue
			}
			v, ok := values[name]
			if !ok {
				results = append(results, OutputRefreshResult{Name: name})
				continue
			}
			fetched[name] = v
			results = append(results, OutputRefreshResult{Name: name, Value: v, Fetched: true})
		}
	}

	if len(fetched) > 0 {
		now := time.Now()
		if uerr := store.Update(resolvedName, func(s *domain.Session) error {
			cur := s.Tasks[instanceKey]
			if cur == nil || cur.Status == contract.TaskStatusCleaned {
				return nil
			}
			if cur.Outputs == nil {
				cur.Outputs = map[string]any{}
			}
			for k, v := range fetched {
				cur.Outputs[k] = v
			}
			s.UpdatedAt = now
			return nil
		}); uerr != nil {
			return results, &Error{Code: ErrExecutionFailed, Message: uerr.Error()}
		}
	}
	return results, nil
}
