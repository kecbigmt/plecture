package service

import (
	"errors"
	"fmt"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// reserveChildCapSlot decides inside ReserveUpSlot's locked snapshot rather
// than an unlocked read, so two racing `plect up` processes can't both
// admit past the cap.
func reserveChildCapSlot(cfg *config.Config, store *state.Store, childSessionName, parentSessionName string, targetAlreadyUp bool) (reserved bool, capErr *Error) {
	if targetAlreadyUp || parentSessionName == "" {
		return false, nil
	}
	parent := store.Get(parentSessionName)
	if parent == nil {
		// A "root:" pseudo-parent (resolveParentSession) has no session to read a cap from.
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
			if name == childSessionName {
				continue // this decides its own slot, not a sibling's
			}
			if sessionRunState(sessions[name]) == domain.RunUp {
				count++
				up[name] = true
			}
		}
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
	if errors.Is(lockErr, state.ErrUpAlreadyReserved) {
		return false, &Error{
			Code:    ErrChildUpInProgress,
			Message: fmt.Sprintf("session %s already has a `plect up` in progress; retry once it finishes", childSessionName),
		}
	}
	if lockErr != nil {
		return false, &Error{Code: ErrExecutionFailed, Message: lockErr.Error()}
	}
	if !approved {
		return false, rejection
	}
	return true, nil
}

func releaseChildCapSlot(store *state.Store, childSessionName string) {
	if childSessionName == "" {
		return
	}
	_ = store.ReleaseUpSlot(childSessionName) // best-effort: a retry or Destroy still recovers it
}
