package service

import (
	"fmt"
	"log/slog"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/domain"
	"github.com/cradel-dev/cradel/app/internal/state"
	"github.com/cradel-dev/cradel/app/internal/task"
	contract "github.com/cradel-dev/cradel/contracts/state"
)

// CreateParams holds parameters for Create — the task-aware counterpart of Add.
// Unlike Add, Create does not auto-start the runtime: the user opts into
// run-scoped lifecycle via Up. session-scoped tasks run after worktree
// creation.
type CreateParams struct {
	URL           string
	Tag           string
	Workflow      string         // workflow name from --workflow; empty = auto-select
	Inputs        map[string]any // frozen at create time; passing to an existing session returns ErrInvalidInput
	ParentSession string         // parent session name; empty falls back to SENNIT_SESSION_NAME when it exists and is not self.
	Observer      task.Observer
}

// CreateResult holds the outcome of Create.
type CreateResult struct {
	SessionName    string                         `json:"session_name"`
	WorktreePath   string                         `json:"worktree_path"`
	Branch         string                         `json:"branch"`
	ReusedWorktree bool                           `json:"reused_worktree"`
	Tasks          map[string]*contract.TaskState `json:"tasks,omitempty"`
}

// Create establishes the session: state entry + working directory + session-
// scoped tasks.
//
// Dispatch order for the resource identifier (any string):
//
//  1. Resolver dispatch — the workflow whose `[resolver]` matches (or the
//     --workflow flag's resolver) derives the session id purely; the
//     provider's setup then acquires the workdir. The core performs no
//     version-control or provider-API work of its own.
//  2. Identity — an identifier no resolver matches, with an explicit
//     --workflow whose provider declares setup: the input string IS the
//     session id.
//
// Create is idempotent: re-running it against an existing session reuses
// the worktree + state entry and retries the workflow setup and any
// session-scoped tasks that have not yet reached "produced". This is the
// recovery path for a previous create that partially failed.
// putBestEffort persists session after a step whose own error already takes
// priority in the caller's return path (e.g. mid-lifecycle checkpoints, or
// cleanup that reports via CleanupWarnings/Force). A failure here must not
// override that primary error, but must not be invisible either.
func putBestEffort(store *state.Store, session *domain.Session, context string) {
	if err := store.Put(session); err != nil {
		slog.Warn("best-effort session state persist failed", "session", session.Name, "context", context, "error", err)
	}
}

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

	// 1. Resolver dispatch.
	disp, matched, dispErr := dispatchResource(cfg, params.Workflow, resource)
	if dispErr != nil {
		if svcErr, ok := dispErr.(*Error); ok {
			return nil, svcErr
		}
		return nil, &Error{Code: ErrExecutionFailed, Message: dispErr.Error()}
	}
	if matched {
		tag, tagErr := effectiveTag(params.Tag, disp.Workflow.ID)
		if tagErr != nil {
			return nil, tagErr
		}
		sessionName := disp.Name + "+" + tag
		return createWithWorkflowSetup(cfg, store, params, disp.Workflow, disp.Provider, sessionName, resource, params.URL)
	}

	// 2. Identity: the resource identifier IS the session id. Requires an
	// explicit workflow, since an identity dispatch on arbitrary input would
	// otherwise shadow every resolver.
	if params.Workflow == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no workflow resolver matches %q; pass --workflow to create a session with this identifier", resource)}
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", err)}
	}
	wf, ok := workflows[params.Workflow]
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q not found; add .sennit/workflows/%s.toml", params.Workflow, params.Workflow)}
	}
	providers, err := cfg.LoadProviders()
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load providers: %v", err)}
	}
	prov, ok, provErr := providerFor(wf, providers)
	if provErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: provErr.Error()}
	}
	if !ok {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q declares no provider; a session needs one to acquire its working directory", wf.ID)}
	}
	tag, tagErr := effectiveTag(params.Tag, wf.ID)
	if tagErr != nil {
		return nil, tagErr
	}
	sessionName := resource + "+" + tag
	return createWithWorkflowSetup(cfg, store, params, wf, prov, sessionName, resource, params.URL)
}
