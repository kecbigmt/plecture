package service

import (
	"fmt"
	"strings"

	"context"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
	"github.com/kecbigmt/sennit/app/internal/workspace"
	contract "github.com/kecbigmt/sennit/contracts/state"
)

// GCAction describes what action should be taken for a session.
type GCAction string

const (
	// GCActionDelete means the session will be auto-deleted.
	GCActionDelete GCAction = "delete"
	// GCActionManual means the session needs manual attention.
	GCActionManual GCAction = "manual"
)

// GCReason describes why a session is classified for GC.
type GCReason string

const (
	GCReasonWorktreeMissing GCReason = "worktree_missing"
	// GCReasonWorkflowMissing means the session's frozen workflow definition
	// no longer exists; neither GC nor destroy can run its cleanups.
	GCReasonWorkflowMissing GCReason = "workflow_missing"
	// GCReasonDone means all task-level done_when predicates hold.
	GCReasonDone GCReason = "done"
	// GCReasonUnhealthy means the session has a produced run-scoped task
	// whose declared healthcheck fails.
	GCReasonUnhealthy GCReason = "unhealthy"
)

// GCEntry describes one session's GC classification and result.
type GCEntry struct {
	SessionName    string   `json:"session_name"`
	ResourceID     string   `json:"resource_id"`
	Action         GCAction `json:"action"`
	Reason         GCReason `json:"reason"`
	Description    string   `json:"description"`
	Deleted        bool     `json:"deleted"`
	DeleteWarnings []string `json:"delete_warnings,omitempty"`
}

// GCResult holds the result of a GC operation.
type GCResult struct {
	Entries  []GCEntry `json:"entries"`
	Executed bool      `json:"executed"`
	Warnings []string  `json:"warnings,omitempty"`
}

// GCParams holds parameters for the GC operation.
type GCParams struct {
	Execute      bool
	DeleteBranch bool
}

// GC identifies and optionally removes stale sessions.
func GC(cfg *config.Config, store *state.Store, params GCParams) (*GCResult, error) {
	sessions := store.All()
	mgr := workspace.NewManager(cfg.WorktreesRoot)

	result := &GCResult{Executed: params.Execute}

	for _, s := range sessions {
		wfFound, warn := workflowFoundForSession(cfg, s)
		if warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
		// Missing worktrees are reconciled first; otherwise repo-local workflow
		// files disappearing with the worktree would strand stale state.
		if !wfFound && fileExists(s.WorktreePath) {
			result.Warnings = append(result.Warnings, fmt.Sprintf("session %s: frozen workflow %q not found; restore the workflow file or clean up manually", s.Name, s.Workflow))
			result.Entries = append(result.Entries, GCEntry{
				SessionName: s.Name,
				ResourceID:  s.ResourceID,
				Action:      GCActionManual,
				Reason:      GCReasonWorkflowMissing,
				Description: fmt.Sprintf("frozen workflow %q not found in any config layer", s.Workflow),
			})
			continue
		}
		taskDefs, warn := taskDefsForSession(cfg, s)
		if warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
		entry, warn := classifySession(s, taskDefs, sessions)
		if warn != "" {
			result.Warnings = append(result.Warnings, warn)
		}
		if entry == nil {
			continue // healthy session, skip
		}

		if params.Execute && entry.Action == GCActionDelete {
			if entry.Reason == GCReasonWorktreeMissing || s.Workflow == "" {
				executeGCDelete(s, mgr, store, entry, params.DeleteBranch, sessions)
			} else {
				executeGCDestroy(cfg, store, s, entry, params.DeleteBranch)
			}
		}

		result.Entries = append(result.Entries, *entry)
	}

	return result, nil
}

// A missing frozen workflow blocks auto-delete because destroy cannot build its cleanup plan.
func workflowFoundForSession(cfg *config.Config, s *domain.Session) (found bool, warn string) {
	if s.Workflow == "" {
		return true, ""
	}
	workflows, err := cfg.LoadWorkflows(s.WorktreePath)
	if err != nil {
		return true, fmt.Sprintf("session %s: load workflows: %v (falling back to legacy merged check)", s.Name, err)
	}
	_, ok := workflows[s.Workflow]
	return ok, ""
}

// Load failures fall back to legacy GC but stay visible as warnings.
func taskDefsForSession(cfg *config.Config, s *domain.Session) (map[string]config.TaskDefinition, string) {
	if len(s.Tasks) == 0 {
		return nil, ""
	}
	defs, err := cfg.LoadTaskDefinitions(s.WorktreePath)
	if err != nil {
		return nil, fmt.Sprintf("session %s: load task definitions: %v (falling back to legacy completion basis)", s.Name, err)
	}
	return defs, ""
}

