package service

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/eventlog"
	gh "github.com/kecbigmt/plect/app/internal/github"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	"github.com/kecbigmt/plect/app/internal/workspace"
	contract "github.com/kecbigmt/plect/contracts/state"
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
	StreamID      string         // opaque work-stream id (B-2); empty falls back to TWS_STREAM_ID. Stamps the session and its events.
	ParentSession string         // parent session name; empty falls back to TWS_SESSION_NAME when it exists and is not self.
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
//     --workflow flag's resolver) derives the session id purely; workflow
//     setup then acquires the workdir. The core performs no git/gh work.
//  2. GitHub-URL bridge — URL/PVTI inputs with no matching resolver keep the
//     legacy owner/repo-<number> naming; workdir acquisition still goes
//     through workflow setup when the workflow declares one.
//  3. Identity — a non-URL input with an explicit --workflow whose workflow
//     declares setup: the input string IS the session id.
//  4. Legacy core path — the core resolves the branch (gh) and creates the
//     worktree itself, exactly as before. Kept until every shipped workflow
//     declares setup.
//
// Create is idempotent: re-running it against an existing session reuses
// the worktree + state entry and retries the workflow setup and any
// session-scoped tasks that have not yet reached "produced". This is the
// recovery path for a previous create that partially failed.
func Create(cfg *config.Config, store *state.Store, params CreateParams) (*CreateResult, error) {
	resource := params.URL
	// PVTI project-item ids need a network resolve to a canonical URL before
	// any pure dispatch can happen. GitHub-specific; rides the legacy bridge.
	if gh.IsProjectItemID(resource) {
		parsed, err := resolveURL(resource)
		if err != nil {
			return nil, err
		}
		resource = parsed.URL()
	}

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

	// 3. Identity (non-URL input, explicit workflow with setup). Checked
	// before the GitHub bridge because a non-URL input can't parse anyway.
	if !gh.IsURL(resource) {
		if params.Workflow == "" {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("no workflow resolver matches %q; pass --workflow to create a session with this identifier", resource)}
		}
		workflows, err := cfg.LoadWorkflows("")
		if err != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", err)}
		}
		wf, ok := workflows[params.Workflow]
		if !ok {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q not found; add .tws/workflows/%s.toml", params.Workflow, params.Workflow)}
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
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q declares no provider; only the GitHub URL path can create sessions without one", wf.ID)}
		}
		tag, tagErr := effectiveTag(params.Tag, wf.ID)
		if tagErr != nil {
			return nil, tagErr
		}
		sessionName := resource + "+" + tag
		return createWithWorkflowSetup(cfg, store, params, wf, prov, sessionName, resource, params.URL)
	}

	// 2./4. GitHub-URL bridge and legacy core path.
	parsed, err := resolveURL(resource)
	if err != nil {
		return nil, err
	}

	if !cfg.IsRepoAllowed(parsed.OwnerRepo) {
		return nil, &Error{Code: ErrRepoNotAllowed, Message: fmt.Sprintf("repository %s is not in the allowlist", parsed.OwnerRepo)}
	}

	if wf, prov, ok := setupWorkflowFor(cfg, store, params, parsed); ok {
		sessionName := gh.SessionName(parsed.OwnerRepo, parsed.Number)
		if params.Tag != "" {
			sessionName = gh.SessionNameWithTag(parsed.OwnerRepo, parsed.Number, params.Tag)
		}
		return createWithWorkflowSetup(cfg, store, params, wf, prov, sessionName, parsed.URL(), params.URL)
	}

	mgr := workspace.NewManager(cfg.WorktreesRoot)
	baseBranch, err := mgr.ResolveBranch(parsed)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to resolve branch: %v", err)}
	}

	sessionName := gh.SessionName(parsed.OwnerRepo, parsed.Number)
	branch := baseBranch
	if params.Tag != "" {
		if err := validateTagFormat(params.Tag); err != nil {
			return nil, err
		}
		sessionName = gh.SessionNameWithTag(parsed.OwnerRepo, parsed.Number, params.Tag)
		branch = gh.BranchWithTag(baseBranch, params.Tag)
	}

	// Session-name guard (the provider paths apply it inside
	// createWithWorkflowSetup; the legacy path has no setup chokepoint, so
	// guard here before any worktree side task).
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}

	// mgr.Add is itself idempotent (reuses an existing worktree) and the
	// session lookup below decides whether we're creating a fresh state
	// entry or recovering an existing one.
	info, err := mgr.Add(workspace.AddParams{
		Parsed:      parsed,
		Branch:      branch,
		BaseBranch:  baseBranch,
		SessionName: sessionName,
	})
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to create worktree: %v", err)}
	}

	now := time.Now()
	// Resolved via mgr.Add so cascade lookup works before the session struct
	// exists; we can't read session.WorktreePath yet in the create-new branch.
	worktreeDir := info.WorktreePath
	var session *domain.Session
	if existing := store.Get(sessionName); existing != nil {
		if params.Inputs != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: inputsOnExistingSessionMessage()}
		}
		if params.Workflow != "" && params.Workflow != existing.Workflow {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("--workflow %q does not match the session's frozen workflow %q; destroy and recreate to switch", params.Workflow, existing.Workflow)}
		}
		session = existing
		if session.Tasks == nil {
			session.Tasks = make(map[string]*contract.TaskState)
		}
		// Refresh fields that may have drifted; the URL/sessionName
		// pair is stable but worktree path could move across migrations.
		session.URL = parsed.URL()
		session.URLType = string(parsed.Type)
		session.OwnerRepo = parsed.OwnerRepo
		session.Number = parsed.Number
		session.Branch = branch
		session.WorktreePath = info.WorktreePath
	} else {
		parentSession, parentErr := resolveParentSession(store, info.SessionName, params.ParentSession)
		if parentErr != nil {
			return nil, parentErr
		}
		workflowName, selectErr := selectWorkflow(cfg, worktreeDir, params.Workflow)
		if selectErr != nil {
			return nil, selectErr
		}
		input, validateErr := resolveSessionInputs(cfg, worktreeDir, workflowName, params.Inputs)
		if validateErr != nil {
			return nil, validateErr
		}
		session = &domain.Session{
			Name:          info.SessionName,
			ParentSession: parentSession,
			URL:           parsed.URL(),
			URLType:       string(parsed.Type),
			OwnerRepo:     parsed.OwnerRepo,
			Number:        parsed.Number,
			Branch:        branch,
			WorktreePath:  info.WorktreePath,
			Workflow:      workflowName,
			Inputs:        input,
			Tasks:         make(map[string]*contract.TaskState),
			CreatedAt:     now,
		}
	}
	// State v3 identity fields — mirror the setup path so legacy-path
	// sessions satisfy the same contract without waiting for a load-time
	// migration (which only backfills, and only on the next reload).
	session.ResourceID = parsed.URL()
	session.Alias = params.URL
	// Stream is the session's identity in the cross-session view: set once when
	// the session first gets one, never overwritten on an idempotent retry
	// (changing it would split the session's later events into another stream).
	if session.StreamID == "" {
		if sid := resolveStreamID(params.StreamID); sid != "" {
			session.StreamID = sid
		}
	}
	session.UpdatedAt = now

	wf, wfErr := loadSessionWorkflow(cfg, worktreeDir, session)
	if wfErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}

	// Environment lifecycle: after the worktree exists (mgr.Add already ran
	// above), before session task setup. A no-op when the workflow declares
	// no environment. Fail-closed like the workflow-setup path — an
	// environment setup failure must not let task setup start.
	if envSetupErr := runEnvironmentSetupForSession(cfg, wf, session, params.Observer); envSetupErr != nil {
		session.UpdatedAt = time.Now()
		_ = store.Put(session)
		return nil, &Error{Code: ErrExecutionFailed, Message: envSetupErr.Error()}
	}

	plan, err := buildPlanForSession(cfg, worktreeDir, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	envExecutor, envExecErr := environmentExecutorForSession(cfg, wf, session)
	if envExecErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envExecErr.Error()}
	}

	setupErr := task.RunSetup(plan.Session, sessionVars(session), session.Tasks, params.Observer, envExecutor)
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if setupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}
	if refreshed := store.Get(info.SessionName); refreshed != nil {
		session = refreshed
	}

	recordSessionCreated(store, info.SessionName)

	return &CreateResult{
		SessionName:    info.SessionName,
		WorktreePath:   info.WorktreePath,
		Branch:         branch,
		ReusedWorktree: info.ReusedWorktree,
		Tasks:          session.Tasks,
	}, nil
}

