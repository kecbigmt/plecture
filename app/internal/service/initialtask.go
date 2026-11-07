package service

import (
	"fmt"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// initialInstance is the instance name an initiating caller's task takes, so
// a repeated selection finds the instance an earlier one created.
const initialInstance = "initial"

// InitialTask is an initiating caller's selection of the one task a session
// starts with. Resource is the concrete resource the task binds; empty binds
// the entry identifier.
type InitialTask struct {
	TaskID   string
	Resource string
	// ResourceInputs is the task binding's own action input object; a task
	// bound to the entry identifier does not inherit the entry's.
	ResourceInputs map[string]any
}

// InitialTaskSelection builds the selection a caller's task flags state, nil
// when they state none, so every surface treats an empty flag as no task.
func InitialTaskSelection(taskID, resource string, resourceInputs map[string]any) *InitialTask {
	if taskID == "" && resource == "" && resourceInputs == nil {
		return nil
	}
	return &InitialTask{TaskID: taskID, Resource: resource, ResourceInputs: resourceInputs}
}

// initialTaskBinding is a selection checked against the configuration: the
// task resolves to a task document and its concrete resource is one that
// document's resource type claims.
type initialTaskBinding struct {
	TaskID         string
	Resource       string
	ResourceInputs map[string]any
}

// checkInitialTask runs before anything is created, so a selection that can
// never instantiate leaves no session behind.
func checkInitialTask(cfg *config.Config, invocationDir, entry string, sel *InitialTask) (*initialTaskBinding, error) {
	if sel == nil {
		return nil, nil
	}
	if strings.TrimSpace(sel.TaskID) == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "a task resource or its inputs need a task: pass --task"}
	}
	docs, effects, err := cfg.LoadTaskDeclarations(invocationDir)
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
	observer, bindErr := checkTaskBinding(doc, observers, resource)
	if bindErr != nil {
		return nil, bindErr
	}
	if err := task.ValidateResourceInputs(observer, sel.ResourceInputs); err != nil {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("task %s resource inputs: %v", doc.ID, err)}
	}
	return &initialTaskBinding{TaskID: sel.TaskID, Resource: resource, ResourceInputs: sel.ResourceInputs}, nil
}

// EnsureInitialTask converges a session's initial instance on a selection a
// caller already checked: a population checks its task against its entry
// resource when it loads.
func EnsureInitialTask(cfg *config.Config, store *state.Store, sessionName string, sel InitialTask) error {
	return ensureInitialTask(cfg, store, sessionName, &initialTaskBinding{TaskID: sel.TaskID, Resource: sel.Resource, ResourceInputs: sel.ResourceInputs})
}

// ensureInitialTask converges the session's initial instance on the
// binding: an instance already produced for the same task and resource is
// kept, and any other existing instance must be cleaned by the caller,
// because silently replacing it would discard its state and verdicts.
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
		if existing.TaskID != binding.TaskID || existing.Resource != binding.Resource {
			return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("session %q already has initial task %q bound to %q; run `plect task cleanup initial --session %s` before selecting %q bound to %q", sessionName, existing.TaskID, existing.Resource, sessionName, binding.TaskID, binding.Resource)}
		}
		if existing.Status == contract.TaskStatusProduced {
			return nil
		}
		return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("session %q initial task is %q; run `plect task cleanup initial --session %s` before selecting it again", sessionName, existing.Status, sessionName)}
	}
	_, err = TaskSetup(cfg, store, TaskSetupParams{
		TaskID:         binding.TaskID,
		SessionName:    sessionName,
		Name:           initialInstance,
		Resource:       binding.Resource,
		ResourceInputs: binding.ResourceInputs,
	})
	return err
}
