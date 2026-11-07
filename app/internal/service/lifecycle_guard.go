package service

import (
	"fmt"
	"os"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/lang"
	"github.com/kecbigmt/plecture/app/internal/state"
)

// checkLifecycleRelationGuard authorizes a destructive lifecycle operation
// (down/destroy) by tree relation: only the target session itself or one of
// its descendants (its own dispatched subtree) may act on it. The caller
// identity is the ambient PLECT_SESSION_NAME; its absence means a human is
// running the raw CLI outside any session pane, which stays exempt so manual
// recovery is never blocked by this guard.
func checkLifecycleRelationGuard(store *state.Store, targetName, op string) *Error {
	caller := os.Getenv("PLECT_SESSION_NAME")
	if caller == "" {
		return nil
	}
	sessions, err := store.AllE()
	if err != nil {
		return &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("read session state: %v", err)}
	}
	switch rel := domain.RelationFromTarget(sessions, caller, targetName); rel {
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

// retiredRevisionRefusal stops a lifecycle operation before any side effect,
// because this release decodes a retired-dialect revision into a different
// plan: running it would set up or clean up the wrong environment.
func retiredRevisionRefusal(session *domain.Session, op string) *Error {
	spellings := config.RetiredRevisionSpellings(session)
	if len(spellings) == 0 {
		return nil
	}
	name := session.Name
	return &Error{
		Code: ErrExecutionFailed,
		Message: fmt.Sprintf(
			"session %q is pinned to a workflow revision written with retired declaration spellings (%s), so this release refuses %s for it.\n"+
				"With the previous release, save the session's uncommitted work and run `plect destroy %s`; then upgrade and recreate the session. See %s.\n"+
				"`plect destroy %s --force` on this release removes the record without running cleanup, so nothing verifies that the session's allocations were released.\n%s",
			name, strings.Join(spellings, ", "), op, name, lang.WorkspaceProviderMigrationGuide, name, forceRequiresConfirmation,
		),
	}
}
