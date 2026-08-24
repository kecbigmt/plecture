package service

import (
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// reserveChildCapSlot enforces a parent workflow's optional
// `max_up_children` (counting rule: docs/language/workflows.md).
// targetAlreadyUp exempts an idempotent re-up. The decision runs inside
// ReserveUpSlot's locked snapshot rather than an unlocked read, so two
// `plect up` processes racing the same at-capacity parent can't both admit
// past the cap. A reserved=true result must be released
// (releaseChildCapSlot) once the child's own state settles the question.
func reserveChildCapSlot(cfg *config.Config, store *state.Store, childSessionName, parentSessionName string, targetAlreadyUp bool) (reserved bool, capErr *Error) {
	if targetAlreadyUp || parentSessionName == "" {
		return false, nil
	}
	parent := store.Get(parentSessionName)
	if parent == nil {
		// The "root:<target>" pseudo-parent form has no session state to
		// read a cap from (resolveParentSession).
		return false, nil
	}
	wf, err := loadSessionWorkflow(cfg, parent.WorkspaceDirPath, parent)
	if err != nil {
		return false, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load parent %s workflow: %v", parentSessionName, err)}
	}
	if wf.MaxUpChildren == nil {
		return false, nil
	}
	limit := *wf.MaxUpChildren

	var rejection *Error
	approved, lockErr := store.ReserveUpSlot(childSessionName, parentSessionName, func(sessions map[string]*domain.Session, reservations map[string]state.UpReservation) bool {
		up := make(map[string]bool)
		count := 0
		for _, name := range childNames(sessions, parentSessionName) {
			if sessionRunState(sessions[name]) == domain.RunUp {
				count++
				up[name] = true
			}
		}
		// Skip a reservation whose child already counted via real run
		// state — the brief overlap right after release, not a second slot.
		for name, res := range reservations {
			if res.Parent != parentSessionName || up[name] {
				continue
			}
			count++
		}
		if count >= limit {
			rejection = &Error{
				Code: ErrChildCapExceeded,
				Message: fmt.Sprintf(
					"parent session %s has reached its max_up_children cap of %d (%d child session(s) currently up or reserved); bring one down or destroy it, then retry",
					parentSessionName, limit, count,
				),
			}
			return false
		}
		return true
	})
	if lockErr != nil {
		return false, &Error{Code: ErrExecutionFailed, Message: lockErr.Error()}
	}
	if !approved {
		return false, rejection
	}
	return true, nil
}

// releaseChildCapSlot undoes a reserveChildCapSlot(...) that returned
// reserved=true. Safe to call unconditionally from a defer: releasing a
// child with no outstanding reservation is a no-op.
func releaseChildCapSlot(store *state.Store, childSessionName string) {
	if childSessionName == "" {
		return
	}
	_ = store.ReleaseUpSlot(childSessionName) // best-effort: TTL or Destroy clears a failed release
}
