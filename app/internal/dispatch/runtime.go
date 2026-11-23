package dispatch

import (
	"log/slog"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// resolveRuntimeNodes names the nodes the health cycle reads activity from:
// run-scoped, with an activity probe on the effect or a layer of its chain.
// A delivery that reaches none of them reached no runtime the cycle can
// accuse of ignoring it, whatever the binding includes.
func resolveRuntimeNodes(logger *slog.Logger, cfg *config.Config, s *domain.Session, wf config.WorkflowFile) map[string]bool {
	defs, err := cfg.TaskDefinitionsForSession(s)
	if err != nil {
		logger.Warn("runtime deliveries untracked; load effect declarations failed", "session", s.Name, "workflow", wf.ID, "error", err)
		return nil
	}
	plan, err := task.CompileWorkflow(wf, defs)
	if err != nil {
		logger.Warn("runtime deliveries untracked; compile plan failed", "session", s.Name, "workflow", wf.ID, "error", err)
		return nil
	}
	nodes := map[string]bool{}
	for _, r := range plan.Run {
		def, ok := defs[r.TaskID]
		if !ok {
			continue
		}
		declares := def.Health.ActivityProbe() != nil
		for _, layer := range def.InnerChain {
			declares = declares || layer.Health.ActivityProbe() != nil
		}
		if declares {
			nodes[r.NodeID] = true
		}
	}
	return nodes
}

// reachesRuntime counts an inbox binding as the runtime's because only a
// runtime takes from the inbox; a push binding must read a runtime node's
// outputs or drive that node's terminal.
func (d *sessionDispatcher) reachesRuntime(ch config.EventChannel) bool {
	def, ok := d.defs[ch.Uses]
	if !ok {
		return false
	}
	if def.Type == config.ChannelTypeInbox {
		return len(d.runtimeNodes) > 0
	}
	for _, id := range lang.NodeReads(ch.Inputs, d.publicOutputs) {
		if d.runtimeNodes[id] {
			return true
		}
	}
	return d.terminal != nil && d.runtimeNodes[d.terminal.NodeID] && bindsTerminal(def.Action)
}

func bindsTerminal(a *lang.Action) bool {
	if a == nil {
		return false
	}
	values := append([]*lang.Value{a.Stdin}, a.Args...)
	for _, v := range a.Bind {
		values = append(values, v)
	}
	for _, v := range values {
		if v != nil && v.Form == lang.FormTerminal {
			return true
		}
	}
	return false
}
