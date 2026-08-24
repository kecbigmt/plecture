package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// checkChildCap enforces a parent workflow's optional `max_up_children`: it
// rejects bringing a not-yet-up session (a brand new child, or an existing
// child transitioning from down to up) to run state "up" when doing so would
// push its parent's currently-up child count past the cap. targetAlreadyUp
// exempts an idempotent re-up of a session that is already up — it is
// already counted among the parent's up children, so re-upping it changes
// nothing the cap cares about.
//
// Counting rule: only children whose run state is "up" (any run-scoped task
// produced) count toward the cap; down and destroyed children do not (see
// docs/language/workflows.md).
func checkChildCap(cfg *config.Config, store *state.Store, parentSessionName string, targetAlreadyUp bool) *Error {
	if targetAlreadyUp || parentSessionName == "" {
		return nil
	}
	parent := store.Get(parentSessionName)
	if parent == nil {
		// The "root:<target>" pseudo-parent form (resolveParentSession)
		// stores a literal string with no session state behind it — nothing
		// to read a cap from.
		return nil
	}
	wf, err := loadSessionWorkflow(cfg, parent.WorkspaceDirPath, parent)
	if err != nil {
		return &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load parent %s workflow: %v", parentSessionName, err)}
	}
	if wf.MaxUpChildren == nil {
		return nil
	}
	limit := *wf.MaxUpChildren

	sessions, err := store.AllE()
	if err != nil {
		return &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	count := 0
	for _, name := range childNames(sessions, parentSessionName) {
		if sessionRunState(sessions[name]) == domain.RunUp {
			count++
		}
	}
	if count >= limit {
		return &Error{
			Code: ErrChildCapExceeded,
			Message: fmt.Sprintf(
				"parent session %s has reached its max_up_children cap of %d (%d child session(s) currently up); bring one down or destroy it, then retry",
				parentSessionName, limit, count,
			),
		}
	}
	return nil
}
