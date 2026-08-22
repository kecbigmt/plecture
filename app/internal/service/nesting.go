package service

import (
	"fmt"
	"slices"

	"github.com/kecbigmt/plecture/app/internal/domain"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
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
	Alive    *lang.Action
	Activity *lang.Action
	Self     map[string]any
	Inputs   map[string]any
	Env      []string
	// SourcePath is the file this unit's probes were declared in — one
	// layer's own declaration for a nesting chain — so a plugin-local bare
	// `bin = "<name>"` resolves against the plugin that ships it.
	SourcePath string
	// From names the layer that wrote that declaration, which is what
	// decides whether its bin references may name another plugin.
	From lang.Ownership
}

// probe pairs one of the target's declared probes with the instance context
// it resolves against.
func (t probeTarget) probe(action *lang.Action) task.Probe {
	return task.Probe{
		Action:     action,
		Self:       t.Self,
		Inputs:     t.Inputs,
		SourcePath: t.SourcePath,
		From:       t.From,
		Env:        t.Env,
	}
}

// probeTargets returns what to probe for one instance. A layer that declares
// no `[health]` yields no target: it is vacuous in the alive AND and casts no
// vote in the activity OR.
func probeTargets(instance string, def config.TaskDefinition, st *contract.TaskState, comp *instanceComposition) []probeTarget {
	if comp == nil {
		if def.Health.AliveProbe() == nil && def.Health.ActivityProbe() == nil {
			return nil
		}
		return []probeTarget{{
			Label:      instance,
			Alive:      def.Health.AliveProbe(),
			Activity:   def.Health.ActivityProbe(),
			Self:       st.Outputs,
			Inputs:     st.Inputs,
			SourcePath: def.SourcePath,
			From:       def.Ownership(),
		}}
	}
	var targets []probeTarget
	for i, layer := range comp.Layers {
		if layer.Health.AliveProbe() == nil && layer.Health.ActivityProbe() == nil {
			continue
		}
		targets = append(targets, probeTarget{
			Label:      fmt.Sprintf("%s layer %q", instance, layer.TaskID),
			Alive:      layer.Health.AliveProbe(),
			Activity:   layer.Health.ActivityProbe(),
			Self:       comp.Views[i],
			Inputs:     st.Layers[i].Inputs,
			Env:        task.EnclosingEnv(st.Layers, i),
			SourcePath: layer.SourcePath,
			From:       layer.From,
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
	// InstanceBudget and InstanceTicks account for the leaves no layer
	// declared — the ones `--done-when-json` added to this instance. They
	// are patience for a condition set of their own, on the same footing as
	// a layer's, so an instance's extras can converge or escalate rather
	// than waiting forever.
	InstanceBudget      int
	InstanceTicks       int
	InstanceEscalations int
	HasInstanceLeaves   bool
	// NextTicks and NextInstanceTicks are the advanced counters the caller
	// persists, set while the action is computed.
	NextTicks         []int
	NextInstanceTicks int
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
		lb.HasInstanceLeaves = true
	}
	if st.DoneWhen != nil {
		lb.InstanceTicks = st.DoneWhen.HeartbeatTicks
		lb.InstanceEscalations = st.DoneWhen.HeartbeatEscalations
	}
	return lb
}

// unmetFor returns the items one owner still owes, which is the only thing
// that owner's budget watches. Layer -1 is the instance itself, holding the
// leaves no layer declared.
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

// chainDrifted reports whether a definition's chain no longer matches the
// instance's layer records, which is the state where a composed view cannot
// be built and only the outermost layer's own declarations remain readable.
//
// No layer records at all counts: a nested task's setup writes a record for
// every layer it reaches, so an instance of one carrying none was produced
// before the task was nested. That is the widest drift there is, not an
// exemption from it.
func chainDrifted(def config.TaskDefinition, st *contract.TaskState) bool {
	if !def.IsNested() || st == nil {
		return false
	}
	return len(st.Layers) != len(def.InnerChain)+1
}

// layerFacts re-keys the composed contract into one layer's own namespace:
// the keys that layer publishes, carrying the values they arrived under.
// A layer's chain names its own keys, and a key the composed contract does
// not carry is simply absent — which is the fire-time counterpart of the
// load-time rule that such a chain could never fire.
func layerFacts(exposure []map[string]string, layer int, composed map[string]any) map[string]any {
	out := make(map[string]any, len(exposure[layer]))
	for own, public := range exposure[layer] {
		if v, ok := composed[public]; ok {
			out[own] = v
		}
	}
	return out
}

// layerCompletionState re-keys one layer's view of the self root into that
// layer's own names, leaving the resource root alone: what an observer
// publishes about the resource is the same fact whichever layer reads it.
func layerCompletionState(state task.CompletionState, exposure []map[string]string, layer int) task.CompletionState {
	return task.CompletionState{
		Resource: state.Resource,
		Self:     layerFacts(exposure, layer, state.Self),
	}
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
func instanceGate(cfg *config.Config, session *domain.Session, def config.TaskDefinition, st *contract.TaskState) (*config.DoneWhen, task.CompletionState, error) {
	comp, err := composeInstance(def, st, sessionVars(cfg, session, nil))
	if err != nil {
		return nil, task.CompletionState{}, err
	}
	base, _ := instanceDoneWhen(def, comp)
	dw, err := effectiveDoneWhen(base, st)
	if err != nil {
		return nil, task.CompletionState{}, err
	}
	return dw, instanceCompletionState(st, comp), nil
}

// instanceCompletionState is the pair of live roots one instance's predicates
// read: the last observation of its resource, and what the instance holds
// about itself. A pass that acts refreshes the observation before evaluating,
// so every leaf of that pass reads one snapshot; a pass that only reports
// reads the snapshot as it stands.
//
// An effect's outputs are folded into the self root while the effect and task
// surfaces are still one declaration: a carried `[[outputs]]` entry writes
// there, and a document's own state_schema is what will declare those keys.
func instanceCompletionState(st *contract.TaskState, comp *instanceComposition) task.CompletionState {
	self := map[string]any{}
	for key, value := range instanceOutputs(st, comp) {
		self[key] = value
	}
	for key, value := range st.State {
		self[key] = value
	}
	state := task.CompletionState{Self: self}
	if st.Observed != nil {
		state.Resource = st.Observed.State
	}
	return state
}