func classifySession(s *domain.Session, taskDefs map[string]config.TaskDefinition, sessions map[string]*domain.Session) (*GCEntry, string) {
	wtExists := fileExists(s.WorktreePath)
	runtimeAlive := sessionRuntimeAlive(s, taskDefs)

	// GC's execute step always routes a worktree-missing verdict through
	// executeGCDelete (no plan can be built without a worktree), which never
	// touches the runtime — so a live runtime here is left running, not
	// cleaned up. The description must say so rather than promise a cleanup
	// that won't happen.
	if !wtExists {
		desc := "worktree does not exist"
		if runtimeAlive {
			desc += "; runtime will keep running (state deletion cannot run its cleanup without a worktree) — clean it up manually if needed"
		}
		return &GCEntry{
			SessionName: s.Name,
			ResourceID:  s.ResourceID,
			Action:      GCActionDelete,
			Reason:      GCReasonWorktreeMissing,
			Description: desc,
		}, ""
	}

	done, doneDesc, reason, warn := evaluateDone(s, taskDefs, sessions)

	if done {
		if isWorktreeClean(s.WorktreePath) {
			desc := doneDesc + " and worktree is clean"
			if runtimeAlive {
				// executeGCDestroy (which runs the declared cleanup) is only
				// reached when a frozen workflow exists; s.Workflow == ""
				// falls back to executeGCDelete, same caveat as above.
				if s.Workflow != "" {
					desc += "; runtime will be cleaned up"
				} else {
					desc += "; runtime will keep running (no frozen workflow to build a cleanup plan from) — clean it up manually if needed"
				}
			}
			return &GCEntry{
				SessionName: s.Name,
				ResourceID:  s.ResourceID,
				Action:      GCActionDelete,
				Reason:      reason,
				Description: desc,
			}, warn
		}
	}

	if !runtimeAlive {
		desc := "runtime is unhealthy"
		if done {
			desc += "; " + doneDesc + " but worktree is dirty"
		} else {
			desc += "; not done (" + doneDesc + ")"
		}
		return &GCEntry{
			SessionName: s.Name,
			ResourceID:  s.ResourceID,
			Action:      GCActionManual,
			Reason:      GCReasonUnhealthy,
			Description: desc,
		}, warn
	}

	return nil, warn
}

// sessionRuntimeAlive reports whether the session's runtime is alive, derived
// from the same declarative healthcheck EvaluateHealth uses for `sennit status`.
// A session with no produced run-scoped task declaring a healthcheck has
// nothing to probe and is treated as alive — GC's dead-runtime detection only
// fires when a healthcheck is declared and fails.
func sessionRuntimeAlive(s *domain.Session, taskDefs map[string]config.TaskDefinition) bool {
	report := evaluateHealthFor(s.Name, s.Tasks, taskDefs, sessionVars(s), 0)
	return !report.Declared || report.Healthy
}

func evaluateDone(s *domain.Session, taskDefs map[string]config.TaskDefinition, sessions map[string]*domain.Session) (bool, string, GCReason, string) {
	status, count, warnings := aggregateTaskDoneWhen(s, taskDefs, sessions)
	warn := strings.Join(warnings, "; ")
	if count > 0 {
		switch status {
		case task.DoneSatisfied:
			return true, fmt.Sprintf("all %d done_when task(s) satisfied", count), GCReasonDone, warn
		case task.DoneUnsatisfied:
			return false, fmt.Sprintf("%d done_when task(s); at least one unsatisfied", count), GCReasonDone, warn
		default:
			return false, fmt.Sprintf("%d done_when task(s); at least one pending", count), GCReasonDone, warn
		}
	}

	return false, "no done_when tasks; nothing to evaluate", GCReasonDone, warn
}

