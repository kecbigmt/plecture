package service

import (
	"fmt"
	"os"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// mergeTasks persists the session by overlaying its in-memory task entries
// onto the freshly-read on-disk session under the state lock, rather than a blind
// Put. A nested `plect task setup` subprocess (the initial_task dispatcher) may
// have written instances straight to disk during the parent's setup pass; a blind
// Put of the parent's stale map would drop them. Overlaying keeps both: disk-only
// keys survive, our keys win on overlap. Non-task fields the parent owns are
// already persisted (this runs after the create's earlier Put), so only the
// tasks map and UpdatedAt need writing back.
func mergeTasks(store *state.Store, sessionName string, session *domain.Session) error {
	return store.Update(sessionName, func(s *domain.Session) error {
		if s.Tasks == nil {
			s.Tasks = make(map[string]*contract.TaskState)
		}
		for k, v := range session.Tasks {
			s.Tasks[k] = v
		}
		s.UpdatedAt = session.UpdatedAt
		return nil
	})
}

func replaceRuntimeState(store *state.Store, sessionName string, session *domain.Session) error {
	return store.Update(sessionName, func(s *domain.Session) error {
		s.Branch = session.Branch
		s.WorkdirPath = session.WorkdirPath
		s.Conversation = session.Conversation
		s.Message = session.Message
		s.Tasks = session.Tasks
		s.Health = session.Health
		s.LastTickAt = session.LastTickAt
		s.TickBackoff = session.TickBackoff
		s.UpdatedAt = session.UpdatedAt
		return nil
	})
}

func resolveParentSession(store *state.Store, sessionName, explicit string) (string, *Error) {
	candidate := explicit
	explicitSet := candidate != ""
	if candidate == "" {
		candidate = os.Getenv("PLECT_SESSION_NAME")
	}
	if candidate == "" || candidate == sessionName {
		return "", nil
	}
	if rootTarget, ok := strings.CutPrefix(candidate, "root:"); ok {
		if rootTarget == "" || store.Get(rootTarget) == nil {
			if explicitSet {
				return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("root parent target %q does not exist", rootTarget)}
			}
			return "", nil
		}
		return candidate, nil
	}
	if store.Get(candidate) == nil {
		if explicitSet {
			return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("parent session %q does not exist", candidate)}
		}
		return "", nil
	}
	return candidate, nil
}

func sessionVars(s *domain.Session) task.SessionVars {
	return task.SessionVars{
		Name:          s.Name,
		ResourceID:    s.ResourceID,
		ParentSession: s.ParentSession,
		WorkdirPath:   s.WorkdirPath,
		Branch:        s.Branch,
		Inputs:        s.Inputs,
	}
}

func inputsOnExistingSessionMessage() string {
	return "--input can only be used when creating a session.\nThis session already exists; destroy and recreate it to change input."
}

// resolveSessionInputs validates raw input against the active workflow's
// input_schema when present, falling back to the global config-level schema
// for the legacy inline-tasks path. nil is normalized to `{}` only when a
// schema is declared, so required-field configs fail fast instead of silently
// accepting `{}`.
func resolveSessionInputs(cfg *config.Config, workdirDir, workflowName string, raw map[string]any) (map[string]any, *Error) {
	inline, file, sourceID := cfg.InputsSchema, cfg.ResolvedInputsSchemaPath(), "plect:config:inputs"
	if workflowName != "" {
		workflows, err := cfg.LoadWorkflows(workdirDir)
		if err != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", err)}
		}
		if wf, ok := workflows[workflowName]; ok {
			// Workflow-level schema wins when present so each workflow can
			// gate its own input shape independently of the global default.
			if len(wf.InputsSchema) > 0 || wf.InputsSchemaFile != "" {
				inline = wf.InputsSchema
				file = wf.ResolvedInputsSchemaPath()
				sourceID = "plect:workflow:" + workflowName + ":inputs"
			}
		}
	}
	schema, err := task.CompileSchema(inline, file, sourceID)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("input schema: %v", err)}
	}
	value := raw
	if schema != nil {
		if value == nil {
			value = map[string]any{}
		}
		if vErr := schema.Validate(value); vErr != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("session input: %s", task.DescribeValidationError(schema, vErr))}
		}
	}
	return value, nil
}

// hasIncompleteSessionTask returns true if the merged task config
// declares any session-scoped task that the session has not yet brought
// to "produced" status. Used by Up to decide whether to invoke Create
// for partial-create recovery. Errors building the plan map to false so
// the caller proceeds and surfaces the error through its own path.
func hasIncompleteSessionTask(cfg *config.Config, session *domain.Session) bool {
	// A provider-backed workflow needs its pseudo-node produced too —
	// a failed/absent setup is exactly the partial-create state to recover.
	if workflows, err := cfg.LoadWorkflows(session.WorkdirPath); err == nil {
		if wf, ok := workflows[session.Workflow]; ok && wf.Provider != "" {
			st, ok := session.Tasks[contract.WorkflowPseudoNodeID]
			if !ok || st == nil || st.Status != contract.TaskStatusProduced {
				return true
			}
		}
	}
	plan, err := buildPlanForSession(cfg, session.WorkdirPath, session)
	if err != nil || plan == nil {
		return false
	}
	for _, r := range plan.Session {
		st, ok := session.Tasks[r.NodeID]
		if !ok || st == nil || st.Status != contract.TaskStatusProduced {
			return true
		}
	}
	return false
}