// UpParams holds parameters for Up.
type UpParams struct {
	Identifier string // URL or session name
	// Tag derives a tagged session name from a URL identifier. Only valid
	// when Identifier is a URL; combining Tag with a bare session name
	// returns ErrInvalidTag so misuse surfaces immediately rather than
	// silently ignoring the flag.
	Tag           string
	Workflow      string         // forwarded to auto-create; rejected when state already exists
	Inputs        map[string]any // forwarded to auto-create; rejected when state already exists
	StreamID      string         // opaque work-stream id (B-2); empty falls back to TWS_STREAM_ID. Applies on the auto-create path only — stream is a create-time identity, so up never changes an existing session's stream.
	ParentSession string         // forwarded to auto-create; empty falls back to TWS_SESSION_NAME when it exists and is not self.
	Observer      task.Observer
}

// UpResult holds the outcome of Up.
type UpResult struct {
	SessionName string                         `json:"session_name"`
	Tasks       map[string]*contract.TaskState `json:"tasks,omitempty"`
}

// Up runs run-scoped tasks for the given session.
//
// docker compose up-style auto-create: if the identifier is a URL, Create
// is invoked internally before run-scoped setup whenever the state entry
// is absent OR any declared session-scoped task has not reached
// "produced". Since Create is idempotent (already-produced session
// tasks are skipped), this also recovers partial-create state without
// the user having to remember to re-run `tws create`. A bare session name
// without a state entry still errors out — Create needs URL information
// to resolve owner/repo/branch, so the asymmetry is intentional.
func Up(cfg *config.Config, store *state.Store, params UpParams) (*UpResult, error) {
	identifier := params.Identifier
	disp, matched, dispErr := dispatchResource(cfg, params.Workflow, params.Identifier)
	if dispErr != nil {
		// Ambiguous resolver match / invalid resolver / explicit --workflow
		// mismatch must fail here exactly as Create would — falling through
		// to the legacy path would let `tws up` silently disagree with
		// `tws create` for the same resource.
		if svcErr, ok := dispErr.(*Error); ok {
			return nil, svcErr
		}
		return nil, &Error{Code: ErrExecutionFailed, Message: dispErr.Error()}
	}
	if matched {
		// Resolver dispatch: the identifier is a resource id. Mirror the URL
		// auto-create semantics below with the resolver-derived session name.
		// The effective tag (explicit --tag or the workflow-id default) is
		// resolved here and forwarded to Create so up and create converge on
		// the same tagged session name.
		tag, tagErr := effectiveTag(params.Tag, disp.Workflow.ID)
		if tagErr != nil {
			return nil, tagErr
		}
		sessionName := disp.Name + "+" + tag
		existing := store.Get(sessionName)
		if params.Inputs != nil && existing != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: inputsOnExistingSessionMessage()}
		}
		if params.Workflow != "" && existing != nil && params.Workflow != existing.Workflow {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("--workflow %q does not match the session's frozen workflow %q", params.Workflow, existing.Workflow)}
		}
		if existing == nil || hasIncompleteSessionTask(cfg, existing) {
			if _, err := Create(cfg, store, CreateParams{
				URL:           params.Identifier,
				Tag:           tag,
				Workflow:      params.Workflow,
				Inputs:        params.Inputs,
				StreamID:      params.StreamID,
				ParentSession: params.ParentSession,
				Observer:      params.Observer,
			}); err != nil {
				return nil, err
			}
		}
		identifier = sessionName
	} else if gh.IsURL(params.Identifier) {
		parsed, parseErr := gh.ParseURL(params.Identifier)
		if parseErr != nil {
			return nil, &Error{Code: ErrInvalidURL, Message: parseErr.Error()}
		}
		// SessionName is derived from owner/repo+number alone (no network
		// call), so we can decide whether to invoke Create without
		// triggering its branch-resolution first.
		sessionName := gh.SessionName(parsed.OwnerRepo, parsed.Number)
		if params.Tag != "" {
			if err := validateTagFormat(params.Tag); err != nil {
				return nil, err
			}
			sessionName = gh.SessionNameWithTag(parsed.OwnerRepo, parsed.Number, params.Tag)
		}
		existing := store.Get(sessionName)
		if params.Inputs != nil && existing != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: inputsOnExistingSessionMessage()}
		}
		if params.Workflow != "" && existing != nil && params.Workflow != existing.Workflow {
			return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("--workflow %q does not match the session's frozen workflow %q", params.Workflow, existing.Workflow)}
		}
		if existing == nil || hasIncompleteSessionTask(cfg, existing) {
			if _, err := Create(cfg, store, CreateParams{
				URL:           params.Identifier,
				Tag:           params.Tag,
				Workflow:      params.Workflow,
				Inputs:        params.Inputs,
				StreamID:      params.StreamID,
				ParentSession: params.ParentSession,
				Observer:      params.Observer,
			}); err != nil {
				return nil, err
			}
		}
		// Use the derived session name for resolveSession below — the URL
		// alone doesn't disambiguate tag variants.
		identifier = sessionName
	} else {
		if params.Tag != "" {
			return nil, &Error{Code: ErrInvalidTag, Message: "--tag is only valid when the identifier is a URL; the session name already encodes the tag"}
		}
		if params.Inputs != nil {
			return nil, &Error{Code: ErrInvalidInput, Message: "--input is only valid when the identifier is a URL (auto-create path); bare session name implies an existing session"}
		}
	}

	sessionName, session, err := resolveSession(cfg, store, identifier)
	if err != nil {
		return nil, err
	}
	// Bringing up an existing session runs run-scoped tasks against it; clamp
	// it to the active guard. The auto-create paths above already guard
	// via Create — this catches `tws up <bare-existing-session>`, which skips it.
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState)
	}

	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	wf, wfErr := loadSessionWorkflow(cfg, session.WorktreePath, session)
	if wfErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}
	envExecutor, envErr := environmentExecutorForSession(cfg, wf, session)
	if envErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envErr.Error()}
	}

	// Stream is a create-time identity, not set here: up runs only run-scoped
	// tasks, but the watcher learns the stream through the session-scoped
	// github_watch task (which subscribes with --stream {{.StreamID}}). That
	// task already produced at create, so backfilling the stream onto an
	// existing session here would update state but leave the watcher publishing
	// streamless github.* events — invisible to the cross-session view. The
	// --stream flag still applies on up's auto-create path, where github_watch
	// runs with the stream in hand.
	//
	// Up does not re-run environment setup — @environment is session-scoped
	// (like @workflow) and already produced during Create; environmentExecutorForSession
	// just reads its persisted outputs.
	setupErr := task.RunSetup(plan.Run, sessionVars(session), session.Tasks, params.Observer, envExecutor)
	session.UpdatedAt = time.Now()
	// A run-scope node's setup script can itself shell out to a nested `tws
	// task setup` against this same session (e.g. goal_bootstrap re-deriving
	// pursue_goal instances, config/tws/tasks/goal_bootstrap.toml) while this
	// call's own RunSetup is still in flight. That nested call persists its
	// instance straight to disk under its own store.Update. A blind Put here
	// would then overwrite disk with our in-memory map, taken before the
	// nested write landed, silently dropping it — the same hazard mergeTasks
	// already exists to close for Create's initial_task dispatcher.
	if err := mergeTasks(store, sessionName, session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if setupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: setupErr.Error()}
	}
	// Reflect nested-written keys (merged onto disk above, not our in-memory
	// map) in the result the same way Create does.
	if refreshed := store.Get(sessionName); refreshed != nil {
		session = refreshed
	}
	recordLifecycle(store, sessionName, "up", "run-scoped tasks produced")
	return &UpResult{SessionName: sessionName, Tasks: session.Tasks}, nil
}

