package service

import (
	"fmt"
	"log/slog"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// CreateParams holds parameters for Create — the task-aware counterpart of Add.
// Unlike Add, Create does not auto-start the runtime: the user opts into
// run-scoped lifecycle via Up. session-scoped tasks run after workspace
// creation.
type CreateParams struct {
	URL           string
	Tag           string
	Workflow      string         // workflow name from --workflow; empty = auto-select
	Inputs        map[string]any // frozen at create time; passing to an existing session returns ErrInvalidInput
	ParentSession string         // parent session name; empty falls back to PLECT_SESSION_NAME when it exists and is not self.
	Observer      task.Observer
	Population    *contract.PopulationProvenance
	// InvocationDir is the caller's directory for a direct invocation, empty
	// for a chain or a population, which read only plugin and global layers.
	InvocationDir string
}

func samePopulation(left, right *contract.PopulationProvenance) bool {
	return left != nil && right != nil && *left == *right
}

func populationCollision(session string, existing, requested *contract.PopulationProvenance) *Error {
	owner := "manual or another lifecycle authority"
	if existing != nil {
		owner = fmt.Sprintf("workflow %q population %q", existing.Workflow, existing.Name)
	}
	return &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("session %q is owned by %s; workflow %q population %q cannot adopt it", session, owner, requested.Workflow, requested.Name)}
}

// ResolvePopulationSessionName runs the same static workflow dispatch as Up
// without creating or mutating a session.
func ResolvePopulationSessionName(cfg *config.Config, workflow, resource string) (string, error) {
	dispatch, matched, err := dispatchResource(cfg, workflow, resource)
	if err != nil {
		return "", err
	}
	if !matched {
		return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no resource declaration recognizes population resource %q", resource)}
	}
	tag, tagErr := effectiveTag("", dispatch.Workflow.ID)
	if tagErr != nil {
		return "", tagErr
	}
	return dispatch.Name + "+" + tag, nil
}

// CreateResult holds the outcome of Create.
type CreateResult struct {
	SessionName string                         `json:"session_name"`
	Tasks       map[string]*contract.TaskState `json:"tasks,omitempty"`
}

// putBestEffort persists session after a step whose own error already takes
// priority in the caller's return path (e.g. mid-lifecycle checkpoints, or
// cleanup that reports via CleanupWarnings/Force). A failure here must not
// override that primary error, but must not be invisible either.
func putBestEffort(store *state.Store, session *domain.Session, context string) {
	if err := store.Put(session); err != nil {
		slog.Warn("best-effort session state persist failed", "session", session.Name, "context", context, "error", err)
	}
}

// Create establishes the session: state entry, workflow revision and every
// session-scoped node. It is idempotent: re-running it against an existing
// session retries nodes that have not yet reached "produced", which is the
// recovery path for a partially failed create.
func Create(cfg *config.Config, store *state.Store, params CreateParams) (*CreateResult, error) {
	resource := params.URL

	allowed, allowErr := cfg.IsResourceAllowed(resource)
	if allowErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: allowErr.Error()}
	}
	if !allowed {
		return nil, &Error{Code: ErrRepoNotAllowed, Message: fmt.Sprintf("resource %q is not in the allowlist", resource)}
	}

	if params.Tag != "" {
		if err := validateTagFormat(params.Tag); err != nil {
			return nil, err
		}
	}

	disp, matched, dispErr := dispatchResource(cfg, params.Workflow, resource)
	if dispErr != nil {
		if svcErr, ok := dispErr.(*Error); ok {
			return nil, svcErr
		}
		return nil, &Error{Code: ErrExecutionFailed, Message: dispErr.Error()}
	}
	if !matched {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no resource declaration recognizes %q", resource)}
	}
	tag, tagErr := effectiveTag(params.Tag, disp.Workflow.ID)
	if tagErr != nil {
		return nil, tagErr
	}
	sessionName := disp.Name + "+" + tag
	return createWithWorkflowSetup(cfg, store, params, disp.Workflow, sessionName, resource, params.URL)
}