// aggregateTaskDoneWhen treats unknown dynamic instances as pending so they
// cannot fall through to the legacy merged-PR delete path.
func aggregateTaskDoneWhen(s *domain.Session, taskDefs map[string]config.TaskDefinition, sessions map[string]*domain.Session) (task.DoneStatus, int, []string) {
	satisfied, unsatisfied, count := 0, 0, 0
	var warnings []string
	for key, st := range s.Tasks {
		if st == nil || key == contract.WorkflowPseudoNodeID || st.Status == contract.TaskStatusCleaned {
			continue
		}
		taskID := taskIDForInstance(key, st)
		def, ok := taskDefs[taskID]
		if !ok {
			if st.Dynamic {
				warnings = append(warnings, fmt.Sprintf("session %s: task %q (instance %q) not found in config; completion cannot be evaluated", s.Name, taskID, key))
				count++
			}
			continue
		}
		if def.DoneWhen == nil {
			if len(st.ExtraDoneWhen) == 0 {
				continue
			}
		}
		dw, err := effectiveDoneWhen(def.DoneWhen, st)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("session %s: instance %q done_when cannot be evaluated: %v", s.Name, key, err))
			count++
			continue
		}
		count++
		switch task.EvaluateTaskDoneWhenWithContext(dw, st.Outputs, doneWhenEvalContext(s.Name, st, sessions)).Overall {
		case task.DoneSatisfied:
			satisfied++
		case task.DoneUnsatisfied:
			unsatisfied++
		}
	}
	switch {
	case count == 0:
		return "", 0, warnings
	case unsatisfied > 0:
		return task.DoneUnsatisfied, count, warnings
	case satisfied == count:
		return task.DoneSatisfied, count, warnings
	default:
		return task.DonePending, count, warnings
	}
}

// Empty TaskID preserves the older static-node state shape.
func taskIDForInstance(key string, st *contract.TaskState) string {
	if st.TaskID != "" {
		return st.TaskID
	}
	return key
}

// isWorktreeClean checks if a worktree has no uncommitted changes or untracked files.
func isWorktreeClean(wtPath string) bool {
	status, err := workspace.GetWorktreeStatus(context.Background(), wtPath)
	if err != nil {
		return false
	}
	return !status.Dirty && status.UntrackedFiles == 0
}

// executeGCDestroy tears the session down through the regular non-force
// destroy path so task cleanups (runtime teardown, slack thread close, ...) run
// in order. A blocked cleanup or dirty worktree aborts the destroy — the
// session survives with a warning instead of being force-reaped.
func executeGCDestroy(cfg *config.Config, store *state.Store, s *domain.Session, entry *GCEntry, deleteBranch bool) {
	_, err := Destroy(cfg, store, DestroyParams{
		Identifier:   s.Name,
		Force:        false,
		DeleteBranch: deleteBranch,
	})
	if err != nil {
		entry.DeleteWarnings = append(entry.DeleteWarnings, fmt.Sprintf("destroy blocked: %v", err))
	}
	entry.Deleted = stateDeleted(s.Name, store)
}

// executeGCDelete removes a stale session's resources directly. Used when
// there is nothing left to clean up in order (worktree already gone) or the
// session predates frozen workflows so no task plan can be built.
//
// Unlike executeGCDestroy (which routes through Destroy and inherits its
// child guard), this calls store.Delete directly, so it re-checks for
// children itself. It skips rather than orphans — the parent is picked up
// in a later GC pass once its children are gone. The check uses the
// snapshot taken at the start of this GC run (not a live store re-read),
// so the skip/collect outcome doesn't depend on map iteration order over
// sessions processed earlier in the same pass.
func executeGCDelete(s *domain.Session, mgr *workspace.Manager, store *state.Store, entry *GCEntry, deleteBranch bool, sessions map[string]*domain.Session) {
	if children := childNames(sessions, s.Name); len(children) > 0 {
		entry.DeleteWarnings = append(entry.DeleteWarnings, fmt.Sprintf("state deletion skipped: session has %d child session(s): %s", len(children), strings.Join(children, ", ")))
		entry.Deleted = false
		return
	}

	wtExists := fileExists(s.WorktreePath)

	// Remove worktree if it exists
	if wtExists {
		repoDir := workspace.ContainerDir(s.WorktreePath)
		gitDir, err := mgr.FindGitDir(repoDir, s.WorktreePath)
		if err != nil {
			entry.DeleteWarnings = append(entry.DeleteWarnings, fmt.Sprintf("worktree removal skipped: %v", err))
		} else {
			if err := mgr.RemoveByPath(context.Background(), s.WorktreePath, gitDir, s.Branch, true, deleteBranch); err != nil {
				entry.DeleteWarnings = append(entry.DeleteWarnings, fmt.Sprintf("worktree removal failed: %v", err))
			}
		}
	}

	// Delete state entry
	if err := store.Delete(s.Name); err != nil {
		entry.DeleteWarnings = append(entry.DeleteWarnings, fmt.Sprintf("state deletion failed: %v", err))
	}

	// No plan can be built here (missing worktree or workflow), so there is no
	// declared cleanup to run against the runtime — any leftover process is
	// left for the operator to reap manually.

	entry.Deleted = len(entry.DeleteWarnings) == 0 || stateDeleted(s.Name, store)
}

// stateDeleted checks whether the state entry was successfully deleted.
func stateDeleted(name string, store *state.Store) bool {
	return store.Get(name) == nil
}