// checkLifecycleRelationGuard authorizes a destructive lifecycle operation
// (down/destroy) by tree relation: only the target session itself or one of
// its descendants (its own dispatched subtree) may act on it. The caller
// identity is the ambient TWS_SESSION_NAME; its absence means a human is
// running the raw CLI outside any session pane, which stays exempt so manual
// recovery is never blocked by this guard.
func checkLifecycleRelationGuard(store *state.Store, targetName, op string) *Error {
	caller := os.Getenv("TWS_SESSION_NAME")
	if caller == "" {
		return nil
	}
	switch rel := domain.RelationFromTarget(store.All(), caller, targetName); rel {
	case domain.RelationSelf, domain.RelationChild, domain.RelationDescendant:
		return nil
	default:
		return &Error{
			Code: ErrRelationNotAllowed,
			Message: fmt.Sprintf(
				"session %q cannot %s session %q: relation is %q, not self or a descendant",
				caller, op, targetName, rel,
			),
		}
	}
}

// DownParams holds parameters for Down.
type DownParams struct {
	Identifier string
	Observer   task.Observer
}

// DownResult holds the outcome of Down.
type DownResult struct {
	SessionName string                         `json:"session_name"`
	Tasks       map[string]*contract.TaskState `json:"tasks,omitempty"`
}

