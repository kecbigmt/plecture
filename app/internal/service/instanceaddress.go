package service

import (
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// nodeAddresses maps a session's node ids to the addresses the effects behind
// them answer to, read off the workflow the session runs.
//
// A workflow node's instance stores no task id when the two agree, so the node
// id is all a later lookup has — and a node id is not an address, because it
// defaults to a reference's last segment rather than the whole reference. The
// workflow that wrote the reference is the only thing that still knows which
// declaration the node runs.
func nodeAddresses(cfg *config.Config, session *domain.Session) map[string]string {
	if session == nil {
		return nil
	}
	wf := sessionWorkflowConfig(cfg, session.Workflow, session.WorkspaceDirPath)
	if wf == nil {
		return nil
	}
	out := make(map[string]string, len(wf.Nodes))
	for _, node := range wf.Nodes {
		if node.Uses != "" {
			out[node.ID] = node.Uses
		}
	}
	return out
}

// instanceDefinitionAddress answers which declaration an instance runs. A
// stored task id is already the address the reference resolved to; without one
// the instance is a workflow node, and nodes resolve through the workflow.
func instanceDefinitionAddress(key string, st *contract.TaskState, nodes map[string]string) string {
	if id := taskIDForInstance(key, st); id != key {
		return id
	}
	if address, ok := nodes[key]; ok {
		return address
	}
	return key
}
