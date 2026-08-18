package service

import (
	"fmt"
	"slices"

	"github.com/kecbigmt/plecture/app/internal/domain"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// instanceComposition is one produced instance's nesting view: the resolved
// layer chain and each layer's own current output contract. Nil for a plain
// task, which is how every consumer tells the two apart.
type instanceComposition struct {
	Layers []task.ResolvedLayer
	Views  []map[string]any
}

// composeInstance builds that view, or returns nil for a plain task. A
// definition that no longer matches the persisted layer records — the chain
// was edited after setup — also returns nil, so a session stays readable and
// reclaimable instead of failing every evaluation.
func composeInstance(def config.TaskDefinition, st *contract.TaskState, vars task.SessionVars) (*instanceComposition, error) {
	if !def.IsNested() || st == nil {
		return nil, nil
	}
	layers, err := task.ResolveLayers(def)
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", def.ID, err)
	}
	if len(st.Layers) != len(layers) {
		return nil, nil
	}
	views, err := task.ProjectLayerOutputs(layers, st.Layers, vars)
	if err != nil {
		return nil, fmt.Errorf("task %q: %w", def.ID, err)
	}
	return &instanceComposition{Layers: layers, Views: views}, nil
}

// probeTarget is one health-probing unit: a plain task, or one layer of a
// nesting chain. Each answers only for the resources it brought up, against
// its own output contract and with the environment its enclosing layers
// inject.
type probeTarget struct {
	// Label names the failing unit in a health report — the instance for a
	// plain task, the instance and the layer for a nested one.
	Label    string
	Alive    string
	Activity string
	Self     map[string]any
	Inputs   map[string]any
	Env      []string
}

// probeTargets returns what to probe for one instance. A layer that declares
// no `[health]` yields no target: it is vacuous in the alive AND and casts no
// vote in the activity OR.
func probeTargets(instance string, def config.TaskDefinition, st *contract.TaskState, comp *instanceComposition) []probeTarget {
	if comp == nil {
		if def.Health.AliveProbe() == "" && def.Health.ActivityProbe() == "" {
			return nil
		}
		return []probeTarget{{
			Label:    instance,
			Alive:    def.Health.AliveProbe(),
			Activity: def.Health.ActivityProbe(),
			Self:     st.Outputs,
			Inputs:   st.Inputs,
		}}
	}
	var targets []probeTarget
	for i, layer := range comp.Layers {
		if layer.Health.AliveProbe() == "" && layer.Health.ActivityProbe() == "" {
			continue
		}
		targets = append(targets, probeTarget{
			Label:    fmt.Sprintf("%s layer %q", instance, layer.TaskID),
			Alive:    layer.Health.AliveProbe(),
			Activity: layer.Health.ActivityProbe(),
			Self:     comp.Views[i],
			Inputs:   st.Layers[i].Inputs,
			Env:      task.EnclosingEnv(st.Layers, i),
		})
	}
	return targets
}

// instanceOutputs is the output contract a consumer evaluates against: the
// composed public contract for a nested task, the task's own outputs
// otherwise.
func instanceOutputs(st *contract.TaskState, comp *instanceComposition) map[string]any {
	if comp == nil {
		return st.Outputs
	}
	return comp.Views[0]
}

// instanceDoneWhen is the completion contract for an instance: the
// conjunction across the chain for a nested task, with the layer that
// declared each leaf, or the task's own for a plain one.
func instanceDoneWhen(def config.TaskDefinition, comp *instanceComposition) (*config.DoneWhen, []int) {
	if comp == nil {
		return def.DoneWhen, nil
	}
	return task.ComposeDoneWhen(comp.Layers)
}

// layerBudgets is a nested instance's per-layer patience accounting. A
// budget is one layer's policy for the conditions that layer declared, so
// two budgets in one chain account for disjoint condition sets and never
// arbitrate: there is no outermost-wins rule because there is no collision.
type layerBudgets struct {
	TaskIDs     []string
	Budget      []int
	Ticks       []int
	Escalations []int
	// LeafOwner names the layer that declared each leaf of the composed
	// done_when, in the same order, or -1 for a leaf the instance added.
	LeafOwner []int
	// NextTicks is the advanced per-layer counter the caller persists, set
	// while the action is computed.
	NextTicks []int
}

