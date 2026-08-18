package dispatch

import (
	"log/slog"

	"github.com/kecbigmt/plecture/app/internal/channel"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// resolveTerminalOwner finds the workflow's [terminal]-declaring task, if
// any, once per dispatcher build — the same static answer buildDispatcher
// already resolves other workflow-level facts (channels, environment
// executor) at. A load or compile failure is logged and treated as "no
// [terminal] task": every other channel still delivers; only one that
// actually references {{terminal "..."}} would then fail per-delivery with
// a clear error, the same as a channel referencing an unwired .Inputs key.
func resolveTerminalOwner(logger *slog.Logger, cfg *config.Config, s *domain.Session, wf config.WorkflowFile) (nodeID string, ops *config.TerminalConfig, layers []task.ResolvedLayer) {
	defs, err := cfg.LoadTaskDefinitions(s.WorkspaceDirPath)
	if err != nil {
		logger.Warn("event channel {{terminal ...}} binding unavailable; load task definitions failed",
			"session", s.Name, "workflow", wf.ID, "error", err)
		return "", nil, nil
	}
	plan, err := task.CompileWorkflow(wf, defs)
	if err != nil {
		logger.Warn("event channel {{terminal ...}} binding unavailable; compile plan failed",
			"session", s.Name, "workflow", wf.ID, "error", err)
		return "", nil, nil
	}
	t := plan.TerminalTask()
	if t == nil {
		return "", nil, nil
	}
	return t.NodeID, t.Terminal, t.Layers
}

// terminalResolver builds the {{terminal "..."}} closure for one event
// delivery: it renders the [terminal]-declaring task's verb templates
// against that task's own CURRENT outputs, re-read from s.Tasks fresh (not
// cached at dispatcher-build time) since a down/up recreates them — the same
// reason channelInputs re-reads s.Tasks per drain instead of once. Returns
// nil when the workflow declares no such task, so a channel that never
// references {{terminal ...}} is unaffected.
func terminalResolver(s *domain.Session, nodeID string, ops *config.TerminalConfig, layers []task.ResolvedLayer, session task.SessionVars) channel.TerminalResolver {
	if ops == nil {
		return nil
	}
	outputs := map[string]any{}
	if st, ok := s.Tasks[nodeID]; ok && st != nil {
		if self := task.TerminalSelf(layers, st); self != nil {
			outputs = self
		}
	}
	binding := &task.TerminalBinding{Ops: ops, Outputs: outputs}
	return func(verb string) (string, error) {
		return task.RenderTerminalOp(binding, verb, session)
	}
}
