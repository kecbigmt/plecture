package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/effect"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/publicoutput"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// nameCollides reports whether name is already taken in s.Nodes/s.Tasks.
func nameCollides(s *domain.Session, name string) bool {
	return domain.TaskState(s, name) != nil
}

// A --name dynamic instance may legally occupy a node's id before that node
// is ever produced (nameCollides only checks existing state), so runNodeSetup
// excludes any such id from ordered before running or writing back setup
// results — its merged entry is really that instance's record, not a node's.
func runNodeSetup(ctx context.Context, cfg *config.Config, store *state.Store, ordered []task.Resolved, vars task.SessionVars, session *domain.Session, observer task.Observer) (map[string]*contract.Resource, error) {
	nodesOnly := make([]task.Resolved, 0, len(ordered))
	previous := make(map[string]time.Time, len(ordered))
	for _, r := range ordered {
		if session.Tasks[r.NodeID] == nil {
			nodesOnly = append(nodesOnly, r)
			if st := session.Nodes[r.NodeID]; st != nil && st.Status == contract.TaskStatusProduced {
				previous[r.NodeID] = st.SetupAt
			}
		}
	}
	merged := domain.MergedTasks(session)
	if observer == nil {
		observer = noopTaskObserver{}
	}
	err := task.RunSetup(ctx, nodesOnly, vars, merged, nodeSubscriptionReleaseObserver{Observer: observer, cfg: cfg, store: store, sessionName: session.Name, projectRoot: session.ProjectRoot})
	if session.Nodes == nil {
		session.Nodes = make(map[string]*contract.TaskState)
	}
	for _, r := range nodesOnly {
		if st, ok := merged[r.NodeID]; ok {
			session.Nodes[r.NodeID] = st
		}
	}
	wired := make(map[string]*contract.Resource)
	errs := []error{err}
	for _, r := range nodesOnly {
		st := session.Nodes[r.NodeID]
		if st == nil || st.Status != contract.TaskStatusProduced || r.Subscription == "" {
			continue
		}
		resolvedNow := st.Subscription == nil
		if resolvedNow {
			identifier, ok := st.Outputs[r.Subscription].(string)
			if !ok || strings.TrimSpace(identifier) == "" {
				errs = append(errs, fmt.Errorf("node %q subscription output %q must be a non-empty resource ID", r.NodeID, r.Subscription))
				continue
			}
			resource, boundErr := boundResource(cfg, store, session.ProjectRoot, identifier)
			if boundErr != nil {
				errs = append(errs, fmt.Errorf("node %q subscription: %w", r.NodeID, boundErr))
				continue
			}
			st.Subscription = resource
		}
		// A subscription that failed to resolve on an earlier pass was never
		// wired, even though the node's setup did not run again.
		if resolvedNow || st.SetupAt != previous[r.NodeID] {
			wired[r.NodeID] = st.Subscription
		}
	}
	return wired, errors.Join(errs...)
}

func runTaskCleanup(ctx context.Context, cfg *config.Config, store *state.Store, ordered []task.Resolved, vars task.SessionVars, session *domain.Session, observer task.Observer) error {
	before := make(map[string]bool)
	for _, r := range ordered {
		if st := session.Nodes[r.NodeID]; st != nil && st.Status != contract.TaskStatusCleaned {
			before[r.NodeID] = true
		}
	}
	err := task.RunCleanup(ctx, ordered, vars, domain.MergedTasks(session), observer)
	released := make(map[string]*contract.TaskState)
	for id := range before {
		if st := session.Nodes[id]; st != nil && st.Status == contract.TaskStatusCleaned && st.Subscription != nil {
			released[id] = st
		}
	}
	if releaseErr := releaseNodeSubscriptions(cfg, store, session.Name, session.ProjectRoot, released); releaseErr != nil && err == nil {
		err = releaseErr
	}
	return err
}

