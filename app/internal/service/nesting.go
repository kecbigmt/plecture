package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// instanceComposition is one produced instance's nesting view: the resolved
// layer chain and each layer's own current output contract. Nil for a plain
// task, which is how every consumer tells the two apart.
type instanceComposition struct {
	Layers []effect.Layer
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
	layers, err := effect.ResolveLayers(def)
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
			Label:      fmt.Sprintf("%s layer %q", instance, layer.EffectID),
			Alive:      layer.Health.AliveProbe(),
			Activity:   layer.Health.ActivityProbe(),
			Self:       comp.Views[i],
			Inputs:     st.Layers[i].Inputs,
			Env:        effect.EnclosingEnv(st.Layers, i),
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

// terminalLayerEnv is the process environment the terminal-declaring layer's
// operation commands run with: what the layers outside it inject. Empty for a
// plain task and for a chain whose outermost layer declares the endpoint.
func terminalLayerEnv(target *task.Resolved, st *contract.TaskState) []string {
	if target == nil || st == nil || len(target.Layers) == 0 {
		return nil
	}
	i := effect.TerminalLayer(target.Layers)
	if i <= 0 || i >= len(st.Layers) {
		return nil
	}
	return effect.EnclosingEnv(st.Layers, i)
}
