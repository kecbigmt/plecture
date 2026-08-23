package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Chain placement values: where the spawned workflow session attaches in the
// session tree relative to the work session that triggered it.
//
//   - sibling: an independent reviewer the parent spawns (the work session's
//     own parent becomes the reviewer's parent). Default.
//   - child:   a reviewer parented under the work session itself. Opt-in.
//
// These mirror the judge-leaf relation policy (domain.AssignableJudgeRelation):
// a sibling reviewer's verdict is accepted by the default `["sibling","parent"]`
// policy, a child reviewer's only when the leaf opts into `child`.
const (
	ChainPlacementSibling = "sibling"
	ChainPlacementChild   = "child"
)

// Chain judge-action vocabulary. These match the recorded judge action values
// (`plect judge approve` / `request-changes`); config validation rejects anything
// else so a `judge_action` fact can never silently never-match. Duplicated here
// rather than imported from the task package because task imports config, not
// the reverse.
const (
	chainJudgeActionApprove        = "approve"
	chainJudgeActionRequestChanges = "request_changes"
)

// chainIDRE constrains a chain id to the tag charset so it can be folded into a
// spawned reviewer's session tag without escaping.
var chainIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ChainWhen is the chain's trigger: a conjunction of raw-fact predicates.
type ChainWhen struct {
	All []ChainWhenFact `toml:"all"`
}

// ChainWhenFact is one trigger predicate, evaluated directly against raw facts
// (outputs / judge records / revision) — never a managed gate state. It is one
// of three kinds, exclusively:
//
//   - check: names an output and applies one comparison operator, exactly like
//     a done_when check leaf.
//   - judge_pending: names a judge leaf id; holds when that leaf has no usable
//     verdict at the current revision (missing / stale / self / wrong relation).
//   - judge_action: names a judge leaf id and the action (`is`) its current
//     verdict must carry.
type ChainWhenFact struct {
	// Check names one key by its root path, exactly as a done_when check
	// leaf does.
	Check string   `toml:"check"`
	Eq    *string  `toml:"eq"`
	Ne    *string  `toml:"ne"`
	In    []any    `toml:"in"`
	Gte   *float64 `toml:"gte"`
	Lte   *float64 `toml:"lte"`
	// Expr states a computed predicate over those same roots.
	Expr string `toml:"expr"`

	JudgePending string `toml:"judge_pending"`
	JudgeAction  string `toml:"judge_action"`
	Is           string `toml:"is"`
}

func (f ChainWhenFact) operatorCount() int {
	n := 0
	if f.Eq != nil {
		n++
	}
	if f.Ne != nil {
		n++
	}
	if f.In != nil {
		n++
	}
	if f.Gte != nil {
		n++
	}
	if f.Lte != nil {
		n++
	}
	return n
}

func (f ChainWhenFact) validate() error {
	hasCheck := strings.TrimSpace(f.Check) != ""
	hasPending := strings.TrimSpace(f.JudgePending) != ""
	hasAction := strings.TrimSpace(f.JudgeAction) != ""
	kinds := 0
	for _, set := range []bool{hasCheck, hasPending, hasAction} {
		if set {
			kinds++
		}
	}
	switch {
	case kinds == 0:
		return fmt.Errorf("sets none of `check` / `judge_pending` / `judge_action`; exactly one is required")
	case kinds > 1:
		return fmt.Errorf("sets more than one of `check` / `judge_pending` / `judge_action`; exactly one is allowed")
	case hasCheck:
		if f.operatorCount() != 1 {
			return fmt.Errorf("check %q must set exactly one operator (eq/ne/in/gte/lte), got %d", f.Check, f.operatorCount())
		}
		if f.Is != "" {
			return fmt.Errorf("check leaf must not set `is`")
		}
	case hasPending:
		if f.operatorCount() > 0 {
			return fmt.Errorf("judge_pending must not set comparison operators")
		}
		if f.Is != "" {
			return fmt.Errorf("judge_pending must not set `is`")
		}
	case hasAction:
		if f.operatorCount() > 0 {
			return fmt.Errorf("judge_action must not set comparison operators")
		}
		switch f.Is {
		case chainJudgeActionApprove, chainJudgeActionRequestChanges:
		default:
			return fmt.Errorf("judge_action requires `is` to be %q or %q", chainJudgeActionApprove, chainJudgeActionRequestChanges)
		}
	}
	return nil
}