// Down runs run-scoped cleanup (in reverse order) for the given session.
func Down(cfg *config.Config, store *state.Store, params DownParams) (*DownResult, error) {
	sessionName, session, err := resolveSession(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}
	// Running cleanup against an existing session mutates it; clamp it to the
	// active guard like the other write paths.
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}
	if guardErr := checkLifecycleRelationGuard(store, sessionName, "down"); guardErr != nil {
		return nil, guardErr
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState)
	}

	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	// ADR-003: a single reverse-instantiation teardown over the run-scoped
	// tasks — static run nodes and run-scoped dynamic instances merged into
	// one seq-descending pass, so a static node instantiated after a dynamic
	// one is still cleaned ahead of it.
	teardown, teardownErr := unifiedTeardownList(cfg, session, plan, true)
	if teardownErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: teardownErr.Error()}
	}
	wf, wfErr := loadSessionWorkflow(cfg, session.WorktreePath, session)
	if wfErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}
	envExecutor, envErr := environmentExecutorForSession(cfg, wf, session)
	if envErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envErr.Error()}
	}
	// Down never touches @environment itself (only Destroy does) — the
	// environment stays alive across down/up, same as @workflow.
	cleanupErr := task.RunCleanup(teardown, sessionVars(session), session.Tasks, params.Observer, envExecutor)
	session.UpdatedAt = time.Now()
	if err := store.Put(session); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to save session state: %v", err)}
	}
	if cleanupErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: cleanupErr.Error()}
	}
	recordLifecycle(store, sessionName, "down", "run-scoped tasks cleaned")
	return &DownResult{SessionName: sessionName, Tasks: session.Tasks}, nil
}

