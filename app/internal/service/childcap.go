package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/state"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

// reserveChildCapSlot decides inside ReserveUpSlot's locked snapshot rather
// than an unlocked read, so two racing `plect up` processes can't both
// admit past the cap.
func reserveChildCapSlot(cfg *config.Config, store *state.Store, childSessionName, parentSessionName string, targetAlreadyUp bool) (reserved bool, capErr *Error) {
	if targetAlreadyUp {
		return false, nil
	}
	virtualRoot := parentSessionName == "" || strings.HasPrefix(parentSessionName, "root:")
	limit := 0
	capParent := parentSessionName
	capLabel := "parent session " + parentSessionName
	if virtualRoot {
		if cfg == nil || cfg.MaxUpChildren == nil {
			return false, nil
		}
		limit = *cfg.MaxUpChildren
		capParent = domain.VirtualRootReservationParent
		capLabel = "virtual root"
	} else {
		parent, err := store.GetE(parentSessionName)
		if err != nil {
			return false, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load parent %s: %v", parentSessionName, err)}
		}
		if parent == nil {
			return false, nil
		}
		wf, err := loadSessionWorkflow(cfg, parent)
		if err != nil {
			return false, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load parent %s workflow: %v", parentSessionName, err)}
		}
		if wf.MaxUpChildren == nil {
			return false, nil
		}
		limit = *wf.MaxUpChildren
	}
	if limit < 1 {
		return false, nil
	}
	queued := map[string]bool{}
	all, err := store.AllE()
	if err != nil {
		return false, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("read queued sessions: %v", err)}
	}
	for name, s := range all {
		if s == nil {
			continue
		}
		memberOfCap := (virtualRoot && (s.ParentSession == "" || strings.HasPrefix(s.ParentSession, "root:"))) || (!virtualRoot && s.ParentSession == parentSessionName)
		if !memberOfCap {
			continue
		}
		waiting, err := queuedForWake(cfg, store, s)
		if err != nil {
			return false, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("read sleep queue: %v", err)}
		}
		queued[name] = waiting
	}

	var rejection *Error
	approved, lockErr := store.ReserveUpSlot(childSessionName, capParent, func(sessions map[string]*domain.Session, reservations map[string]state.UpReservation) bool {
		var targetAt time.Time
		if target := sessions[childSessionName]; target != nil && queued[childSessionName] {
			targetAt = target.SleepSince
		}
		for name, s := range sessions {
			if name == childSessionName || s == nil || !queued[name] || s.Status != contract.SessionStatusDown || s.SleepSince.IsZero() || sessionFinalized(s) {
				continue
			}
			if targetAt.IsZero() || s.SleepSince.Before(targetAt) || (s.SleepSince.Equal(targetAt) && name < childSessionName) {
				rejection = &Error{Code: ErrChildCapExceeded, Message: fmt.Sprintf("%s has an earlier sleeping child waiting for capacity", capLabel)}
				return false
			}
		}
		up := make(map[string]bool)
		count := 0
		var children []string
		if virtualRoot {
			for name, session := range sessions {
				if session != nil && (session.ParentSession == "" || strings.HasPrefix(session.ParentSession, "root:")) {
					children = append(children, name)
				}
			}
		} else {
			children = childNames(sessions, parentSessionName)
		}
		for _, name := range children {
			if name == childSessionName {
				continue // this decides its own slot, not a sibling's
			}
			if sessionRunState(cfg, sessions[name]) == domain.RunUp {
				count++
				up[name] = true
			}
		}
		for name, res := range reservations {
			if res.Parent != capParent || up[name] {
				continue
			}
			count++
		}
		if count >= limit {
			rejection = &Error{
				Code: ErrChildCapExceeded,
				Message: fmt.Sprintf(
					"%s has reached its max_up_children cap of %d (%d child session(s) currently up or reserved); bring one down or destroy it, then retry",
					capLabel, limit, count,
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

// asChildCapExceeded identifies the capacity result before Up converts it to
// an accepted sleep.
func asChildCapExceeded(err error) (*Error, bool) {
	svcErr, ok := err.(*Error)
	if !ok || svcErr == nil || svcErr.Code != ErrChildCapExceeded {
		return nil, false
	}
	return svcErr, true
}
