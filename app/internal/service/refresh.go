package service

import (
	"context"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
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
	defs, err := cfg.LoadTaskDefinitions(session.WorkspaceDirPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	var out []InstanceRefreshResult
	for key, st := range session.Tasks {
		if st == nil || st.Status == contract.TaskStatusCleaned {
			continue
		}
		if !declaresDynamicOutputs(defs[taskIDForInstance(key, st)]) {
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
// `plect task finalize` call this at a decision point; `plect status` (without
// --refresh) and `plect ls` never do — they read the persisted values, so
// listing many sessions at once doesn't shell out per session and hit the
// rate limit. A fetch failure leaves the prior value untouched and is
// surfaced in the result.
func RefreshInstanceOutputs(cfg *config.Config, store *state.Store, sessionName, instanceKey string) ([]OutputRefreshResult, error) {
	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return nil, err
	}
	st := session.Tasks[instanceKey]
	if st == nil {
		return nil, &Error{Code: ErrInvalidInput, Message: "instance " + instanceKey + " not found in session " + resolvedName}
	}
	defs, err := cfg.LoadTaskDefinitions(session.WorkspaceDirPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	def := defs[taskIDForInstance(instanceKey, st)]
	if !declaresDynamicOutputs(def) {
		return nil, nil
	}

	// No full compiled Plan in scope here (a dynamic output refresh works off
	// the instance's own state entry) — {{terminal "..."}} is unavailable,
	// same as taskcleanup.go's single-instance teardown.
	vars := sessionVars(cfg, session, nil)
	if st.Resource != "" {
		vars.ResourceID = st.Resource
	}
	comp, compErr := composeInstance(def, st, vars)
	if compErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: compErr.Error()}
	}
	workflowOutputs := map[string]any(nil)
	if w := session.Tasks[contract.WorkflowPseudoNodeID]; w != nil {
		workflowOutputs = w.Outputs
	}

	// fetched is per layer: a layer's `[[outputs]]` produce keys in that
	// layer's own contract, and the composed contract is re-read from there.
	fetched := make([]map[string]any, len(refreshTargets(def, comp)))
	var results []OutputRefreshResult
	for i, target := range refreshTargets(def, comp) {
		fetched[i] = map[string]any{}
		ctx := task.RenderContext{Self: target.Self, Inputs: target.Inputs, Session: vars, Workflow: workflowOutputs, SourcePath: target.SourcePath}
		for _, src := range target.Outputs {
			values, ferr := task.FetchOutput(context.Background(), cfg, src, ctx, target.Env...)
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
				fetched[i][name] = v
				results = append(results, OutputRefreshResult{Name: name, Value: v, Fetched: true})
			}
		}
	}

	if anyFetched(fetched) {
		now := time.Now()
		if uerr := store.Update(resolvedName, func(s *domain.Session) error {
			cur := s.Tasks[instanceKey]
			if cur == nil || cur.Status == contract.TaskStatusCleaned {
				return nil
			}
			if comp == nil {
				if cur.Outputs == nil {
					cur.Outputs = map[string]any{}
				}
				for k, v := range fetched[0] {
					cur.Outputs[k] = v
				}
				s.UpdatedAt = now
				return nil
			}
			if len(cur.Layers) != len(comp.Layers) {
				return nil
			}
			for i := range fetched {
				if len(fetched[i]) == 0 {
					continue
				}
				if cur.Layers[i].Outputs == nil {
					cur.Layers[i].Outputs = map[string]any{}
				}
				for k, v := range fetched[i] {
					cur.Layers[i].Outputs[k] = v
				}
			}
			// A refreshed layer value is an underlying value of the composed
			// contract, so the projection is re-read rather than left to go
			// stale beside it.
			projected, perr := task.ProjectPublicOutputs(comp.Layers, cur.Layers, vars)
			if perr != nil {
				return perr
			}
			cur.Outputs = projected
			s.UpdatedAt = now
			return nil
		}); uerr != nil {
			return results, &Error{Code: ErrExecutionFailed, Message: uerr.Error()}
		}
	}
	return results, nil
}

// refreshTarget is one unit whose `[[outputs]]` are fetched together: a plain
// task, or one layer of a nesting chain against its own contract.
type refreshTarget struct {
	Outputs    []config.DynamicOutput
	Self       map[string]any
	Inputs     map[string]any
	Env        []string
	SourcePath string
}

// refreshTargets returns one entry per layer (or one for a plain task), in
// layer order, so a caller can index the fetched values back to the layer
// that produced them.
func refreshTargets(def config.TaskDefinition, comp *instanceComposition) []refreshTarget {
	if comp == nil {
		return []refreshTarget{{Outputs: def.DynamicOutputs, SourcePath: def.SourcePath}}
	}
	targets := make([]refreshTarget, len(comp.Layers))
	for i, layer := range comp.Layers {
		targets[i] = refreshTarget{Outputs: layer.DynamicOutputs, Self: comp.Views[i], SourcePath: layer.SourcePath}
	}
	return targets
}

func declaresDynamicOutputs(def config.TaskDefinition) bool {
	if len(def.DynamicOutputs) > 0 {
		return true
	}
	for _, inner := range def.InnerChain {
		if len(inner.DynamicOutputs) > 0 {
			return true
		}
	}
	return false
}

func anyFetched(fetched []map[string]any) bool {
	for _, m := range fetched {
		if len(m) > 0 {
			return true
		}
	}
	return false
}
