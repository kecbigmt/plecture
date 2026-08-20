package task

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// ProjectPublicOutputs renders the composed task's public contract from the
// per-layer records, innermost outward: each layer's `[bind.outputs]` reads
// the layer inside it and produces the contract the layer outside it reads,
// so the value that arrives is the current one rather than a copy taken at
// setup.
//
// A binding whose sources are not produced yet contributes nothing, exactly
// as a plain task's outputs simply lack a key its setup did not emit — an
// empty string would read as a value the check leaves and templates would
// then act on.
func ProjectPublicOutputs(layers []ResolvedLayer, states []contract.TaskLayerState, session SessionVars) (map[string]any, error) {
	if len(layers) == 0 {
		return map[string]any{}, nil
	}
	if len(states) != len(layers) {
		return nil, fmt.Errorf("nesting chain has %d layers but %d layer records", len(layers), len(states))
	}
	current := orEmpty(states[len(states)-1].Outputs)
	for i := len(layers) - 2; i >= 0; i-- {
		ctx := RenderContext{
			Inner:      current,
			Locals:     states[i].Locals,
			Inputs:     states[i].Inputs,
			Session:    session,
			SourcePath: layers[i].SourcePath,
		}
		next := make(map[string]any, len(layers[i].BindOutputs))
		for _, b := range layers[i].BindOutputs {
			if b.Direct {
				if v, ok := current[b.InnerKey]; ok {
					next[b.Key] = v
				}
				continue
			}
			if !allProduced(current, b.InnerRefs) {
				continue
			}
			rendered, err := render(b.Template, ctx)
			if err != nil {
				return nil, fmt.Errorf("layer %q bind.outputs %q: %w", layers[i].TaskID, b.Key, err)
			}
			next[b.Key] = rendered
		}
		current = next
	}
	return current, nil
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
	// A binding template reads only `.Inner.outputs` and `.Locals`, both of
	// which the layer records carry, so the re-read needs no session — the
	// load-time classification rejects any other source, including the two
	// session-dependent template helpers.
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
			return 0, "", fmt.Errorf("output %q is a computed template binding and read-only; only a direct binding of an inner output routes a write", key)
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
