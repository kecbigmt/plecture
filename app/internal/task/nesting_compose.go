package task

import (
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
)

// LayerExposure maps, for each layer, that layer's own output keys to the
// name they arrive under in the composed contract. A key reaches the composed
// contract only through an unbroken run of direct bindings, which is what
// lets an inner layer's own declarations — its done_when leaves, its chain
// facts — be resolved against a composed instance without rewriting what the
// inner author declared.
func LayerExposure(layers []ResolvedLayer) []map[string]string {
	exposure := make([]map[string]string, len(layers))
	if len(layers) == 0 {
		return exposure
	}
	exposure[0] = map[string]string{}
	for _, key := range ownPublicKeys(layers[0]) {
		exposure[0][key] = key
	}
	for i := 0; i+1 < len(layers); i++ {
		exposure[i+1] = map[string]string{}
		for _, b := range layers[i].BindOutputs {
			if !b.Direct {
				continue
			}
			if public, ok := exposure[i][b.Key]; ok {
				exposure[i+1][b.InnerKey] = public
			}
		}
	}
	return exposure
}

// ownPublicKeys lists the public names one layer defines itself: the
// `[bind.outputs]` keys it binds and the `[[outputs]]` keys it produces, the
// two sources a public name may never share.
func ownPublicKeys(layer ResolvedLayer) []string {
	var keys []string
	for _, b := range layer.BindOutputs {
		keys = append(keys, b.Key)
	}
	for _, o := range layer.DynamicOutputs {
		keys = append(keys, o.OutputNames()...)
	}
	sort.Strings(keys)
	return keys
}

// ComposeDoneWhen conjoins the chain's completion conditions, innermost
// first, and returns the layer index that declared each leaf alongside them.
//
// Composition can only narrow completion: there is one leaf list and no
// removal syntax, so every condition an inner author declared stays
// necessary. Each check leaf keeps naming the output its own layer publishes;
// what changes is the name it resolves to on a composed instance, which is
// the public name that layer's output arrives under. Layers that declare
// nothing contribute nothing.
func ComposeDoneWhen(layers []ResolvedLayer) (*config.DoneWhen, []int) {
	if len(layers) == 0 {
		return nil, nil
	}
	exposure := LayerExposure(layers)
	var leaves []config.DoneWhenLeaf
	var owners []int
	for i := len(layers) - 1; i >= 0; i-- {
		if layers[i].DoneWhen == nil {
			continue
		}
		for _, leaf := range layers[i].DoneWhen.All {
			if leaf.Check != "" {
				if public, ok := exposure[i][leaf.Check]; ok {
					leaf.Check = public
				}
			}
			leaves = append(leaves, leaf)
			owners = append(owners, i)
		}
	}
	if len(leaves) == 0 {
		return nil, nil
	}
	// The composed table carries no budget of its own: a budget is one
	// layer's patience policy for its own conditions, and two layers'
	// budgets account for disjoint condition sets. LayerBudget reads them
	// per layer instead.
	return &config.DoneWhen{All: leaves}, owners
}

// TerminalLayer returns the index of the layer that declares `[terminal]`,
// or -1 when none does. At most one layer of a chain may declare it, so the
// lookup needs no tie-break.
func TerminalLayer(layers []ResolvedLayer) int {
	for i, layer := range layers {
		if layer.Terminal.IsDeclared() {
			return i
		}
	}
	return -1
}
