package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/dispatch"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// createWithWorkflowSetup is the workflow-setup create path:
//
//	state entry → workflow setup (acquires workdir) → cascade resolution
//	from workdir → task DAG compile → session-scoped tasks
//
// sessionName is the final id (tag already applied); resource is the
// canonical resource identifier; alias is the user's original input.
//
// The state entry is recorded before setup runs so a failed setup leaves an
// inspectable session (with the @workflow pseudo-node marked failed) that a
// later create retries and a non-force destroy can immediately release.
func createWithWorkflowSetup(cfg *config.Config, store *state.Store, params CreateParams, wf config.WorkflowFile, prov config.ProviderConfig, sessionName, resource, alias string) (*CreateResult, error) {
	if err := validateSessionName(sessionName); err != nil {
		return nil, err
	}
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}

	now := time.Now()
	var session *domain.Session
	if existing := store.Get(sessionName); existing != nil {
		if params.Inputs != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: inputsOnExistingSessionMessage()}
		}
		if params.Workflow != "" && params.Workflow != existing.Workflow {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("--workflow %q does not match the session's frozen workflow %q; destroy and recreate to switch", params.Workflow, existing.Workflow)}
		}
		if existing.Workflow != "" && existing.Workflow != wf.ID {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource dispatches to workflow %q but session %q is frozen to %q; destroy and recreate to switch", wf.ID, sessionName, existing.Workflow)}
		}
		session = existing
		if session.Tasks == nil {
			session.Tasks = make(map[string]*contract.TaskState)
		}
	} else {
		parentSession, parentErr := resolveParentSession(store, sessionName, params.ParentSession)
		if parentErr != nil {
			return nil, parentErr
		}
		// Session inputs are validated against the trusted-layer schema;
		// workdir overlays can add nodes but not tighten the input contract
		// retroactively (the workdir doesn't exist at validation time).
		input, validateErr := resolveSessionInputs(cfg, "", wf.ID, params.Inputs)
		if validateErr != nil {
			return nil, validateErr
		}
		session = &domain.Session{
			Name:          sessionName,
			ParentSession: parentSession,
			Workflow:      wf.ID,
			Inputs:        input,
			Tasks:         make(map[string]*contract.TaskState),
			CreatedAt:     now,
		}
		// Seed the dispatcher's read cursor at this fresh session's empty log tail
		// so the initial task instruction, appended below during create, is
		// delivered. The dispatcher only starts once the run scope comes up (after
		// create returns), by which point its own first-start seed would land past
		// the instruction and drop it.
		dispatch.SeedCursor(eventlog.NewStore(store.Dir()), sessionName)
	}
	session.ResourceID = resource
	session.Alias = alias
	session.UpdatedAt = now

	// Record the session before setup so partial failures stay visible.
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}

	reused := false
	if st, ok := session.Tasks[contract.WorkflowPseudoNodeID]; ok && st != nil && st.Status == contract.TaskStatusProduced {
		reused = true
	}

	vars := task.WorkflowHookVars{
		ResourceID:    resource,
		SessionName:   sessionName,
		WorkdirsRoot:  cfg.WorkdirsRoot,
		SessionInputs: session.Inputs,
	}
	outputs, setupErr := task.RunWorkflowSetup(prov, vars, session.Tasks, params.Observer)
	session.UpdatedAt = time.Now()
	if outputs != nil {
		if workdir, ok := outputs[contract.OutputKeyWorkdir].(string); ok {
			// The session's own working-directory field is the one every
			// consumer (cd/attach/ls/web UI/hooks) reads, so mirror it here.
			session.WorkdirPath = workdir
		}
		if branch, ok := outputs["branch"].(string); ok && branch != "" {
			session.Branch = branch
		}
	}
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if setupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}

	// Environment lifecycle: after provider setup (the workdir exists), before
	// session task setup. A no-op when the workflow declares no environment.
	// Fail-closed like provider setup — an environment setup failure must not
	// let task setup start.
	envSetupErr := runEnvironmentSetupForSession(cfg, wf, session, params.Observer)
	session.UpdatedAt = time.Now()
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if envSetupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envSetupErr.Error()}
	}

	// The workdir now exists: resolve the full cascade (incl. overlays above
	// and the node-only layer inside the workdir) and run session tasks.
	plan, err := buildPlanForSession(cfg, session.WorkdirPath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	envExecutor, envExecErr := environmentExecutorForSession(cfg, wf, session)
	if envExecErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envExecErr.Error()}
	}
	tasksErr := task.RunSetup(context.Background(), plan.Session, sessionVars(cfg, session), session.Tasks, params.Observer, envExecutor)
	session.UpdatedAt = time.Now()
	// A session node (the initial_task dispatcher) can shell out to a nested
	// `plect task setup` subprocess that writes its instance straight to disk.
	// Overlay our in-memory task entries onto the freshly-read session instead
	// of a blind Put so that nested write (e.g. the `initial` task) survives.
	if err := mergeTasks(store, sessionName, session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if tasksErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: tasksErr.Error()}
	}
	if refreshed := store.Get(sessionName); refreshed != nil {
		session = refreshed
	}

	// Record lifecycle.created on the first successful create (idempotent across
	// retries of a partial failure and re-runs of an already-created session).
	recordSessionCreated(store, sessionName)

	return &CreateResult{
		SessionName:   sessionName,
		WorkdirPath:   session.WorkdirPath,
		Branch:        session.Branch,
		ReusedWorkdir: reused,
		Tasks:         session.Tasks,
	}, nil
}
