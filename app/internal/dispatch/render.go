// Package dispatch is the session dispatcher: one per active session, hosted in
// the resident `plect bus serve` process. It reads a session's event log from a
// durable read cursor (so it survives session down/up) and fans each event out
// to the workflow's [[event.channel]] workers whose include filter matches,
// delivering via app/internal/channel and recording a plect.channel.error when a
// channel exhausts its retries.
package dispatch

import (
	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/domain"
	"github.com/plecture/plect/app/internal/task"
	contract "github.com/plecture/plect/contracts/state"
)

// channelInputs renders an [[event.channel]]'s input templates against the
// session's persisted node outputs — the same pass task node inputs use, so
// {{.Nodes.claude.outputs.socket_path}} resolves to the live value. Rendered
// fresh per batch because a down/up re-creates run-scoped outputs.
func channelInputs(s *domain.Session, ch config.EventChannel) (map[string]any, error) {
	deps := make(map[string]map[string]any, len(s.Tasks))
	for id, st := range s.Tasks {
		if st == nil {
			continue
		}
		out := st.Outputs
		if out == nil {
			out = map[string]any{}
		}
		deps[id] = out
	}
	var wfOutputs map[string]any
	if st := s.Tasks[contract.WorkflowPseudoNodeID]; st != nil {
		wfOutputs = st.Outputs
	}
	var envOutputs map[string]any
	if st := s.Tasks[contract.EnvironmentPseudoNodeID]; st != nil {
		envOutputs = st.Outputs
	}
	return task.RenderInputs(ch.Inputs, deps, wfOutputs, task.SessionVars{
		Name:         s.Name,
		ResourceID:   s.ResourceID,
		WorktreePath: s.WorktreePath,
		Branch:       s.Branch,
		Inputs:       s.Inputs,
	}, envOutputs)
}
