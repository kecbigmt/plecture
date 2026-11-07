package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/eventlog"
	"github.com/kecbigmt/plecture/app/internal/state"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// DestroyParams holds parameters for Destroy.
type DestroyParams struct {
	Identifier string
	Force      bool
	Observer   task.Observer
}

// forceRequiresConfirmation follows every suggestion to re-run with --force,
// because an agent that reads only the error would otherwise take the
// suggestion as license to discard what destroy was protecting.
const forceRequiresConfirmation = "--force discards what destroy protects: uncommitted changes, child sessions, and the record of a release nobody verified. Report this failure to a human and get their confirmation before re-running with --force."

// DestroyResult holds the outcome of Destroy.
type DestroyResult struct {
	SessionName string `json:"session_name"`
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
	name, err := resolveSessionName(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}
	var result *DestroyResult
	err = withLifecycleLock(store, name, func() error {
		var destroyErr error
		result, destroyErr = destroyUnlocked(cfg, store, params)
		return destroyErr
	})
	return result, err
}

func destroyUnlocked(cfg *config.Config, store *state.Store, params DestroyParams) (*DestroyResult, error) {
	sessionName, session, err := resolveSession(cfg, store, params.Identifier)
	if err != nil {
		return nil, err
	}
	if err := requireSessionRoot(cfg, session); err != nil {
		return nil, err
	}
	params.Observer = withNodeResultRecording(store, sessionName, params.Observer)
	flushPendingDeliveryLogged(cfg, store, sessionName)
	// Tearing down an existing session is a per-session write; clamp it to the
	// active guard so a guarded orchestrator can't destroy another owner's
	// session it can see via `plect ls`. Create guards on the way in; this
	// closes the symmetric teardown vector.
	if guardErr := checkSessionGuard(cfg, sessionName); guardErr != nil {
		return nil, guardErr
	}
	if guardErr := checkLifecycleRelationGuard(store, sessionName, "destroy"); guardErr != nil {
		return nil, guardErr
	}
	if session.Nodes == nil {
		session.Nodes = make(map[string]*contract.TaskState)
	}
	if session.Tasks == nil {
		session.Tasks = make(map[string]*contract.TaskState)
	}

	result := &DestroyResult{SessionName: sessionName}

	// Fail-closed before any teardown side effect: destroying the parent
	// removes it from the live tree while a child's ParentSession keeps
	// naming it, and plect up never re-adopts a child onto a new live
	// session under that name, so a silent destroy permanently strands the
	// child. --force makes that an explicit, reported choice instead.
	allSessions, err := store.AllE()
	if err != nil {
		// An unreadable store must never read as "no children": proceeding on
		// a fabricated empty child list would silently strand a real one.
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("read session state: %v", err)}
	}
	if children := childNames(allSessions, sessionName); len(children) > 0 {
		if !params.Force {
			return nil, &Error{
				Code: ErrHasChildren,
				Message: fmt.Sprintf(
					"session %s has %d child session(s) that would be orphaned: %s\nUse `plect down %s` + `plect up %s` to reset without orphaning them, or re-run with `plect destroy %s --force` to destroy and orphan them.\n%s",
					sessionName, len(children), strings.Join(children, ", "), sessionName, sessionName, sessionName, forceRequiresConfirmation,
				),
			}
		}
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("orphaned %d child session(s): %s", len(children), strings.Join(children, ", ")))
	}
	if retired := config.RetiredRevisionSpellings(session); len(retired) > 0 {
		if !params.Force {
			return nil, retiredRevisionRefusal(session, "destroy")
		}
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("workflow revision uses retired declaration spellings (%s); cleanup skipped, release not verified", strings.Join(retired, ", ")))
		recordLifecycle(store, sessionName, "destroyed", "session destroyed")
		if err := store.Destroy(sessionName); err != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to destroy state entry: %v", err)}
		}
		return result, nil
	}
	if session.Workflow != "" && session.WorkflowRevision == "" {
		if !params.Force {
			return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("session %q has no stored workflow revision; re-run with `plect destroy %s --force` to remove it without cleanup.\n%s", sessionName, sessionName, forceRequiresConfirmation)}
		}
		result.CleanupWarnings = append(result.CleanupWarnings, "no stored workflow revision; cleanup skipped")
		recordLifecycle(store, sessionName, "destroyed", "session destroyed")
		if err := store.Destroy(sessionName); err != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to destroy state entry: %v", err)}
		}
		return result, nil
	}

	var plan *task.Plan
	needsPlan := len(session.Tasks) > 0 || (session.Workflow == "" && !params.Force)
	for _, node := range session.Nodes {
		if node != nil && node.Status != contract.TaskStatusCleaned {
			needsPlan = true
		}
	}
	if needsPlan {
		var planErr error
		plan, planErr = buildPlanForSession(cfg, session)
		if planErr != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: planErr.Error()}
		}
	}

	// A single reverse-instantiation teardown over every task — static plan
	// nodes (run + session) and dynamic instances merged into one
	// seq-descending pass. This is strictly the reverse of the instantiation
	// stack, so a static node instantiated after a dynamic one is still
	// cleaned first regardless of scope.
	teardown, teardownErr := unifiedTeardownList(cfg, session, false)
	if teardownErr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: teardownErr.Error()}
	}
	vars := sessionVars(cfg, session, plan)
	vars.Force = params.Force
	vars.ReleaseAfterFailure = params.Force
	if cleanupErr := runTaskCleanup(context.Background(), cfg, store, teardown, vars, session, params.Observer); cleanupErr != nil {
		session.UpdatedAt = time.Now()
		putBestEffort(store, session, "run cleanup failure")
		if !params.Force {
			return nil, &Error{
				Code:    ErrExecutionFailed,
				Message: fmt.Sprintf("%v (session %s)\nRe-run with `plect destroy %s --force` to delete the state entry anyway.\n%s", cleanupErr, sessionName, sessionName, forceRequiresConfirmation),
			}
		}
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("cleanup: %v", cleanupErr))
	}

	// Persist any TaskState changes (status flips to cleaned) before we
	// delete the entry, so a failed delete leaves the checkpoint inspectable.
	session.UpdatedAt = time.Now()
	putBestEffort(store, session, "post-run-cleanup checkpoint")

	// The retained destroyed session row is the tombstone. Record its lifecycle
	// transition before making the row non-live so the event shares its id.
	recordLifecycle(store, sessionName, "destroyed", "session destroyed")

	if err := store.Destroy(sessionName); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("failed to destroy state entry: %v", err)}
	}

	if err := eventlog.NewStore(store.Dir()).ClearChainAttempts(session.ID); err != nil {
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("chain-attempt bookkeeping cleanup: %v", err))
	}

	// After the delete, so unwireDeliveryOnTeardown's fresh read sees the
	// session as gone rather than skipping the unsubscribe as still needed.
	if _, errMsg := unwireDeliveryOnTeardown(cfg, store, sessionName, session.Resource, session.ID); errMsg != "" {
		result.CleanupWarnings = append(result.CleanupWarnings, fmt.Sprintf("resource delivery unsubscribe: %s", errMsg))
	}

	return result, nil
}
