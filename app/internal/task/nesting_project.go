package task

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// ProjectPublicOutputs resolves the composed task's public contract from the
// per-layer records, innermost outward: each layer's `[outputs.bind]` reads
// the layer inside it and produces the contract the layer outside it reads,
// so the value that arrives is the current one rather than a copy taken at
// setup.
//
// A binding whose sources are not produced yet contributes nothing, exactly
// as a plain task's outputs simply lack a key its setup did not emit — an
// empty string would read as a value the check leaves and templates would
// then act on.
func ProjectPublicOutputs(layers []ResolvedLayer, states []contract.LayerState, session SessionVars) (map[string]any, error) {
	views, err := ProjectLayerOutputs(layers, states, session)
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return map[string]any{}, nil
	}
	return views[0], nil
}

// ProjectLayerOutputs returns every layer's own public contract, outermost
// first: what the layer outside it reads, and what that layer's own probes,
// output scripts, and completion conditions are written against. Index 0 is
// the composed task's contract.
//
// A layer's contract is its `[outputs.bind]` projection of the layer inside
// it plus the values that layer produces itself — the two sources one public
// name may never share, which is why merging them needs no precedence rule.
func ProjectLayerOutputs(layers []ResolvedLayer, states []contract.LayerState, session SessionVars) ([]map[string]any, error) {
	if len(layers) == 0 {
		return nil, nil
	}
	if len(states) != len(layers) {
		return nil, fmt.Errorf("nesting chain has %d layers but %d layer records", len(layers), len(states))
	}
	views := make([]map[string]any, len(layers))
	views[len(layers)-1] = orEmpty(states[len(layers)-1].Outputs)
	for i := len(layers) - 2; i >= 0; i-- {
		inner := views[i+1]
		ctx := RenderContext{
			Inner:      inner,
			Locals:     states[i].Locals,
			Inputs:     states[i].Inputs,
			Session:    session,
			SourcePath: layers[i].SourcePath,
		}
		view := make(map[string]any, len(layers[i].BindOutputs)+len(states[i].Outputs))
		for _, b := range layers[i].BindOutputs {
			if b.Direct {
				if v, ok := inner[b.InnerKey]; ok {
					view[b.Key] = v
				}
				continue
			}
			if !allProduced(inner, b.InnerRefs) {
				continue
			}
			resolved, absent, err := computeBoundOutput(b, ctx, layers[i].From)
			if err != nil {
				return nil, fmt.Errorf("layer %q outputs.bind %q: %w", layers[i].TaskID, b.Key, err)
			}
			if absent {
				continue
			}
			view[b.Key] = resolved
		}
		for k, v := range states[i].Outputs {
			view[k] = v
		}
		views[i] = view
	}
	return views, nil
}

// computeBoundOutput resolves one computed binding. It observes no live root
// and no session, so it needs neither: the layer records carry everything
// `[outputs.bind]` may read.
func computeBoundOutput(b config.OutputBinding, ctx RenderContext, from lang.Ownership) (any, bool, error) {
	eval := capabilitiesFor(ctx, from).Eval(outputsBindRoots(ctx), "")
	return eval.Value(b.Value)
}

func allProduced(outputs map[string]any, keys []string) bool {
	for _, k := range keys {
		if _, ok := outputs[k]; !ok {
			return false
		}
	}
	return true
}

// ApplyMutableOutputs writes updates to a nested task's public keys by
// routing each one along its direct-binding chain to the inner output it
// publishes, then re-projects the public contract from the updated records.
// A computed binding is a rendering with no inner output behind it, so it is
// read-only and named as such.
func ApplyMutableOutputs(layers []ResolvedLayer, st *contract.TaskState, updates map[string]any) error {
	keys := make([]string, 0, len(updates))
	for k := range updates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(st.Layers) != len(layers) {
		return fmt.Errorf("nesting chain has %d layers but %d layer records", len(layers), len(st.Layers))
	}
	for _, key := range keys {
		layerIdx, innerKey, err := routeMutableKey(layers, key)
		if err != nil {
			return err
		}
		if st.Layers[layerIdx].Outputs == nil {
			st.Layers[layerIdx].Outputs = map[string]any{}
		}
		st.Layers[layerIdx].Outputs[innerKey] = updates[key]
	}
	// A binding reads only `inner.outputs`, `locals`, and this layer's own
	// `inputs`, all of which the layer records carry, so the re-read needs no
	// session — the surface offers no session root to begin with.
	projected, err := ProjectPublicOutputs(layers, st.Layers, SessionVars{})
	if err != nil {
		return err
	}
	// The projection owns the keys it binds and nothing else. A public key
	// the outer layer produces itself is equally part of the contract, so
	// replacing the map wholesale would drop it — and with it the value a
	// completion check may already have been satisfied by.
	for key, value := range st.Outputs {
		if _, bound := findBinding(layers[0].BindOutputs, key); bound {
			continue
		}
		if _, fresh := projected[key]; fresh {
			continue
		}
		projected[key] = value
	}
	st.Outputs = projected
	return nil
}

// routeMutableKey follows a public key inward along direct bindings to the
// layer and inner-output name a write addresses.
func routeMutableKey(layers []ResolvedLayer, key string) (int, string, error) {
	current := key
	for i := 0; i+1 < len(layers); i++ {
		b, ok := findBinding(layers[i].BindOutputs, current)
		if !ok {
			return 0, "", fmt.Errorf("output %q is not bound by layer %q, so a write has no inner output to address", current, layers[i].TaskID)
		}
		if !b.Direct {
			return 0, "", fmt.Errorf("output %q is a computed binding and read-only; only a direct projection of an inner output routes a write", key)
		}
		current = b.InnerKey
	}
	return len(layers) - 1, current, nil
}

func findBinding(bindings []config.OutputBinding, key string) (config.OutputBinding, bool) {
	for _, b := range bindings {
		if b.Key == key {
			return b, true
		}
	}
	return config.OutputBinding{}, false
}

// TerminalSelf is the output contract an effect's `[terminal]` verbs resolve
// against as `self.outputs`: the declaring layer's own contract for a nesting
// chain, the effect's own outputs for a plain effect. The verbs belong to the
// layer that declared them and name that layer's keys, the same way its
// probes and output scripts do — the composed contract may carry those keys
// under other names, or not at all.
//
// A projection failure degrades to the composed outputs rather than erroring:
// this feeds attach, capture, and status display, where refusing to resolve
// is worse than resolving against the contract the instance already
// published.
func TerminalSelf(layers []ResolvedLayer, st *contract.TaskState) map[string]any {
	if st == nil {
		return nil
	}
	i := TerminalLayer(layers)
	if i < 0 || len(st.Layers) != len(layers) {
		return st.Outputs
	}
	views, err := ProjectLayerOutputs(layers, st.Layers, SessionVars{})
	if err != nil {
		return st.Outputs
	}
	return views[i]
}
