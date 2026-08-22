// Package chain is the deterministic workflow-chaining engine's pure core: it
// decides whether a chain fires against a work session's raw facts, with no I/O
// and no session spawning. The service layer assembles Facts from persisted
// state and acts on the decision (spawning the workflow session at its
// placement); this package only answers "does it fire, and what does it wire".
package chain

import (
	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// JudgeFact is the raw-fact view of one judge leaf at the current revision,
// projected from the done_when evaluation (not a stored gate state). Pending is
// true when the leaf has no usable verdict (missing / stale / self / wrong
// relation); Action is the current verdict's action ("approve" /
// "request_changes") or empty when pending.
type JudgeFact struct {
	Pending bool
	Action  string
}

// Facts is the raw-fact snapshot a chain's `when` evaluates against: the work
// instance's outputs plus its judge leaves, both read directly from
// (outputs, judge records, revision).
type Facts struct {
	// State is the pair of live roots a trigger reads: what the declared
	// observer publishes about the resource, and what the instance holds.
	State  task.CompletionState
	Judges map[string]JudgeFact
}

// WhenSatisfied reports whether every fact in `when` holds. An empty `when`
// returns false — config validation forbids it, so this is a defensive floor
// against an unconditional fire.
func WhenSatisfied(when config.ChainWhen, facts Facts) bool {
	if len(when.All) == 0 {
		return false
	}
	for _, fact := range when.All {
		if !factHolds(fact, facts) {
			return false
		}
	}
	return true
}

func factHolds(fact config.ChainWhenFact, facts Facts) bool {
	switch {
	case fact.JudgePending != "":
		return facts.Judges[fact.JudgePending].Pending
	case fact.JudgeAction != "":
		return facts.Judges[fact.JudgeAction].Action == fact.Is
	default:
		leaf := config.DoneWhenLeaf{
			Check: fact.Check,
			Eq:    fact.Eq,
			Ne:    fact.Ne,
			In:    fact.In,
			Gte:   fact.Gte,
			Lte:   fact.Lte,
			Expr:  fact.Expr,
		}
		return task.CheckLeafStatus(leaf, facts.State) == task.DoneSatisfied
	}
}
