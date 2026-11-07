package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

const initialInstance = "initial"

// InitialTask is an initiating caller's task selection; an empty Resource
// binds the entry identifier.
type InitialTask struct {
	TaskID   string
	Resource string
}

func InitialTaskSelection(taskID, resource string) *InitialTask {
	if taskID == "" && resource == "" {
		return nil
	}
	return &InitialTask{TaskID: taskID, Resource: resource}
}

type initialTaskBinding struct {
	TaskID   string
	Resource string
}

// checkInitialTask runs before anything is created, so a selection that can
// never instantiate leaves no session behind.
func checkInitialTask(cfg *config.Config, invocationDir, entry string, existing *domain.Session, sel *InitialTask) (*initialTaskBinding, error) {
	if sel == nil {
		return nil, nil
	}
	if strings.TrimSpace(sel.TaskID) == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "a task resource needs a task: pass --task with --task-resource"}
	}
	// An existing session instantiates from its own revision, so the check
	// reads the same declarations rather than today's configuration.
	var docs map[string]config.TaskDocument
	var effects map[string]config.TaskDefinition
	var err error
	if existing != nil {
		docs, effects, err = cfg.TaskDeclarationsForSession(existing)
	} else {
		docs, effects, err = cfg.LoadTaskDeclarations(invocationDir)
	}
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task declarations: %v", err)}
	}
	doc, ok := docs[sel.TaskID]
	if !ok {
		if _, isEffect := effects[sel.TaskID]; isEffect {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("%q is an effect, not a task document; an initial task must be a task document written for a resource type", sel.TaskID)}
		}
		hint := config.AddressHint(config.Addresses(docs), sel.TaskID)
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task %q not found%s", sel.TaskID, hint)}
	}
	resource := sel.Resource
	if resource == "" {
		resource = entry
	}
	observers, err := cfg.LoadResourceDefs()
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load resources: %v", err)}
	}
	if _, bindErr := checkTaskBinding(doc, observers, resource); bindErr != nil {
		return nil, bindErr
	}
	return &initialTaskBinding{TaskID: sel.TaskID, Resource: resource}, nil
}

// EnsureInitialTask skips checkInitialTask because a population checks its
// task against its entry resource when it loads.
func EnsureInitialTask(cfg *config.Config, store *state.Store, sessionName string, sel InitialTask) error {
	return ensureInitialTask(cfg, store, sessionName, &initialTaskBinding{TaskID: sel.TaskID, Resource: sel.Resource})
}

// ensureInitialTask refuses to replace a different existing instance,
// because doing so would discard its state and verdicts.
func ensureInitialTask(cfg *config.Config, store *state.Store, sessionName string, binding *initialTaskBinding) error {
	if binding == nil {
		return nil
	}
	current, err := store.GetE(sessionName)
	if err != nil {
		return &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("read session %q state: %v", sessionName, err)}
	}
	if current == nil {
		return &Error{Code: ErrSessionNotFound, Message: fmt.Sprintf("session %q disappeared before its initial task was set up", sessionName)}
	}
	if existing := current.Tasks[initialInstance]; existing != nil {
		same := domain.Locator(existing.Resource) == binding.Resource
		if !same {
			bound, boundErr := boundResource(cfg, store, binding.Resource)
			if boundErr != nil {
				return boundErr
			}
			same = sameResource(existing.Resource, bound)
		}
		if existing.TaskID != binding.TaskID || !same {
			return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("session %q already has initial task %q bound to %q; run `plect task cleanup initial --session %s` before selecting %q bound to %q", sessionName, existing.TaskID, domain.Locator(existing.Resource), sessionName, binding.TaskID, binding.Resource)}
		}
		if existing.Status == contract.TaskStatusProduced {
			return nil
		}
		return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("session %q initial task is %q; run `plect task cleanup initial --session %s` before selecting it again", sessionName, existing.Status, sessionName)}
	}
	_, err = TaskSetup(cfg, store, TaskSetupParams{
		TaskID:      binding.TaskID,
		SessionName: sessionName,
		Name:        initialInstance,
		Resource:    binding.Resource,
	})
	return err
}

// keepInitialSelection records a failed initial instance when a forced
// recreate fails before re-instantiating it, because that instance is the
// only durable record of the selection and the next recreate captures it.
func keepInitialSelection(cfg *config.Config, store *state.Store, sessionName string, binding *initialTaskBinding, cause error) error {
	if binding == nil {
		return cause
	}
	bound, boundErr := boundResource(cfg, store, binding.Resource)
	if boundErr != nil {
		return &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("%v (and the initial task selection could not be kept: %v)", cause, boundErr)}
	}
	now := time.Now()
	if err := store.Update(sessionName, func(s *domain.Session) error {
		if s.Tasks == nil {
			s.Tasks = make(map[string]*contract.TaskState)
		}
		if s.Tasks[initialInstance] != nil {
			return nil
		}
		s.Tasks[initialInstance] = &contract.TaskState{
			Scope:    contract.TaskScopeSession,
			TaskID:   binding.TaskID,
			Resource: bound,
			Name:     initialInstance,
			Status:   contract.TaskStatusFailed,
			Error:    fmt.Sprintf("not re-instantiated: %v", cause),
			Seq:      task.NextSeq(s.Nodes, s.Tasks),
			FailedAt: now,
		}
		s.UpdatedAt = now
		return nil
	}); err != nil {
		return &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("%v (and the initial task selection could not be kept: %v)", cause, err)}
	}
	return cause
}
