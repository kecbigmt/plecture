package domain

import (
	"time"

	contract "github.com/kecbigmt/plecture/contracts/state"
)

// UnansweredDelivery is an event the session's runtime channel delivered
// with no non-empty status written since. A non-empty status removes every
// delivery before it, so its existence alone says no turn has started after
// DeliveredAt.
type UnansweredDelivery struct {
	EventID     string
	EventType   string
	DeliveredAt time.Time
}

// RunStartedAt is the latest setup of a produced run-scoped task. A fact
// recorded before it belongs to a runtime that no longer exists.
func RunStartedAt(s *Session) time.Time {
	var started time.Time
	for _, task := range MergedTasks(s) {
		if task != nil && task.Scope == contract.TaskScopeRun && task.Status == contract.TaskStatusProduced && task.SetupAt.After(started) {
			started = task.SetupAt
		}
	}
	return started
}
