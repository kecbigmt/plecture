package service

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/chain"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// evalDocumentChain decides one (chain, document instance) pair: whether it
// fires, where a fire would put the spawned session, and what its
// `[chains.inputs]` projections resolve to.
//
// The reference checks a chain carries — the target workflow, the judge ids
// its trigger names, the keys its projections read — are resolved at load
// against the contracts that declare them, so what is left here is the part
// that can only be known now: whether the trigger holds against this
// instance's live facts, and whether every projection has something to read.
func evalDocumentChain(cfg *config.Config, store *state.Store, def config.DocumentChain, workName string, session *domain.Session, instance, resource string, facts chain.Facts) ChainSpawn {
	placement := def.EffectivePlacement()
	sp := ChainSpawn{
		ChainID:       def.ID,
		Instance:      instance,
		Workflow:      def.Workflow,
		Placement:     placement,
		Resource:      resource,
		Tag:           chainSpawnTag(def.ID, instance),
		ParentSession: placementParent(placement, session, workName),
	}
	// The trigger is decided before the inputs are resolved, so an
	// unresolvable projection is reported only for a chain that would
	// otherwise really fire — the same discipline evalChain applies to the
	// workflow-existence check.
	if !chain.WhenSatisfied(def.When, facts) {
		sp.BlockedReason = chainBlockedWhenUnmet
		return sp
	}
	inputs, missing, err := resolveDocumentChainInputs(def, workName, session.Workflow, instance, facts)
	switch {
	case err != nil:
		sp.BlockedReason = chainBlockedInvalidBindings
		sp.Warnings = append(sp.Warnings, fmt.Sprintf("input projections could not be resolved: %v", err))
		return sp
	case len(missing) > 0:
		sp.BlockedReason = chainBlockedOutputsMissing
		sp.MissingOutputs = missing
		return sp
	}
	if len(inputs) > 0 {
		resolved, vErr := resolveSessionInputs(cfg, session.WorkspaceDirPath, def.Workflow, inputs)
		if vErr != nil {
			sp.BlockedReason = chainBlockedInvalidBindings
			sp.Warnings = append(sp.Warnings, fmt.Sprintf("resolved inputs violate workflow %q inputs contract: %s", def.Workflow, vErr.Message))
			return sp
		}
		sp.Inputs = resolved
	}
	sp.Fired = true
	if name, err := resolveSpawnSessionName(cfg, resource, def.Workflow, sp.Tag); err == nil && name != "" {
		sp.TargetSession = name
		if store.Get(name) != nil {
			sp.AlreadyActive = true
		}
	}
	return sp
}

// resolveDocumentChainInputs evaluates each `[chains.inputs]` projection
// against the facts of the instance that fired and the two live roots it
// reads. A projection reaching a declared key nothing has reported yet is
// returned as missing rather than as an error: that is the firing gate — a
// chain waits for the fact instead of spawning a session wired to nothing.
func resolveDocumentChainInputs(def config.DocumentChain, workName, workflow, instance string, facts chain.Facts) (map[string]any, []string, error) {
	eval := lang.Eval{Roots: documentChainRoots(workName, workflow, instance, facts)}
	out := make(map[string]any, len(def.Inputs))
	var missing []string
	for _, key := range def.InputKeys() {
		value := def.Inputs[key]
		resolved, absent, err := eval.Value(value)
		switch {
		case err != nil && value.Form == lang.FormFrom:
			missing = append(missing, value.From)
		case err != nil:
			return nil, nil, fmt.Errorf("input %q: %w", key, err)
		case absent:
		default:
			out[key] = resolved
		}
	}
	return out, missing, nil
}

// documentChainRoots is what a chain's inputs are evaluated against: the
// facts of the instance that fired, and the same two live roots its trigger
// read.
func documentChainRoots(workName, workflow, instance string, facts chain.Facts) lang.Roots {
	roots := facts.State.Roots()
	roots["task"] = map[string]any{
		"session":  workName,
		"instance": instance,
		"workflow": workflow,
		// The pending ids reach the spawned reviewer as one scalar, so a
		// `for id in ...` setup can iterate them; a list has nowhere to go in
		// a session input declared as a string.
		"done_when": map[string]any{"pending_judge_ids": strings.Join(pendingJudgeIDs(facts), " ")},
	}
	return roots
}