// mergeTasks persists Nodes/Tasks by overlay under the state lock rather
// than a blind Put, so a nested `plect task setup` subprocess's disk-only
// write during this call's own (unlocked) setup pass survives.
func mergeTasks(store *state.Store, sessionName string, session *domain.Session) error {
	return mergeTasksWithSubscriptions(nil, store, sessionName, session, nil)
}

func mergeTasksWithSubscriptions(cfg *config.Config, store *state.Store, sessionName string, session *domain.Session, subscriptions map[string]*contract.Resource) error {
	if err := store.Update(sessionName, func(s *domain.Session) error {
		if s.Nodes == nil {
			s.Nodes = make(map[string]*contract.TaskState)
		}
		for k, v := range session.Nodes {
			s.Nodes[k] = v
		}
		if s.Tasks == nil {
			s.Tasks = make(map[string]*contract.TaskState)
		}
		for k, v := range session.Tasks {
			s.Tasks[k] = v
		}
		s.UpdatedAt = session.UpdatedAt
		return nil
	}); err != nil {
		return err
	}
	return refreshNodeIdentities(store, sessionName, session)
}

func replaceRuntimeState(store *state.Store, sessionName string, session *domain.Session) error {
	if err := store.Update(sessionName, func(s *domain.Session) error {
		s.Nodes = session.Nodes
		s.Tasks = session.Tasks
		s.Health = session.Health
		s.LastTickAt = session.LastTickAt
		s.TickBackoff = session.TickBackoff
		s.UpdatedAt = session.UpdatedAt
		return nil
	}); err != nil {
		return err
	}
	return refreshNodeIdentities(store, sessionName, session)
}

// refreshNodeIdentities copies each of session.Nodes' now-confirmed
// ExecutionID back from store, so Up's UpOrder() re-walk (which persists the
// same objects again later) targets its own row by exact id instead of the
// identity-less fallback (see TaskState.ExecutionID).
func refreshNodeIdentities(store *state.Store, sessionName string, session *domain.Session) error {
	if len(session.Nodes) == 0 {
		return nil
	}
	refreshed, err := store.GetE(sessionName)
	if err != nil {
		return err
	}
	if refreshed == nil {
		return nil
	}
	for id := range session.Nodes {
		if st, ok := refreshed.Nodes[id]; ok {
			session.Nodes[id] = st
		}
	}
	return nil
}

// setSessionStatus durably records a lifecycle-status transition on its
// own, separate from mergeTasks/replaceRuntimeState's narrower field
// sets. Each call site decides its own timing against what it can
// already promise at that point: Up sets it to up only once setup has
// fully succeeded; Down and a force-recreate set it to down as soon as
// they commit to tearing the runtime down, before attempting to, so
// every failure after that point already reflects it.
func setSessionStatus(store *state.Store, sessionName, status string) error {
	return store.Update(sessionName, func(s *domain.Session) error {
		s.Status = status
		if status == contract.SessionStatusUp {
			s.SleepSince = time.Time{}
		}
		s.UpdatedAt = time.Now()
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
		if rootTarget == "" {
			if explicitSet {
				return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("root parent target %q does not exist", rootTarget)}
			}
			return "", nil
		}
		target, err := store.GetE(rootTarget)
		if err != nil {
			return "", &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("read root parent target %q: %v", rootTarget, err)}
		}
		if target == nil {
			if explicitSet {
				return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("root parent target %q does not exist", rootTarget)}
			}
			return "", nil
		}
		return candidate, nil
	}
	target, err := store.GetE(candidate)
	if err != nil {
		return "", &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("read parent session %q: %v", candidate, err)}
	}
	if target == nil {
		if explicitSet {
			return "", &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("parent session %q does not exist", candidate)}
		}
		return "", nil
	}
	return candidate, nil
}