// newLayerBudgets builds that accounting, or returns nil for a plain task.
// leafOwner is padded to the evaluated leaf count so instance-added leaves
// (--done-when-json) belong to no layer and consume no layer's budget.
func newLayerBudgets(comp *instanceComposition, st *contract.TaskState, leafOwner []int, leafCount int) *layerBudgets {
	if comp == nil {
		return nil
	}
	lb := &layerBudgets{
		TaskIDs:     make([]string, len(comp.Layers)),
		Budget:      make([]int, len(comp.Layers)),
		Ticks:       make([]int, len(comp.Layers)),
		Escalations: make([]int, len(comp.Layers)),
		LeafOwner:   make([]int, leafCount),
	}
	for i, layer := range comp.Layers {
		lb.TaskIDs[i] = layer.TaskID
		lb.Budget[i] = doneWhenHeartbeatBudget(layer.DoneWhen)
		if i < len(st.Layers) {
			lb.Ticks[i] = st.Layers[i].HeartbeatTicks
			lb.Escalations[i] = st.Layers[i].HeartbeatEscalations
		}
	}
	for i := range lb.LeafOwner {
		if i < len(leafOwner) {
			lb.LeafOwner[i] = leafOwner[i]
			continue
		}
		lb.LeafOwner[i] = -1
	}
	return lb
}

// unmetFor returns the items one layer still owes, which is the only thing
// that layer's budget watches.
func (lb *layerBudgets) unmetFor(layer int, result task.DoneWhenResult) []CheckUnmetItem {
	var out []CheckUnmetItem
	for i, leaf := range result.Leaves {
		if i >= len(lb.LeafOwner) || lb.LeafOwner[i] != layer {
			continue
		}
		if leaf.Status != task.DoneSatisfied {
			out = append(out, checkUnmetItem(leaf))
		}
	}
	slices.SortFunc(out, compareCheckUnmetItem)
	return out
}

// layerFacts re-keys the composed contract into one layer's own namespace:
// the keys that layer publishes, carrying the values they arrived under.
// A layer's chain names its own keys, and a key the composed contract does
// not carry is simply absent — which is the fire-time counterpart of the
// load-time rule that such a chain could never fire.
func layerFacts(comp *instanceComposition, layer int, composed map[string]any) map[string]any {
	exposure := task.LayerExposure(comp.Layers)
	out := make(map[string]any, len(exposure[layer]))
	for own, public := range exposure[layer] {
		if v, ok := composed[public]; ok {
			out[own] = v
		}
	}
	return out
}

// terminalLayerEnv is the process environment the terminal-declaring layer's
// operation commands run with: what the layers outside it inject. Empty for a
// plain task and for a chain whose outermost layer declares the endpoint.
func terminalLayerEnv(target *task.Resolved, st *contract.TaskState) []string {
	if target == nil || st == nil || len(target.Layers) == 0 {
		return nil
	}
	i := task.TerminalLayer(target.Layers)
	if i <= 0 || i >= len(st.Layers) {
		return nil
	}
	return task.EnclosingEnv(st.Layers, i)
}

// instanceGate is one instance's completion contract and the outputs it is
// evaluated against: composed across the nesting chain for a nested task, the
// task's own for a plain one, with any instance-added leaves appended in both
// cases. It is the single place every consumer of a done_when — status,
// judge, finalize, tick — asks what an instance owes.
func instanceGate(cfg *config.Config, session *domain.Session, def config.TaskDefinition, st *contract.TaskState) (*config.DoneWhen, map[string]any, error) {
	comp, err := composeInstance(def, st, sessionVars(cfg, session, nil))
	if err != nil {
		return nil, nil, err
	}
	base, _ := instanceDoneWhen(def, comp)
	dw, err := effectiveDoneWhen(base, st)
	if err != nil {
		return nil, nil, err
	}
	return dw, instanceOutputs(st, comp), nil
}
