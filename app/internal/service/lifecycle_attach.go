package service

import (
	"fmt"

	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/state"
	"github.com/plecture/plect/app/internal/task"
	contract "github.com/plecture/plect/contracts/state"
)

// AttachParams holds parameters for Attach.
type AttachParams struct {
	Identifier string
}

// AttachResult is the resolved attach plan. The caller (CLI) is expected to
// hand control to the rendered command via syscall.Exec.
type AttachResult struct {
	SessionName string `json:"session_name"`
	TaskID      string `json:"task_id"`
	Command     string `json:"command"`
}

// Attach resolves the session, locates the task declaring `attach`, and
// renders its template against that task's own outputs. It does not exec
// anything — the CLI handles syscall.Exec so this stays testable.
func Attach(cfg *config.Config, store *state.Store, params AttachParams) (*AttachResult, error) {
	sessionName, session, err := resolveSession(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}

	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	target := plan.AttachTask()
	if target == nil {
		return nil, &Error{Code: ErrNotAttachable, Message: "this workflow has no attach target"}
	}

	// Resolved.Config is empty for nodes synthesized from workflow files (only
	// legacy inline `[[tasks]]` populates it). Reach for NodeID/Attach
	// directly so the workflow path renders the right command instead of
	// looking up `session.Tasks[""]`.
	st, ok := session.Tasks[target.NodeID]
	if !ok || st == nil || st.Status != contract.TaskStatusProduced {
		return nil, &Error{
			Code:    ErrNotProduced,
			Message: fmt.Sprintf("task %q is not produced; run 'plect up %s' first", target.NodeID, sessionName),
		}
	}

	cmdStr, err := task.RenderAttach(target.Attach, st.Outputs, sessionVars(session))
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("attach template: %v", err)}
	}

	return &AttachResult{
		SessionName: sessionName,
		TaskID:      target.NodeID,
		Command:     cmdStr,
	}, nil
}
