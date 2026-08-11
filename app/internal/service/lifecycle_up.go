package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

// UpParams holds parameters for Up.
type UpParams struct {
	Identifier string // URL or session name
	// Tag derives a tagged session name from a URL identifier. Only valid
	// when Identifier is a URL; combining Tag with a bare session name
	// returns ErrInvalidTag so misuse surfaces immediately rather than
	// silently ignoring the flag.
	Tag           string
	Workflow      string         // forwarded to auto-create; rejected when state already exists
	Inputs        map[string]any // forwarded to auto-create; rejected when state already exists
	ParentSession string         // forwarded to auto-create; empty falls back to SENNIT_SESSION_NAME when it exists and is not self.
	Observer      task.Observer
}

// UpResult holds the outcome of Up.
type UpResult struct {
	SessionName string                         `json:"session_name"`
	Tasks       map[string]*contract.TaskState `json:"tasks,omitempty"`
}

// Up runs run-scoped tasks for the given session.
//
// docker compose up-style auto-create: if the identifier is a URL, Create
// is invoked internally before run-scoped setup whenever the state entry
// is absent OR any declared session-scoped task has not reached
// "produced". Since Create is idempotent (already-produced session
// tasks are skipped), this also recovers partial-create state without
// the user having to remember to re-run `sennit create`. A bare session name
// without a state entry still errors out — Create needs URL information
// to resolve workspace/branch, so the asymmetry is intentional.
func Up(cfg *config.Config, store *state.Store, params UpParams) (*UpResult, error) {
	identifier := params.Identifier
	disp, matched, dispErr := dispatchResource(cfg, params.Workflow, params.Identifier)
	if dispErr != nil {
		// Ambiguous resolver match / invalid resolver / explicit --workflow
		// mismatch must fail here exactly as Create would — falling through
		// to the legacy path would let `sennit up` silently disagree with
		// `sennit create` for the same resource.
		if svcErr, ok := dispErr.(*Error); ok {
			return nil, svcErr
		}
		return nil, &Error{Code: ErrExecutionFailed, Message: dispErr.Error()}
	}
	if matched {
		// Resolver dispatch: the identifier is a resource id. Mirror the URL
		// auto-create semantics below with the resolver-derived session name.
		// The effective tag (explicit --tag or the workflow-id default) is
		// resolved here and forwarded to Create so up and create converge on
		// the same tagged session name.
		tag, tagErr := effectiveTag(params.Tag, disp.Workflow.ID)
		if tagErr != nil {
			return nil, tagErr
		}
		sessionName := disp.Name + "+" + tag
		existing := store.Get(sessionName)
		if params.Inputs != nil && existing != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: inputsOnExistingSessionMessage()}
		}
		if params.Workflow != "" && existing != nil && params.Workflow != existing.Workflow {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("--workflow %q does not match the session's frozen workflow %q", params.Workflow, existing.Workflow)}
		}
		if existing == nil || hasIncompleteSessionTask(cfg, existing) {
			if _, err := Create(cfg, store, CreateParams{
				URL:           params.Identifier,
				Tag:           tag,
				Workflow:      params.Workflow,
				Inputs:        params.Inputs,
				ParentSession: params.ParentSession,
				Observer:      params.Observer,
			}); err != nil {
				return nil, err
			}
		}
		identifier = sessionName
	} else {
		if params.Tag != "" {
			return nil, &Error{Code: ErrInvalidTag, Message: "--tag is only valid when the identifier is a resource a workflow resolver matches; a session name already encodes the tag"}
		}
		if params.Inputs != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: "--input is only valid on the auto-create path; a bare session name implies an existing session"}
		}
	}

	sessionName, session, err := resolveSession(cfg, store, identifier)
	if err != nil {
		return nil, err
	}
	// Bringing up an existing session runs run-scoped tasks against it; clamp
	// it to the active guard. The auto-create paths above already guard
	// via Create — this catches `sennit up <bare-existing-session>`, which skips it.
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState)
	}

	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	wf, wfErr := loadSessionWorkflow(cfg, session.WorktreePath, session)
	if wfErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}
	envExecutor, envErr := environmentExecutorForSession(cfg, wf, session)
	if envErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envErr.Error()}
	}

	// Up does not re-run environment setup — @environment is session-scoped
	// (like @workflow) and already produced during Create; environmentExecutorForSession
	// just reads its persisted outputs.
	setupErr := task.RunSetup(context.Background(), plan.Run, sessionVars(session), session.Tasks, params.Observer, envExecutor)
	session.UpdatedAt = time.Now()
	// A run-scope node's setup script can itself shell out to a nested `sennit
	// task setup` against this same session (e.g. goal_bootstrap re-deriving
	// pursue_goal instances, config/sennit/tasks/goal_bootstrap.toml) while this
	// call's own RunSetup is still in flight. That nested call persists its
	// instance straight to disk under its own store.Update. A blind Put here
	// would then overwrite disk with our in-memory map, taken before the
	// nested write landed, silently dropping it — the same hazard mergeTasks
	// already exists to close for Create's initial_task dispatcher.
	if err := mergeTasks(store, sessionName, session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if setupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}
	// Reflect nested-written keys (merged onto disk above, not our in-memory
	// map) in the result the same way Create does.
	if refreshed := store.Get(sessionName); refreshed != nil {
		session = refreshed
	}
	recordLifecycle(store, sessionName, "up", "run-scoped tasks produced")
	return &UpResult{SessionName: sessionName, Tasks: session.Tasks}, nil
}