// DestroyParams holds parameters for Destroy.
type DestroyParams struct {
	Identifier   string
	Force        bool
	DeleteBranch bool
	Observer     task.Observer
}

// DestroyResult holds the outcome of Destroy.
type DestroyResult struct {
	SessionName     string `json:"session_name"`
	RemovedWorktree bool   `json:"removed_worktree"`
	WorktreeWarning string `json:"worktree_warning,omitempty"`
	// CleanupWarnings carries task cleanup errors that were downgraded to
	// warnings by --force. Without --force a cleanup error aborts Destroy and
	// returns the error directly; this field is only populated when the user
	// explicitly opted into best-effort teardown.
	CleanupWarnings []string `json:"cleanup_warnings,omitempty"`
}

// Destroy is the task-aware teardown path. fail-fast by default so a
// cleanup error leaves the partial state inspectable for retry; --force
// demotes cleanup errors to warnings so a stuck session can be freed
// without manual cleanup. State is persisted before each subsequent step
// so a mid-teardown crash stays inspectable. State-delete failures error
// even under --force — silent partial teardown would be worse than a
// noisy one.
func Destroy(cfg *config.Config, store *state.Store, params DestroyParams) (*DestroyResult, error) {
	sessionName, session, err := resolveSession(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}
	// Tearing down an existing session is a per-session write; clamp it to the
	// active guard so a guarded orchestrator can't destroy another owner's
	// session it can see via `tws ls`. Create guards on the way in; this
	// closes the symmetric teardown vector.
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}
	if guardErr := checkLifecycleRelationGuard(store, sessionName, "destroy"); guardErr != nil {
		return nil, guardErr
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState)
	}

	result := &DestroyResult{SessionName: sessionName}

	// Fail-closed before any teardown side effect: store.Delete unconditionally
	// clears ParentSession on every child, and tws up never re-adopts an
	// orphan, so a silent destroy permanently severs the tree. --force makes
	// that orphaning an explicit, reported choice instead.
	if children := childNames(store.All(), sessionName); len(children) > 0 {
		if !params.Force {
			return nil, &Error{
				Code: ErrHasChildren,
				Message: fmt.Sprintf(
					"session %s has %d child session(s) that would be orphaned: %s\nUse `tws down %s` + `tws up %s` to reset without orphaning them, or re-run with `tws destroy %s --force` to destroy and orphan them.",
					sessionName, len(children), strings.Join(children, ", "), sessionName, sessionName, sessionName,
				),
			}
		}
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("orphaned %d child session(s): %s", len(children), strings.Join(children, ", ")))
	}

	mgr := workspace.NewManager(cfg.WorktreesRoot)
	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	// ADR-003: a single reverse-instantiation teardown over every non-@workflow
	// task — static plan nodes (run + session) and dynamic instances merged
	// into one seq-descending pass. This is strictly the reverse of the
	// instantiation stack, so a static node instantiated after a dynamic one is
	// still cleaned first regardless of scope. @workflow (workdir) is released
	// last, below.
	teardown, teardownErr := unifiedTeardownList(cfg, session, plan, false)
	if teardownErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: teardownErr.Error()}
	}
	wf, wfErr := loadSessionWorkflow(cfg, session.WorktreePath, session)
	if wfErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: wfErr.Error()}
	}
	envExecutor, envErr := environmentExecutorForSession(cfg, wf, session)
	if envErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: envErr.Error()}
	}
	if cleanupErr := task.RunCleanup(teardown, sessionVars(session), session.Tasks, params.Observer, envExecutor); cleanupErr != nil {
		session.UpdatedAt = time.Now()
		_ = store.Put(session)
		if !params.Force {
			return nil, &Error{Code: ErrExecutionFailed, Message: cleanupErr.Error()}
		}
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("cleanup: %v", cleanupErr))
	}

	// Persist any TaskState changes (status flips to cleaned) before we
	// delete the entry, in case worktree removal fails and the user wants
	// to inspect state.json post hoc.
	session.UpdatedAt = time.Now()
	_ = store.Put(session)

	// Environment cleanup: after run+session task cleanup, before provider
	// cleanup (tasks -> environment -> provider). A no-op when the workflow
	// declares no environment, or its setup never ran.
	if envCleanupErr := runEnvironmentCleanupForSession(cfg, wf, session, params.Observer); envCleanupErr != nil {
		session.UpdatedAt = time.Now()
		_ = store.Put(session)
		if !params.Force {
			return nil, &Error{Code: ErrExecutionFailed, Message: envCleanupErr.Error()}
		}
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("environment cleanup: %v", envCleanupErr))
	}

	if wfState, ok := session.Tasks[contract.WorkflowPseudoNodeID]; ok && wfState != nil {
		// Workflow setup acquired the working directory, so workflow cleanup
		// owns its release — the core performs no worktree removal here.
		// (Whether the workdir is actually deleted is the cleanup script's
		// decision; setup/cleanup symmetry is the author's contract.)
		cleanupErr := runWorkflowCleanupForDestroy(cfg, session, params.Observer)
		session.UpdatedAt = time.Now()
		_ = store.Put(session)
		if cleanupErr != nil {
			if !params.Force {
				return nil, &Error{
					Code:    ErrExecutionFailed,
					Message: fmt.Sprintf("%v (session %s)\nRe-run with `tws destroy %s --force` to delete the state entry anyway.", cleanupErr, sessionName, sessionName),
				}
			}
			result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("workflow cleanup: %v", cleanupErr))
		}
		result.RemovedWorktree = session.WorktreePath != "" && !fileExists(session.WorktreePath)
	} else if session.WorktreePath != "" {
		repoDir := mgr.RepoDir(session.OwnerRepo)
		gitDir, findErr := mgr.FindGitDir(repoDir, session.WorktreePath)
		if findErr != nil {
			result.WorktreeWarning = fmt.Sprintf("worktree removal failed: %v", findErr)
		} else if err := mgr.RemoveByPath(session.WorktreePath, gitDir, session.Branch, params.Force, params.DeleteBranch); err != nil {
			result.WorktreeWarning = fmt.Sprintf("worktree removal failed: %v", err)
		} else {
			result.RemovedWorktree = true
		}
	}

	// Without --force, abort before store.Delete so the user can retry —
	// otherwise the worktree is orphaned on disk while tws forgets about it.
	if result.WorktreeWarning != "" && !params.Force {
		return nil, &Error{
			Code:    ErrExecutionFailed,
			Message: fmt.Sprintf("%s (session %s)\nRe-run with `tws destroy %s --force` to delete the worktree and state entry anyway.", result.WorktreeWarning, sessionName, sessionName),
		}
	}

	// Snapshot the state entry as a tombstone in the event log directory
	// before it's deleted, so resource mapping / judge records / final
	// outputs survive destroy. Fail-closed and unconditional on --force:
	// a lost tombstone is exactly the silent context loss this exists to prevent.
	destroyedAt := time.Now()
	tombstone := contract.Tombstone{Session: *session, DestroyedAt: destroyedAt}
	tombstoneData, merr := json.Marshal(tombstone)
	if merr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to marshal tombstone: %v", merr)}
	}
	if werr := eventlog.NewStore(store.Dir()).WriteTombstone(sessionName, tombstoneData); werr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to write tombstone: %v", werr)}
	}

	// Record after the tombstone succeeds; otherwise a failed destroy would
	// leave a lifecycle event claiming the session was destroyed.
	recordLifecycle(store, sessionName, "destroyed", "session destroyed")

	if err := store.Delete(sessionName); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to delete state entry: %v", err)}
	}
	return result, nil
}

