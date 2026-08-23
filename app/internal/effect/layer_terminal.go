package effect

import contract "github.com/kecbigmt/plecture/contracts/state"

// TerminalLayer returns the index of the layer that declares `[terminal]`,
// or -1 when none does. At most one layer of a chain may declare it, so the
// lookup needs no tie-break.
func TerminalLayer(layers []Layer) int {
	for i, layer := range layers {
		if layer.Terminal.IsDeclared() {
			return i
		}
	}
	return -1
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
func TerminalSelf(layers []Layer, st *contract.TaskState) map[string]any {
	if st == nil {
		return nil
	}
	i := TerminalLayer(layers)
	if i < 0 || len(st.Layers) != len(layers) {
		return st.Outputs
	}
	views, err := ProjectLayerOutputs(layers, st.Layers, zeroCaps)
	if err != nil {
		return st.Outputs
	}
	return views[i]
}
