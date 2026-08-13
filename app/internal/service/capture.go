package service

import (
	"context"
	"fmt"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// CaptureParams holds parameters for Capture.
type CaptureParams struct {
	Identifier string
}

// CaptureResult is a read-only snapshot of a session's captured channel.
type CaptureResult struct {
	SessionName string `json:"session_name"`
	TaskID      string `json:"task_id"`
	Content     string `json:"content"`
}

// Capture resolves the session, locates the task declaring `capture`
// (mirroring Attach's resolution of `attach`), and runs its template against
// that task's own outputs. The channel behind the declaration (a terminal
// multiplexer pane today)
// stays inside the task definition; core never references it.
func Capture(cfg *config.Config, store *state.Store, params CaptureParams) (*CaptureResult, error) {
	sessionName, session, err := resolveSession(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}

	plan, err := buildPlanForSession(cfg, session.WorkdirPath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	target, err := plan.CaptureTask()
	if err != nil {
		return nil, &Error{Code: ErrAmbiguousCapture, Message: err.Error()}
	}
	if target == nil {
		return nil, &Error{Code: ErrNotCapturable, Message: "this workflow has no capture-declaring task"}
	}

	st, ok := session.Tasks[target.NodeID]
	if !ok || st == nil || st.Status != contract.TaskStatusProduced {
		return nil, &Error{
			Code:    ErrNotProduced,
			Message: fmt.Sprintf("task %q is not produced; run 'plect up %s' first", target.NodeID, sessionName),
		}
	}

	content, err := task.RunCapture(context.Background(), target.Capture, st.Outputs, sessionVars(session))
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("capture failed for %q: %v", sessionName, err)}
	}

	return &CaptureResult{SessionName: sessionName, TaskID: target.NodeID, Content: content}, nil
}
