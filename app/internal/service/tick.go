package service

import (
	"fmt"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
)

// TickParams carries SkipRefresh, unlike CheckParams: check never refreshes,
// so only tick needs a way to suppress its (default-on) refresh.
type TickParams struct {
	SessionName string
	SkipRefresh bool
	Observer    task.Observer
}

// TickSession is the Goal Loop actuator (ADR amendment 2026-07-03, story
// PR-C/PR-D): it refreshes outputs (unless SkipRefresh), evaluates done_when
// for each produced task instance, and — unlike CheckSession — carries out
// the result. A round advances only when the observed facts actually changed
// since the last tick (checkActionForResult's fingerprint compare);
// satisfied/escalate push a terminal event to the parent exactly once per
// instance; review_required and kick publish same-session events that drive
// the reviewer/work session. Against that same refreshed fact set, it also
// fires [[chains]]: each chain whose `when` holds and whose wired outputs are
// present spawns its workflow (idempotent — an already-active target is
// reported, not re-spawned), exactly as the chaining wiki's timing section
// requires ("evaluation and firing are... done by sennit tick").
func TickSession(cfg *config.Config, store *state.Store, params TickParams) (*CheckResult, error) {
	resolvedName, computed, chainPlan, warnings, err := evaluateSessionActions(cfg, store, params.SessionName, !params.SkipRefresh)
	if err != nil {
		return nil, err
	}

	// Stamp unconditionally (even when no instance has a computed action): a
	// tick always resets the `stale_when` staleness clock the reactor tracks,
	// whether or not anything was found unsatisfied this round.
	if err := stampLastTick(store, resolvedName); err != nil {
		return nil, err
	}

	var actions []CheckAction
	for _, c := range computed {
		action := c.action
		// Publish before persisting the marker: a publish failure must leave
		// LastAction/fingerprint unadvanced so the next tick retries this same
		// action instead of silently skipping delivery.
		warnings, err := publishTickAction(cfg, store, resolvedName, c.instance, action, c.alreadySatisfied)
		if err != nil {
			return nil, err
		}
		action.Warnings = append(action.Warnings, warnings...)
		actions = append(actions, action)
		if err := persistTickAction(store, resolvedName, c.instance, action); err != nil {
			return nil, err
		}
	}

	chains := make([]ChainSpawn, 0, len(chainPlan))
	for _, sp := range chainPlan {
		if sp.Fired && !sp.AlreadyActive {
			up, err := Up(cfg, store, UpParams{
				Identifier:    sp.Resource,
				Tag:           sp.Tag,
				Workflow:      sp.Workflow,
				Inputs:        sp.Inputs,
				ParentSession: sp.ParentSession,
				Observer:      params.Observer,
			})
			// A spawn failure (e.g. a transient workspace/runtime error) must not
			// discard the done_when actions already published/persisted above,
			// nor the other chains' results — it is reported on this entry only,
			// so the next tick can retry the same (idempotent) fire.
			if err != nil {
				sp.Warnings = append(sp.Warnings, fmt.Sprintf("spawn failed: %v", err))
			} else {
				sp.Spawned = true
				sp.TargetSession = up.SessionName
			}
		} else if sp.Fired && sp.AlreadyActive {
			delivered, err := publishAlreadyActiveChainKick(cfg, store, resolvedName, sp)
			if err != nil {
				sp.Warnings = append(sp.Warnings, fmt.Sprintf("already-active kick failed: %v", err))
			} else if delivered {
				sp.KickDelivered = true
			} else {
				sp.KickDebounced = true
			}
		}
		chains = append(chains, sp)
	}

	return &CheckResult{Actions: actions, Chains: chains, Warnings: warnings}, nil
}