// AttachParams holds parameters for Attach.
type AttachParams struct {
	Identifier string
}

// AttachResult is the resolved attach plan. The caller (CLI) is expected to
// hand control to the rendered command via syscall.Exec.
type AttachResult struct {
	SessionName string `json:"session_name"`
	TaskID      string `json:"task_id"`
	Command     string `json:"command"`
}

// Attach resolves the session, locates the task declaring `attach`, and
// renders its template against that task's own outputs. It does not exec
// anything — the CLI handles syscall.Exec so this stays testable.
func Attach(cfg *config.Config, store *state.Store, params AttachParams) (*AttachResult, error) {
	sessionName, session, err := resolveSession(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}

	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	target := plan.AttachTask()
	if target == nil {
		return nil, &Error{Code: ErrNotAttachable, Message: "this workflow has no attach target"}
	}

	// Resolved.Config is empty for nodes synthesized from workflow files (only
	// legacy inline `[[tasks]]` populates it). Reach for NodeID/Attach
	// directly so the workflow path renders the right command instead of
	// looking up `session.Tasks[""]`.
	st, ok := session.Tasks[target.NodeID]
	if !ok || st == nil || st.Status != contract.TaskStatusProduced {
		return nil, &Error{
			Code:    ErrNotProduced,
			Message: fmt.Sprintf("task %q is not produced; run 'tws up %s' first", target.NodeID, sessionName),
		}
	}

	cmdStr, err := task.RenderAttach(target.Attach, st.Outputs, sessionVars(session))
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("attach template: %v", err)}
	}

	return &AttachResult{
		SessionName: sessionName,
		TaskID:      target.NodeID,
		Command:     cmdStr,
	}, nil
}