// sessionVars builds the template variable bundle for a session's task
// hooks. plan is optional (nil when the caller has no full compiled plan in
// scope, e.g. a dynamic instance's own cleanup) — it feeds the terminal
// "..."}} helper binding; every other field is plan-independent.
func sessionVars(cfg *config.Config, s *domain.Session, plan *task.Plan) task.SessionVars {
	publicOutputs, err := publicoutput.Declare(s)
	if err != nil {
		slog.Warn("public outputs not declared", "session", s.Name, "error", err)
	}
	return task.SessionVars{
		PublicOutputs: publicOutputs,
		Name:          s.Name,
		ResourceID:    domain.Locator(s.Resource),
		Tag:           s.Tag,
		ParentSession: s.ParentSession,
		Inputs:        s.Inputs,
		Plugins:       cfg.Plugins,
		Terminal:      terminalBinding(plan, s),
	}
}

// terminalBinding resolves the plan's [terminal]-declaring task (if any)
// into the terminal capability's binding: its verbs plus its
// own current outputs (the .Self a nested render needs). Nil plan or no
// declaring task means the terminal capability is unavailable for this resolution —
// the same way a caller with no full plan in scope already lacks
// `.Nodes.<id>.outputs` access to sibling tasks outside its own dependency
// set.
func terminalBinding(plan *task.Plan, s *domain.Session) *task.TerminalBinding {
	if plan == nil {
		return nil
	}
	t := plan.TerminalTask()
	if t == nil {
		return nil
	}
	outputs := map[string]any{}
	dir := ""
	if st, ok := s.Nodes[t.NodeID]; ok && st != nil {
		if self := effect.TerminalSelf(t.Layers, st); self != nil {
			outputs = self
		}
		dir = st.Directory
	}
	return &task.TerminalBinding{Ops: t.Terminal, Outputs: outputs, Dir: dir, SourcePath: t.SourcePath, From: t.From}
}

func inputsOnExistingSessionMessage() string {
	return "--input can only be used when creating a session.\nThis session already exists; destroy and recreate it to change input."
}

// resolveSessionInputs validates raw input against the active workflow's
// input_schema when present, falling back to the global config-level schema
// for the legacy inline-tasks path. nil is normalized to `{}` only when a
// schema is declared, so required-field configs fail fast instead of silently
// accepting `{}`.
func resolveSessionInputs(cfg *config.Config, projectRoot, workflowName string, raw map[string]any) (map[string]any, *Error) {
	inline, file, sourceID := cfg.InputsSchema, cfg.ResolvedInputsSchemaPath(), "plect:config:inputs"
	if workflowName != "" {
		workflows, err := cfg.LoadWorkflows(projectRoot)
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
	schema, err := lang.CompileSchema(inline, file, sourceID)
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
	if session.Workflow != "" && session.WorkflowRevision == "" {
		return true
	}
	plan, err := buildPlanForSession(cfg, session)
	if err != nil || plan == nil {
		return false
	}
	for _, r := range plan.Session {
		st, ok := session.Nodes[r.NodeID]
		if !ok || st == nil || st.Status != contract.TaskStatusProduced {
			return true
		}
	}
	return false
}

// loadSessionWorkflow reloads the workflow a session is frozen to, for a
// caller that has only the session in hand, not an already-loaded
// WorkflowFile.
func loadSessionWorkflow(cfg *config.Config, session *domain.Session) (config.WorkflowFile, error) {
	if session == nil || session.Workflow == "" {
		return config.WorkflowFile{}, nil
	}
	if r, ok, err := config.RevisionForSession(session); ok || err != nil {
		return r.Workflow, err
	}
	return config.WorkflowFile{}, fmt.Errorf("session %q has no stored workflow revision", session.Name)
}

func workdirProducer(cfg *config.Config, session *domain.Session) string {
	wf, err := loadSessionWorkflow(cfg, session)
	if err != nil || wf.Workdir == nil {
		return ""
	}
	return wf.Workdir.Node
}

// sessionWorkdir evaluates the directory the session's workflow declares as its
// `workdir`, against the outputs its producer node recorded. A workflow that
// declares none has no directory, reported as "".
func sessionWorkdir(cfg *config.Config, session *domain.Session) (string, error) {
	wf, err := loadSessionWorkflow(cfg, session)
	if err != nil {
		return "", err
	}
	return task.WorkdirOf(wf.Workdir, session.Nodes)
}
