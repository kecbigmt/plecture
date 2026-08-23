package task

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/lang"
)

// The two environments below are docs/language/values.md's rows for the
// workflow surface, kept beside the effect surfaces' constructors in
// effect.go: a surface exposes only the context it is allowed to observe, so
// a root a surface does not offer is absent from the tree rather than present
// and ignored.

// nodeInputsRoots is what a node's input wiring — and an event channel's,
// which reads the same roots — observes: what the session has already
// produced, and nothing of the node's own, since its inputs are what this
// environment resolves.
func nodeInputsRoots(ctx RenderContext) lang.Roots {
	env := sessionRoots(ctx)
	env["nodes"] = nodeRoots(ctx.Tasks)
	env["workflow"] = map[string]any{"outputs": orEmpty(normalizeOutputs(ctx.Workflow))}
	return env
}

// displayRoots is the listing surface: persisted outputs and the session's
// own inputs, so rendering a listing reads state and never the network.
func displayRoots(wfOutputs, sessionInputs map[string]any) lang.Roots {
	return lang.Roots{
		"workflow": map[string]any{"outputs": orEmpty(normalizeOutputs(wfOutputs))},
		"session":  map[string]any{"inputs": orEmpty(normalizeOutputs(sessionInputs))},
	}
}

// ResolveNodeInputs resolves one node's input wiring against the node-inputs
// surface. The result is what the node's setup receives as `inputs.<key>` and
// what is persisted as the instance's inputs.
//
// A projection whose source has nothing to report is a contract statement,
// not an empty string: it fails unless the value declares a default or is
// optional, so a reference to an output no upstream node produced surfaces as
// the wiring error it is.
func ResolveNodeInputs(inputs map[string]*lang.Value, deps map[string]map[string]any, wfOutputs map[string]any, session SessionVars) (map[string]any, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	ctx := RenderContext{Tasks: deps, Workflow: wfOutputs, Session: session}
	eval := lang.Eval{Roots: nodeInputsRoots(ctx)}
	out := make(map[string]any, len(inputs))
	for _, key := range sortedValueKeys(inputs) {
		value, absent, err := eval.Value(inputs[key])
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", key, err)
		}
		if absent {
			continue
		}
		out[key] = value
	}
	return out, nil
}

// ResolveDisplay resolves one of a workflow's display values against a
// session's persisted state. An unresolved projection is reported rather than
// rendered as a blank, so a caller can leave the field at whatever it already
// shows instead of replacing it with an empty line.
func ResolveDisplay(value *lang.Value, wfOutputs, sessionInputs map[string]any) (string, error) {
	eval := lang.Eval{Roots: displayRoots(wfOutputs, sessionInputs)}
	resolved, absent, err := eval.Argument(value)
	if err != nil {
		return "", err
	}
	if absent {
		return "", nil
	}
	return resolved, nil
}

func sortedValueKeys(values map[string]*lang.Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
