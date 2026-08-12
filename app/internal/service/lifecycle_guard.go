package service

import (
	"fmt"
	"os"

	"github.com/cradel-dev/cradel/app/internal/domain"
	"github.com/cradel-dev/cradel/app/internal/state"
)

// checkLifecycleRelationGuard authorizes a destructive lifecycle operation
// (down/destroy) by tree relation: only the target session itself or one of
// its descendants (its own dispatched subtree) may act on it. The caller
// identity is the ambient SENNIT_SESSION_NAME; its absence means a human is
// running the raw CLI outside any session pane, which stays exempt so manual
// recovery is never blocked by this guard.
func checkLifecycleRelationGuard(store *state.Store, targetName, op string) *Error {
	caller := os.Getenv("SENNIT_SESSION_NAME")
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
