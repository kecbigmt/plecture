package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
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
	ParentSession string         // forwarded to auto-create; empty falls back to PLECT_SESSION_NAME when it exists and is not self.
	Observer      task.Observer
	// ForceRecreate rebuilds the runtime for an existing session while
	// preserving durable identity and event log state.
	ForceRecreate bool
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
// the user having to remember a separate command. A bare session name
// without a state entry still errors out — Create needs URL information
// to resolve session/branch, so the asymmetry is intentional.
func Up(cfg *config.Config, store *state.Store, params UpParams) (*UpResult, error) {
	identifier := params.Identifier
	forceRecreateExisting := false
	disp, matched, dispErr := dispatchResource(cfg, params.Workflow, params.Identifier)
	if dispErr != nil {
		// Ambiguous resolver match / invalid resolver / explicit --workflow
		// mismatch must fail here exactly as Create would — falling through
		// to the legacy path would let `plect up` silently disagree with
		// auto-create for the same resource.
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
		forceRecreateExisting = existing != nil
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
	// via Create — this catches `plect up <bare-existing-session>`, which skips it.
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}
	if params.ForceRecreate && !matched {
		forceRecreateExisting = true
	}
	if params.ForceRecreate && forceRecreateExisting {
		if guardErr := checkLifecycleRelationGuard(store, sessionName, "up --force-recreate"); guardErr != nil {
			return nil, guardErr
		}
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState)
	}

	plan, err := buildPlanForSession(cfg, session.WorkdirPath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	wf, wfErr := loadSessionWorkflow(cfg, session.WorkdirPath, session)
	if wfErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}
	envExecutor, envErr := environmentExecutorForSession(cfg, wf, session)
	if envErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envErr.Error()}
	}

	// Plain Up does not re-run environment setup — @environment is
	// session-scoped and already produced during Create. ForceRecreate resets
	// that state before the run-scoped setup below.
	if params.ForceRecreate && forceRecreateExisting {
		var recreateErr error
		plan, envExecutor, recreateErr = recreateSessionRuntime(cfg, store, sessionName, session, wf, plan, params.Observer, envExecutor)
		if recreateErr != nil {
			return nil, recreateErr
		}
	}
	setupErr := task.RunSetup(context.Background(), plan.Run, sessionVars(session), session.Tasks, params.Observer, envExecutor)
	session.UpdatedAt = time.Now()
	// A run-scope node's setup script can itself shell out to a nested `plect
	// task setup` against this same session (e.g. goal_bootstrap re-deriving
	// pursue_goal instances, config/plect/tasks/goal_bootstrap.toml) while this
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

func recreateSessionRuntime(cfg *config.Config, store *state.Store, sessionName string, session *domain.Session, wf config.WorkflowFile, teardownPlan *task.Plan, observer task.Observer, envExecutor task.Executor) (*task.Plan, task.Executor, error) {
	teardown, teardownErr := unifiedTeardownList(cfg, session, teardownPlan, false)
	if teardownErr != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: teardownErr.Error()}
	}
	cleanupErr := task.RunCleanup(context.Background(), teardown, sessionVars(session), session.Tasks, observer, envExecutor)
	session.UpdatedAt = time.Now()
	if cleanupErr != nil {
		if err := mergeTasks(store, sessionName, session); err != nil {
			return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
		}
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: cleanupErr.Error()}
	}

	if envCleanupErr := runEnvironmentCleanupForSession(cfg, wf, session, observer); envCleanupErr != nil {
		session.UpdatedAt = time.Now()
		if err := mergeTasks(store, sessionName, session); err != nil {
			return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
		}
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: envCleanupErr.Error()}
	}

	if wfState, ok := session.Tasks[contract.WorkflowPseudoNodeID]; ok && wfState != nil {
		if workflowCleanupErr := runWorkflowCleanupForDestroy(cfg, session, true, observer); workflowCleanupErr != nil {
			session.UpdatedAt = time.Now()
			if err := mergeTasks(store, sessionName, session); err != nil {
				return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
			}
			return nil, nil, &Error{Code: ErrExecutionFailed, Message: workflowCleanupErr.Error()}
		}
	}

	session.Branch = ""
	session.WorkdirPath = ""
	session.Conversation = nil
	session.Message = nil
	session.Tasks = make(map[string]*contract.TaskState)
	session.Health = nil
	session.LastTickAt = time.Time{}
	session.TickBackoff = nil
	session.UpdatedAt = time.Now()
	if err := replaceRuntimeState(store, sessionName, session); err != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}

	outputs, setupErr := runWorkflowSetupForSession(cfg, wf, session, observer)
	session.UpdatedAt = time.Now()
	if outputs != nil {
		if workdir, ok := outputs[contract.OutputKeyWorkdir].(string); ok {
			session.WorkdirPath = workdir
		}
		if branch, ok := outputs["branch"].(string); ok && branch != "" {
			session.Branch = branch
		}
	}
	if err := replaceRuntimeState(store, sessionName, session); err != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if setupErr != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}

	wf, wfErr := loadSessionWorkflow(cfg, session.WorkdirPath, session)
	if wfErr != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}
	setupPlan, planErr := buildPlanForSession(cfg, session.WorkdirPath, session)
	if planErr != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: planErr.Error()}
	}

	if envSetupErr := runEnvironmentSetupForSession(cfg, wf, session, observer); envSetupErr != nil {
		session.UpdatedAt = time.Now()
		if err := replaceRuntimeState(store, sessionName, session); err != nil {
			return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
		}
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: envSetupErr.Error()}
	}
	session.UpdatedAt = time.Now()
	if err := replaceRuntimeState(store, sessionName, session); err != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	envExecutor, envErr := environmentExecutorForSession(cfg, wf, session)
	if envErr != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: envErr.Error()}
	}
	setupErr = task.RunSetup(context.Background(), setupPlan.Session, sessionVars(session), session.Tasks, observer, envExecutor)
	session.UpdatedAt = time.Now()
	if err := mergeTasks(store, sessionName, session); err != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if setupErr != nil {
		return nil, nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}
	return setupPlan, envExecutor, nil
}

func runWorkflowSetupForSession(cfg *config.Config, wf config.WorkflowFile, session *domain.Session, observer task.Observer) (map[string]any, error) {
	providers, err := cfg.LoadProviders()
	if err != nil {
		return nil, fmt.Errorf("load providers: %w", err)
	}
	prov, ok, provErr := providerFor(wf, providers)
	if provErr != nil {
		return nil, provErr
	}
	if !ok {
		return nil, fmt.Errorf("workflow %q declares no provider; its setup hook cannot run", wf.ID)
	}
	vars := task.WorkflowHookVars{
		ResourceID:    session.ResourceID,
		SessionName:   session.Name,
		SessionInputs: session.Inputs,
	}
	return task.RunWorkflowSetup(prov, vars, session.Tasks, observer)
}
