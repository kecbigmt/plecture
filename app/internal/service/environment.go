package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// sessionEnvironment resolves the environment definition a workflow
// declares, or ok=false for host degeneration (Environment unset or "host").
func sessionEnvironment(cfg *config.Config, wf config.WorkflowFile) (config.EnvironmentConfig, bool, error) {
	if wf.Environment == "" || wf.Environment == config.ExecutionHost {
		return config.EnvironmentConfig{}, false, nil
	}
	envs, err := cfg.LoadEnvironments()
	if err != nil {
		return config.EnvironmentConfig{}, false, fmt.Errorf("load environments: %w", err)
	}
	env, ok := envs[wf.Environment]
	if !ok {
		return config.EnvironmentConfig{}, false, fmt.Errorf("workflow %q references unknown environment %q", wf.ID, wf.Environment)
	}
	return env, true, nil
}

func environmentHookVars(wf config.WorkflowFile, session *domain.Session) task.EnvironmentHookVars {
	return task.EnvironmentHookVars{
		ResourceID:        session.ResourceID,
		SessionName:       session.Name,
		SessionInputs:     session.Inputs,
		WorktreePath:      session.WorktreePath,
		EnvironmentInputs: wf.EnvironmentInputs,
	}
}

// runEnvironmentSetupForSession runs the @environment pseudo-node's setup (a
// no-op when the workflow declares no environment). Must run after provider
// setup (the worktree exists) and before session task setup: provider ->
// environment -> tasks.
func runEnvironmentSetupForSession(cfg *config.Config, wf config.WorkflowFile, session *domain.Session, observer task.Observer) error {
	env, ok, err := sessionEnvironment(cfg, wf)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	_, err = task.RunEnvironmentSetup(env, environmentHookVars(wf, session), session.Tasks, observer)
	return err
}

// runEnvironmentCleanupForSession runs the @environment pseudo-node's
// cleanup (a no-op when the workflow declares no environment, or setup never
// ran). Must run after run+session task cleanup and before provider cleanup:
// tasks -> environment -> provider.
func runEnvironmentCleanupForSession(cfg *config.Config, wf config.WorkflowFile, session *domain.Session, observer task.Observer) error {
	env, ok, err := sessionEnvironment(cfg, wf)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return task.RunEnvironmentCleanup(env, environmentHookVars(wf, session), session.Tasks, observer)
}

// environmentExecutorForSession builds the Executor that routes a session's
// execution="environment" nodes through the workflow's environment `exec`
// wrapper. Returns nil when the workflow declares no environment, OR when
// its setup has not (yet) succeeded (missing/failed @environment state) —
// callers must not treat nil as "fall back to host" for an
// execution="environment" node; task.execForNode fails closed on a nil
// envExecutor instead of silently downgrading the plane.
func environmentExecutorForSession(cfg *config.Config, wf config.WorkflowFile, session *domain.Session) (task.Executor, error) {
	env, ok, err := sessionEnvironment(cfg, wf)
	if err != nil || !ok {
		return nil, err
	}
	st, ok := session.Tasks[contract.EnvironmentPseudoNodeID]
	if !ok || st == nil || st.Status != contract.TaskStatusProduced {
		return nil, nil
	}
	return task.NewEnvironmentExecutor(env, st.Outputs), nil
}

// loadSessionWorkflow reloads the workflow a session is frozen to. Used by
// lifecycle paths (up/down/destroy/task setup/cleanup) that only have the
// session in hand, not an already-loaded WorkflowFile.
func loadSessionWorkflow(cfg *config.Config, worktreeDir string, session *domain.Session) (config.WorkflowFile, error) {
	if session == nil || session.Workflow == "" {
		return config.WorkflowFile{}, nil
	}
	workflows, err := cfg.LoadWorkflows(worktreeDir)
	if err != nil {
		return config.WorkflowFile{}, fmt.Errorf("load workflows: %w", err)
	}
	wf, ok := workflows[session.Workflow]
	if !ok {
		return config.WorkflowFile{}, fmt.Errorf("workflow %q not found", session.Workflow)
	}
	return wf, nil
}