// resolveURL accepts URLs and PVTI ids, returning the parsed form.
func resolveURL(url string) (*gh.ParsedURL, error) {
	if gh.IsProjectItemID(url) {
		parsed, err := gh.ResolveProjectItemID(url)
		if err != nil {
			return nil, &Error{Code: ErrInvalidURL, Message: err.Error()}
		}
		return parsed, nil
	}
	parsed, err := gh.ParseURL(url)
	if err != nil {
		return nil, &Error{Code: ErrInvalidURL, Message: err.Error()}
	}
	return parsed, nil
}

// buildPlanForSession honors the workflow name frozen on the session so
// up/down/destroy replay against the same plan create saw. A session without a
// frozen workflow is impossible now that sessions always freeze one at create
// time, but we surface it as an error rather than panicking so a stale state
// entry from before workflows existed doesn't crash the binary.
func buildPlanForSession(cfg *config.Config, worktreeDir string, session *domain.Session) (*task.Plan, error) {
	if session == nil || session.Workflow == "" {
		return nil, fmt.Errorf("session has no frozen workflow; destroy and recreate it with --workflow")
	}
	return buildWorkflowPlan(cfg, worktreeDir, session.Workflow)
}

// buildWorkflowPlan loads `.tws/workflows/<name>.toml` (+ referenced task
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
		return nil, fmt.Errorf("workflow %q not found in .tws/workflows or global config", name)
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
//     to freeze, and there is no inline-tasks fallback to fall back to.
//  4. Multiple workflows on disk without a flag is ambiguous — error so the
//     user picks one explicitly.
func selectWorkflow(cfg *config.Config, worktreeDir, flag string) (string, *Error) {
	workflows, err := cfg.LoadWorkflows(worktreeDir)
	if err != nil {
		return "", &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", err)}
	}
	if flag != "" {
		if _, ok := workflows[flag]; !ok {
			return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("workflow %q not found; add .tws/workflows/%s.toml", flag, flag)}
		}
		return flag, nil
	}
	switch len(workflows) {
	case 0:
		return "", &Error{
			Code:    ErrInvalidInput,
			Message: "no workflows found; add .tws/workflows/<name>.toml or pass --workflow",
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

// mergeTasks persists the session by overlaying its in-memory task entries
// onto the freshly-read on-disk session under the state lock, rather than a blind
// Put. A nested `tws task setup` subprocess (the initial_task dispatcher) may
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

// resolveStreamID picks the work-stream id for a create/up: the explicit flag
// wins, else the inherited TWS_STREAM_ID env (how an orchestrator propagates its
// stream into a dispatched `tws up` — see config/tws/tasks/claude.toml). Empty
// means the session belongs to no stream.
func resolveStreamID(flag string) string {
	if flag != "" {
		return flag
	}
	return os.Getenv("TWS_STREAM_ID")
}

func resolveParentSession(store *state.Store, sessionName, explicit string) (string, *Error) {
	candidate := explicit
	explicitSet := candidate != ""
	if candidate == "" {
		candidate = os.Getenv("TWS_SESSION_NAME")
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
		StreamID:      s.StreamID,
		ParentSession: s.ParentSession,
		WorktreePath:  s.WorktreePath,
		URL:           s.URL,
		OwnerRepo:     s.OwnerRepo,
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
func resolveSessionInputs(cfg *config.Config, worktreeDir, workflowName string, raw map[string]any) (map[string]any, *Error) {
	inline, file, sourceID := cfg.InputsSchema, cfg.ResolvedInputsSchemaPath(), "tws:config:inputs"
	if workflowName != "" {
		workflows, err := cfg.LoadWorkflows(worktreeDir)
		if err != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load workflows: %v", err)}
		}
		if wf, ok := workflows[workflowName]; ok {
			// Workflow-level schema wins when present so each workflow can
			// gate its own input shape independently of the global default.
			if len(wf.InputsSchema) > 0 || wf.InputsSchemaFile != "" {
				inline = wf.InputsSchema
				file = wf.ResolvedInputsSchemaPath()
				sourceID = "tws:workflow:" + workflowName + ":inputs"
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
	if workflows, err := cfg.LoadWorkflows(session.WorktreePath); err == nil {
		if wf, ok := workflows[session.Workflow]; ok && wf.Provider != "" {
			st, ok := session.Tasks[contract.WorkflowPseudoNodeID]
			if !ok || st == nil || st.Status != contract.TaskStatusProduced {
				return true
			}
		}
	}
	plan, err := buildPlanForSession(cfg, session.WorktreePath, session)
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

// runWorkflowCleanupForDestroy resolves the session's workflow definition and
// runs its cleanup hook. The definition comes from the trusted layers (the
// workdir layer cannot declare hooks), so resolving against the session's
// worktree path is safe even though that path is clone content.
func runWorkflowCleanupForDestroy(cfg *config.Config, session *domain.Session, observer task.Observer) error {
	workflows, err := cfg.LoadWorkflows(session.WorktreePath)
	if err != nil {
		return fmt.Errorf("load workflows: %w", err)
	}
	wf, ok := workflows[session.Workflow]
	if !ok {
		return fmt.Errorf("workflow %q not found; its cleanup hook cannot run", session.Workflow)
	}
	providers, err := cfg.LoadProviders()
	if err != nil {
		return fmt.Errorf("load providers: %w", err)
	}
	prov, ok, provErr := providerFor(wf, providers)
	if provErr != nil {
		return provErr
	}
	if !ok {
		return fmt.Errorf("workflow %q declares no provider; its cleanup hook cannot run", session.Workflow)
	}
	vars := task.WorkflowHookVars{
		ResourceID:    session.ResourceID,
		SessionName:   session.Name,
		SessionInputs: session.Inputs,
	}
	return task.RunWorkflowCleanup(prov, vars, session.Tasks, observer)
}

// unifiedTeardownList builds the single cleanup-ordered Resolved list for a
// teardown phase (ADR-003): static plan nodes and dynamic instances merged into
// one slice sorted by ascending instantiation Seq. RunCleanup reclaims in
// reverse, so the result is strictly the reverse of the instantiation stack —
// a static node instantiated after a dynamic one (e.g. a re-`up` that re-stamps
// run nodes) is still cleaned first. The @workflow pseudo-node is excluded; it
// is released last via the provider cleanup hook.
//
// runOnly restricts to run-scoped tasks (the `down` lifecycle); destroy
// passes false to reclaim every task regardless of scope. Static nodes are
// enumerated session-then-run so legacy state (Seq all zero) preserves the old
// run-before-session reverse order through the stable sort. A dynamic instance
// whose task definition has since disappeared is reclaimed with an empty
// cleanup (best-effort, mirroring how a missing workflow degrades GC).
func unifiedTeardownList(cfg *config.Config, session *domain.Session, plan *task.Plan, runOnly bool) ([]task.Resolved, error) {
	type seqResolved struct {
		seq int
		r   task.Resolved
	}
	var items []seqResolved
	static := make(map[string]bool)

	appendStatic := func(nodes []task.Resolved) {
		for _, r := range nodes {
			if runOnly && r.Scope != contract.TaskScopeRun {
				continue
			}
			seq := 0
			if st := session.Tasks[r.NodeID]; st != nil {
				seq = st.Seq
			}
			items = append(items, seqResolved{seq: seq, r: r})
			static[r.NodeID] = true
		}
	}
	appendStatic(plan.Session)
	appendStatic(plan.Run)

	defs, err := cfg.LoadTaskDefinitions(session.WorktreePath)
	if err != nil {
		return nil, fmt.Errorf("load task definitions: %w", err)
	}
	// Best-effort: teardown must stay resilient to a workflow config that has
	// since disappeared or broken (see the comment below), so an error here
	// just leaves Execution at its zero value (host) rather than aborting.
	wf, _ := loadSessionWorkflow(cfg, session.WorktreePath, session)
	// Sort dynamic keys for a deterministic input order before the stable sort
	// (map iteration is random; equal-seq legacy entries would otherwise vary).
	dynKeys := make([]string, 0, len(session.Tasks))
	for key, st := range session.Tasks {
		if st == nil || !st.Dynamic || key == contract.WorkflowPseudoNodeID || static[key] {
			continue
		}
		if runOnly && st.Scope != contract.TaskScopeRun {
			continue
		}
		dynKeys = append(dynKeys, key)
	}
	sort.Strings(dynKeys)
	for _, key := range dynKeys {
		st := session.Tasks[key]
		taskID := taskIDForInstance(key, st)
		// Build only the cleanup-relevant fields straight from the definition —
		// no schema / requires / done_when validation (that runs at create / up /
		// task run). Teardown must stay resilient to a def whose config drifted
		// to invalid after the instance was created: a present-but-invalid def
		// must be no more fatal than a disappeared one, so `tws destroy --force`
		// can still reclaim the session. Cleanup needs only the script plus the
		// persisted inputs/outputs.
		r := task.Resolved{NodeID: key, TaskID: taskID, Scope: st.Scope}
		if def, ok := defs[taskID]; ok {
			r.Cleanup = def.Cleanup
			if resolved, execErr := task.ResolveExecution(def.Execution, wf.Environment); execErr == nil {
				r.Execution = resolved
			}
		}
		items = append(items, seqResolved{seq: st.Seq, r: r})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].seq < items[j].seq })
	out := make([]task.Resolved, len(items))
	for i, it := range items {
		out[i] = it.r
	}
	return out, nil
}

func hasLiveRunTask(tasks map[string]*contract.TaskState) bool {
	for _, e := range tasks {
		if e == nil {
			continue
		}
		if e.Scope == contract.TaskScopeRun && e.Status == contract.TaskStatusProduced {
			return true
		}
	}
	return false
}
