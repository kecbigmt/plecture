package service

import (
	"fmt"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/dispatch"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	gh "github.com/kecbigmt/plect/app/internal/github"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

// setupWorkflowFor decides whether a GitHub-URL create (no resolver matched)
// should still go through the workflow setup path, returning the resolved
// workflow when so. This is the bridge for workflows that declare `setup`
// but no `[resolver]`: session naming stays the legacy owner/repo-<number>
// derivation while workdir acquisition already runs through the hook.
//
// The decision must happen before any working directory exists, so only the
// trusted base layers (plugin + global) are consulted — the ancestor overlay
// chain is anchored at the workdir, which doesn't exist yet. Selection:
//
//  1. an existing session's frozen workflow wins
//  2. then the --workflow flag
//  3. then auto-select when the trusted cascade has exactly one workflow
//
// Any failure to resolve (unknown name, ambiguity, no setup declared) falls
// back to the legacy path, which re-runs the old selection logic against the
// post-Add worktree and surfaces its own errors.
func setupWorkflowFor(cfg *config.Config, store *state.Store, params CreateParams, parsed *gh.ParsedURL) (config.WorkflowFile, config.ProviderConfig, bool) {
	name := params.Workflow
	sessionName := gh.SessionName(parsed.OwnerRepo, parsed.Number)
	if params.Tag != "" {
		sessionName = gh.SessionNameWithTag(parsed.OwnerRepo, parsed.Number, params.Tag)
	}
	if existing := store.Get(sessionName); existing != nil && existing.Workflow != "" {
		name = existing.Workflow
	}
	workflows, err := cfg.LoadWorkflows("")
	if err != nil {
		return config.WorkflowFile{}, config.ProviderConfig{}, false
	}
	if name == "" {
		if len(workflows) != 1 {
			return config.WorkflowFile{}, config.ProviderConfig{}, false
		}
		for n := range workflows {
			name = n
		}
	}
	wf, ok := workflows[name]
	if !ok || wf.Provider == "" {
		return config.WorkflowFile{}, config.ProviderConfig{}, false
	}
	providers, err := cfg.LoadProviders()
	if err != nil {
		return config.WorkflowFile{}, config.ProviderConfig{}, false
	}
	prov, ok, provErr := providerFor(wf, providers)
	if provErr != nil || !ok {
		return config.WorkflowFile{}, config.ProviderConfig{}, false
	}
	return wf, prov, true
}

// createWithWorkflowSetup is the workflow-setup create path:
//
//	state entry → workflow setup (acquires workdir) → cascade resolution
//	from workdir → task DAG compile → session-scoped tasks
//
// sessionName is the final id (tag already applied); resource is the
// canonical resource identifier; alias is the user's original input.
//
// The state entry is recorded before setup runs so a failed setup leaves an
// inspectable session (with the @workflow pseudo-node marked failed) that a
// later create retries and a non-force destroy can immediately release.
func createWithWorkflowSetup(cfg *config.Config, store *state.Store, params CreateParams, wf config.WorkflowFile, prov config.ProviderConfig, sessionName, resource, alias string) (*CreateResult, error) {
	if err := validateSessionName(sessionName); err != nil {
		return nil, err
	}
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}

	// Legacy compat fields (URL/URLType/OwnerRepo/Number) stay populated for
	// GitHub-shaped resources so existing consumers (ghcache shim,
	// web UI) keep working; non-GitHub resources leave them at their zero
	// values and consumers fall back to ResourceID.
	var parsed *gh.ParsedURL
	if p, err := gh.ParseURL(resource); err == nil {
		parsed = p
	}

	now := time.Now()
	var session *domain.Session
	if existing := store.Get(sessionName); existing != nil {
		if params.Inputs != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: inputsOnExistingSessionMessage()}
		}
		if params.Workflow != "" && params.Workflow != existing.Workflow {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("--workflow %q does not match the session's frozen workflow %q; destroy and recreate to switch", params.Workflow, existing.Workflow)}
		}
		if existing.Workflow != "" && existing.Workflow != wf.ID {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("resource dispatches to workflow %q but session %q is frozen to %q; destroy and recreate to switch", wf.ID, sessionName, existing.Workflow)}
		}
		session = existing
		if session.Tasks == nil {
			session.Tasks = make(map[string]*contract.TaskState)
		}
	} else {
		parentSession, parentErr := resolveParentSession(store, sessionName, params.ParentSession)
		if parentErr != nil {
			return nil, parentErr
		}
		// Session inputs are validated against the trusted-layer schema;
		// workdir overlays can add nodes but not tighten the input contract
		// retroactively (the workdir doesn't exist at validation time).
		input, validateErr := resolveSessionInputs(cfg, "", wf.ID, params.Inputs)
		if validateErr != nil {
			return nil, validateErr
		}
		session = &domain.Session{
			Name:          sessionName,
			ParentSession: parentSession,
			Workflow:      wf.ID,
			Inputs:        input,
			Tasks:         make(map[string]*contract.TaskState),
			CreatedAt:     now,
		}
		// Seed the dispatcher's read cursor at this fresh session's empty log tail
		// so the initial task instruction, appended below during create, is
		// delivered. The dispatcher only starts once the run scope comes up (after
		// create returns), by which point its own first-start seed would land past
		// the instruction and drop it.
		dispatch.SeedCursor(eventlog.NewStore(store.Dir()), sessionName)
	}
	session.ResourceID = resource
	session.Alias = alias
	// Stream is the session's identity: set once from --stream (or inherited
	// TWS_STREAM_ID), never overwritten on retry. A provider may instead supply
	// it via setup output, adopted below — but that lands AFTER provider
	// setup, so a provider that both emits stream_id and self-subscribes with
	// {{.StreamID}} at setup would subscribe streamless (none does today).
	if session.StreamID == "" {
		if sid := resolveStreamID(params.StreamID); sid != "" {
			session.StreamID = sid
		}
	}
	if parsed != nil {
		session.URL = parsed.URL()
		session.URLType = string(parsed.Type)
		session.OwnerRepo = parsed.OwnerRepo
		session.Number = parsed.Number
	} else if session.URL == "" {
		// Display compat: surfaces (ls/show/web UI) render URL today.
		session.URL = resource
	}
	session.UpdatedAt = now

	// Record the session before setup so partial failures stay visible.
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}

	reused := false
	if st, ok := session.Tasks[contract.WorkflowPseudoNodeID]; ok && st != nil && st.Status == contract.TaskStatusProduced {
		reused = true
	}

	vars := task.WorkflowHookVars{
		ResourceID:    resource,
		SessionName:   sessionName,
		SessionInputs: session.Inputs,
	}
	outputs, setupErr := task.RunWorkflowSetup(prov, vars, session.Tasks, params.Observer)
	session.UpdatedAt = time.Now()
	if outputs != nil {
		if workdir, ok := outputs[contract.OutputKeyWorkdir].(string); ok {
			// Mirror into the legacy field so every existing consumer
			// (cd/attach/ls/web UI/hooks) keeps working unchanged.
			session.WorktreePath = workdir
		}
		if branch, ok := outputs["branch"].(string); ok && branch != "" {
			session.Branch = branch
		}
		// Adopt a provider-generated stream as the session's own when none was
		// set explicitly (no --stream, no inherited TWS_STREAM_ID). This makes
		// session.StreamID the single source of the stream for the orchestrator
		// route too — the provider emits stream_id from setup, core stamps it
		// here, and the claude task exports {{.StreamID}}. Write-once:
		// an existing StreamID (flag/env/prior create) is never overwritten, so
		// dispatched children keep inheriting their parent's stream.
		if session.StreamID == "" {
			if sid, ok := outputs["stream_id"].(string); ok && sid != "" {
				session.StreamID = sid
			}
		}
	}
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if setupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}

	// Environment lifecycle: after provider setup (the worktree exists), before
	// session task setup. A no-op when the workflow declares no environment.
	// Fail-closed like provider setup — an environment setup failure must not
	// let task setup start.
	envSetupErr := runEnvironmentSetupForSession(cfg, wf, session, params.Observer)
	session.UpdatedAt = time.Now()
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if envSetupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envSetupErr.Error()}
	}

	// The workdir now exists: resolve the full cascade (incl. overlays above
	// and the node-only layer inside the workdir) and run session tasks.
	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	envExecutor, envExecErr := environmentExecutorForSession(cfg, wf, session)
	if envExecErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envExecErr.Error()}
	}
	tasksErr := task.RunSetup(plan.Session, sessionVars(session), session.Tasks, params.Observer, envExecutor)
	session.UpdatedAt = time.Now()
	// A session node (the initial_task dispatcher) can shell out to a nested
	// `tws task setup` subprocess that writes its instance straight to disk.
	// Overlay our in-memory task entries onto the freshly-read session instead
	// of a blind Put so that nested write (e.g. the `initial` task) survives.
	if err := mergeTasks(store, sessionName, session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if tasksErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: tasksErr.Error()}
	}
	if refreshed := store.Get(sessionName); refreshed != nil {
		session = refreshed
	}

	// Record lifecycle.created on the first successful create (idempotent across
	// retries of a partial failure and re-runs of an already-created session).
	recordSessionCreated(store, sessionName)

	return &CreateResult{
		SessionName:    sessionName,
		WorktreePath:   session.WorktreePath,
		Branch:         session.Branch,
		ReusedWorktree: reused,
		Tasks:          session.Tasks,
	}, nil
}
