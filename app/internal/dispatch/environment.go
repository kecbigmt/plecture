package dispatch

import (
	"context"
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/channel"
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// taskExecutorAdapter narrows a task.Executor (ExecRequest-shaped) to the
// argv-only channel.Executor an exec channel's environment plane needs.
// dispatch is the only package that depends on both channel and task, so the
// adaptation lives here rather than coupling channel to task's Executor type.
type taskExecutorAdapter struct{ ex task.Executor }

func (a taskExecutorAdapter) Run(argv []string) (stdout, stderr []byte, err error) {
	// channel.Executor's Run signature carries no context, so there is no
	// caller context to forward.
	return a.ex.Run(context.Background(), task.ExecRequest{Argv: argv})
}

// buildChannelEnvironmentExecutor resolves the Executor an exec channel
// opting into the environment plane should route through. Resolved once per
// dispatcher build (buildDispatcher runs again on every run-scope up) since
// @environment's outputs are session-scoped and stable for the session's
// lifetime.
//
// Returns nil when the workflow declares no environment, OR when its setup
// has not (yet) succeeded (missing/failed @environment state) — a channel
// declaring execution="environment" must not silently deliver on host when
// the environment isn't actually available; deliverExec fails closed on a
// nil executor instead.
func buildChannelEnvironmentExecutor(cfg *config.Config, wf config.WorkflowFile, s *domain.Session) (channel.Executor, error) {
	if wf.Environment == "" || wf.Environment == config.ExecutionHost {
		return nil, nil
	}
	envs, err := cfg.LoadEnvironments()
	if err != nil {
		return nil, fmt.Errorf("load environments: %w", err)
	}
	env, ok := envs[wf.Environment]
	if !ok {
		return nil, fmt.Errorf("workflow %q references unknown environment %q", wf.ID, wf.Environment)
	}
	st, ok := s.Tasks[contract.EnvironmentPseudoNodeID]
	if !ok || st == nil || st.Status != contract.TaskStatusProduced {
		return nil, nil
	}
	return taskExecutorAdapter{ex: task.NewEnvironmentExecutor(env, st.Outputs)}, nil
}
