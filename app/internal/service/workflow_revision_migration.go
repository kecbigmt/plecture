package service

import (
	"fmt"
	"sort"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
)

func BackfillWorkflowRevisions(cfg *config.Config, store *state.Store) error {
	sessions, err := store.AllE()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(sessions))
	for name, s := range sessions {
		if s.Workflow != "" && s.WorkflowRevision == "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		s := sessions[name]
		revision, err := cfg.CaptureWorkflowRevision(s.WorkspaceDirPath, s.Workflow)
		if err != nil {
			return fmt.Errorf("migrate session %q workflow revision: %w", name, err)
		}
		if _, err := task.CompileWorkflow(revision.Workflow, revision.Effects); err != nil {
			return fmt.Errorf("migrate session %q workflow revision: %w", name, err)
		}
		body, digest, err := revision.CanonicalBody()
		if err != nil {
			return fmt.Errorf("migrate session %q workflow revision: %w", name, err)
		}
		if err := store.Update(name, func(current *domain.Session) error {
			if current.WorkflowRevision != "" {
				return nil
			}
			if current.Workflow != s.Workflow || current.WorkspaceDirPath != s.WorkspaceDirPath {
				return fmt.Errorf("session identity changed during workflow revision migration")
			}
			current.WorkflowRevision = digest
			current.WorkflowRevisionBody = string(body)
			return nil
		}); err != nil {
			return fmt.Errorf("migrate session %q workflow revision: %w", name, err)
		}
	}
	return store.CompleteWorkflowRevisionBackfill()
}
