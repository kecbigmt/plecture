package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// buildPlanForSession honors the workflow name frozen on the session so
// up/down/destroy replay against the same plan create saw. A session without a
// frozen workflow is impossible now that the inline `[[tasks]]`
// path is gone, but we surface it as an error rather than panicking so a stale state
// entry from before the migration doesn't crash the binary.
func buildPlanForSession(cfg *config.Config, worktreeDir string, session *domain.Session) (*task.Plan, error) {
	if session == nil || session.Workflow == "" {
		return nil, fmt.Errorf("session has no frozen workflow; destroy and recreate it with --workflow")
	}
	return buildWorkflowPlan(cfg, worktreeDir, session.Workflow)
}

// buildWorkflowPlan loads `.plecture/workflows/<name>.toml` (+ referenced task
// definitions) and compiles it. Returns a clear "not found" error when the
// named workflow is missing so the CLI surfaces "did you forget to add the
// file?" instead of an empty plan that silently does nothing.
func buildWorkflowPlan(cfg *config.Config, worktreeDir, name string) (*task.Plan, error) {
	workflows, err := cfg.LoadWorkflows(worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("load workflows: %w", err)
	}
	wf, ok := workflows[name]
	if !ok {
		return nil, fmt.Errorf("workflow %q not found in .plecture/workflows or global config", name)
	}
	defs, err := cfg.LoadTaskDefinitions(worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("load task definitions: %w", err)
	}
	return task.CompileWorkflow(wf, defs)
}

// selectWorkflow decides which workflow name to freeze onto a new session.
//
//  1. Explicit --workflow flag wins; we still verify the file exists so the
//     user catches typos at create time, not at the next up/down/destroy.
//  2. With exactly one workflow on disk, default to it. Single-workflow
//     setups shouldn't need to type the name.
//  3. With zero workflows on disk, error — every session needs a workflow
//     (there is no more inline-tasks fallback).
//  4. Multiple workflows on disk without a flag is ambiguous — error so the
//     user picks one explicitly.
func selectWorkflow(cfg *config.Config, worktreeDir, flag string) (string, *Error) {
	workflows, err := cfg.LoadWorkflows(worktreeDir)
	if err != nil {
		return "", &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", err)}
	}
	if flag != "" {
		if _, ok := workflows[flag]; !ok {
			return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q not found; add .plecture/workflows/%s.toml", flag, flag)}
		}
		return flag, nil
	}
	switch len(workflows) {
	case 0:
		return "", &Error{
			Code:    ErrInvalidInput,
			Message: "no workflows found; add .plecture/workflows/<name>.toml or pass --workflow",
		}
	case 1:
		for name := range workflows {
			return name, nil
		}
	}
	names := make([]string, 0, len(workflows))
	for name := range workflows {
		names = append(names, name)
	}
	return "", &Error{
		Code:    ErrInvalidInput,
		Message: fmt.Sprintf("multiple workflows present; pass --workflow to choose: %v", names),
	}
}
