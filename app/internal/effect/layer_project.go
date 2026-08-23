package effect

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// CapsFor resolves one layer's execution capabilities from its own
// declaration (source file, ownership) — the same shape ChainHost.Caps
// takes, reused here so a projection and a chain walk share one notion of
// "how to build this layer's capabilities."
type CapsFor func(layer Layer) Capabilities

// zeroCaps is what a projection with no live session gets: a layer's own bin
// references still resolve against its declaring file, but no terminal
// capability exists to resolve against. Used where re-deriving a public
// contract (a cleanup's self view, a post-write re-projection, a terminal
// display) has no session in scope to build real capabilities from.
func zeroCaps(layer Layer) Capabilities {
	bins := config.MountedBins{SourcePath: layer.SourcePath}
	return Capabilities{Bin: func(ref string) (string, error) { return bins.ResolveBin(ref, layer.From) }}
}

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
func ProjectPublicOutputs(layers []Layer, states []contract.LayerState, capsFor CapsFor) (map[string]any, error) {
	views, err := ProjectLayerOutputs(layers, states, capsFor)
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
func ProjectLayerOutputs(layers []Layer, states []contract.LayerState, capsFor CapsFor) ([]map[string]any, error) {
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
			resolved, absent, err := computeBoundOutput(b, inner, states[i].Locals, states[i].Inputs, layers[i], capsFor)
			if err != nil {
				return nil, fmt.Errorf("layer %q outputs.bind %q: %w", layers[i].EffectID, b.Key, err)
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
// beyond the layer's own inner/locals/inputs, and the capabilities its own
// declaration resolves against.
func computeBoundOutput(b config.OutputBinding, inner map[string]any, locals, inputs map[string]any, layer Layer, capsFor CapsFor) (any, bool, error) {
	roots := lang.Roots{
		"inner":  map[string]any{"outputs": orEmpty(lang.NormalizeOutputs(inner))},
		"locals": lang.NormalizeOutputs(locals),
		"inputs": lang.NormalizeOutputs(inputs),
	}
	eval := capsFor(layer).Eval(roots, "")
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
func ApplyMutableOutputs(layers []Layer, st *contract.TaskState, updates map[string]any) error {
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
	projected, err := ProjectPublicOutputs(layers, st.Layers, zeroCaps)
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
func routeMutableKey(layers []Layer, key string) (int, string, error) {
	current := key
	for i := 0; i+1 < len(layers); i++ {
		b, ok := findBinding(layers[i].BindOutputs, current)
		if !ok {
			return 0, "", fmt.Errorf("output %q is not bound by layer %q, so a write has no inner output to address", current, layers[i].EffectID)
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
