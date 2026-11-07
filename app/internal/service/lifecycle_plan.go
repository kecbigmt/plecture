package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// requireSessionRoot gates anything that runs a session's stored revision:
// the revision was captured under its root's trust, which is revocable, so
// a frozen copy is no licence to keep running that root's commands.
func requireSessionRoot(cfg *config.Config, session *domain.Session) error {
	if err := cfg.RequireProjectRoot(session.ProjectRoot); err != nil {
		return &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("session %q: %v", session.Name, err)}
	}
	return nil
}

// buildPlanForSession honors the workflow name frozen on the session so
// up/down/destroy replay against the same plan create saw. A session without a
// frozen workflow is impossible now that the inline `[[tasks]]`
// path is gone, but we surface it as an error rather than panicking so a stale state
// entry from before the migration doesn't crash the binary.
func buildPlanForSession(cfg *config.Config, session *domain.Session) (*task.Plan, error) {
	if session == nil || session.Workflow == "" {
		return nil, fmt.Errorf("session has no frozen workflow; destroy and recreate it with --workflow")
	}
	if r, ok, err := config.RevisionForSession(session); ok || err != nil {
		if err != nil {
			return nil, err
		}
		return task.CompileWorkflow(r.Workflow, r.Effects)
	}
	return nil, fmt.Errorf("session %q has no stored workflow revision", session.Name)
}
