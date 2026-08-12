package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/plecture/plect/app/internal/chain"
	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/domain"
	"github.com/plecture/plect/app/internal/state"
	"github.com/plecture/plect/app/internal/task"
)

// Blocked-reason values for a chain that did not fire this evaluation.
const (
	chainBlockedWhenUnmet          = "when_unmet"
	chainBlockedOutputsMissing     = "outputs_missing"
	chainBlockedInvalidBindings    = "invalid_bindings"
	chainBlockedWorkflowUnresolved = "workflow_unresolved"
)

// ChainSpawn is one (chain, instance) evaluation: whether the chain fired and,
// if so, the spawned reviewer's placement and identity. CheckSession (plect
// status / plect_check) reports it as a dry-run plan (Spawned always false);
// TickSession (plect tick) spawns each fired, not-already-active entry and
// fills Spawned/TargetSession in with the result.
type ChainSpawn struct {
	ChainID        string         `json:"chain_id"`
	Task           string         `json:"task,omitempty"`
	Instance       string         `json:"instance"`
	Workflow       string         `json:"workflow"`
	Placement      string         `json:"placement"`
	Resource       string         `json:"resource,omitempty"`
	Tag            string         `json:"tag,omitempty"`
	ParentSession  string         `json:"parent_session,omitempty"`
	TargetSession  string         `json:"target_session,omitempty"`
	Fired          bool           `json:"fired"`
	BlockedReason  string         `json:"blocked_reason,omitempty"`
	MissingOutputs []string       `json:"missing_outputs,omitempty"`
	Inputs         map[string]any `json:"inputs,omitempty"`
	Spawned        bool           `json:"spawned,omitempty"`
	AlreadyActive  bool           `json:"already_active,omitempty"`
	KickDelivered  bool           `json:"kick_delivered,omitempty"`
	KickDebounced  bool           `json:"kick_debounced,omitempty"`
	Warnings       []string       `json:"warnings,omitempty"`
}

// evalChain decides one (chain, instance) pair: whether it fires, the resolved
// placement/identity of the session a fire would spawn, and — on a fire — the
// downstream inputs its `[chains.inputs]` bindings resolve to. The bindings are
// validated twice: each wired output must be published by the upstream output
// contract (upstreamOutputs), and the rendered inputs must satisfy the spawned
// workflow's inputs contract. A binding that fails either check blocks the fire
// (chainBlockedInvalidBindings) rather than spawning a reviewer with unwired or
// contract-violating inputs.
func evalChain(cfg *config.Config, store *state.Store, def config.ChainDefinition, workName string, session *domain.Session, instance, resource string, facts chain.Facts, upstreamOutputs []string) ChainSpawn {
	placement := def.EffectivePlacement()
	parent := placementParent(placement, session, workName)
	tag := chainSpawnTag(def.ID, instance)
	workflow, workflowErr := chain.RenderWorkflow(def.Workflow, chain.WorkFacts{
		Resource:        resource,
		Session:         workName,
		Workflow:        session.Workflow,
		Instance:        instance,
		Outputs:         facts.Outputs,
		PendingJudgeIDs: pendingJudgeIDs(facts),
	})
	sp := ChainSpawn{
		ChainID:       def.ID,
		Instance:      instance,
		Workflow:      def.Workflow,
		Placement:     placement,
		Resource:      resource,
		Tag:           tag,
		ParentSession: parent,
	}
	if workflowErr != nil {
		sp.BlockedReason = chainBlockedWorkflowUnresolved
		sp.Warnings = append(sp.Warnings, fmt.Sprintf("workflow template could not be rendered: %v", workflowErr))
		return sp
	}
	sp.Workflow = workflow

	missing, err := chain.MissingOutputs(def.Inputs, facts.Outputs)
	if err != nil {
		sp.BlockedReason = chainBlockedInvalidBindings
		sp.Warnings = append(sp.Warnings, fmt.Sprintf("input bindings could not be parsed: %v", err))
		return sp
	}
	undeclared, err := chain.UndeclaredWiredOutputs(def.Inputs, upstreamOutputs)
	if err != nil {
		sp.BlockedReason = chainBlockedInvalidBindings
		sp.Warnings = append(sp.Warnings, fmt.Sprintf("input bindings could not be parsed: %v", err))
		return sp
	}
	if len(undeclared) > 0 {
		sp.BlockedReason = chainBlockedInvalidBindings
		sp.Warnings = append(sp.Warnings, fmt.Sprintf("wired outputs %v are not published by the upstream output contract %v", undeclared, upstreamOutputs))
		return sp
	}

	switch {
	case !chain.WhenSatisfied(def.When, facts):
		sp.BlockedReason = chainBlockedWhenUnmet
		return sp
	case len(missing) > 0:
		sp.BlockedReason = chainBlockedOutputsMissing
		sp.MissingOutputs = missing
		return sp
	}

	if len(def.Inputs) > 0 {
		rendered, rErr := chain.RenderInputs(def.Inputs, chain.WorkFacts{
			Resource:        resource,
			Session:         workName,
			Workflow:        session.Workflow,
			Instance:        instance,
			Outputs:         facts.Outputs,
			PendingJudgeIDs: pendingJudgeIDs(facts),
		})
		if rErr != nil {
			sp.BlockedReason = chainBlockedInvalidBindings
			sp.Warnings = append(sp.Warnings, fmt.Sprintf("input bindings could not be rendered: %v", rErr))
			return sp
		}
		resolved, vErr := resolveSessionInputs(cfg, session.WorktreePath, workflow, rendered)
		if vErr != nil {
			sp.BlockedReason = chainBlockedInvalidBindings
			sp.Warnings = append(sp.Warnings, fmt.Sprintf("resolved inputs violate workflow %q inputs contract: %s", workflow, vErr.Message))
			return sp
		}
		sp.Inputs = resolved
	}

	// The resolved workflow must actually be defined before this reports
	// fired: otherwise a typoed or stale template result reports fired=true
	// every tick, and each spawn attempt fails identically at Up() — a
	// repeating, silent-in-effect failure rather than the explicit error AC1
	// requires. Checked here (not at render time) so it only blocks a chain
	// that would otherwise really fire.
	workflows, wfErr := cfg.LoadWorkflows(session.WorktreePath)
	if wfErr != nil {
		sp.BlockedReason = chainBlockedWorkflowUnresolved
		sp.Warnings = append(sp.Warnings, fmt.Sprintf("load workflows: %v", wfErr))
		return sp
	}
	if _, ok := workflows[workflow]; !ok {
		sp.BlockedReason = chainBlockedWorkflowUnresolved
		sp.Warnings = append(sp.Warnings, fmt.Sprintf("chain %q resolves to workflow %q, which is not defined (add .plect/workflows/%s.toml)", def.ID, workflow, workflow))
		return sp
	}

	sp.Fired = true
	if name, err := resolveSpawnSessionName(cfg, resource, workflow, tag); err == nil && name != "" {
		sp.TargetSession = name
		if store.Get(name) != nil {
			sp.AlreadyActive = true
		}
	}
	return sp
}

// pendingJudgeIDs lists, sorted, the judge leaf ids with no usable verdict at
// the current revision — the `.Work.done_when.pending_judge_ids` a chain binding
// hands the spawned reviewer so it knows which leaves to judge.
func pendingJudgeIDs(facts chain.Facts) []string {
	var ids []string
	for id, jf := range facts.Judges {
		if jf.Pending {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// buildChainFacts projects the done_when evaluation into the raw-fact view a
// chain's `when` reads: outputs verbatim, judge leaves reduced to
// pending/current-action.
func buildChainFacts(outputs map[string]any, result task.DoneWhenResult) chain.Facts {
	judges := make(map[string]chain.JudgeFact)
	for _, leaf := range result.Leaves {
		if leaf.Kind != "judge" {
			continue
		}
		jf := chain.JudgeFact{Pending: leaf.Status == task.DonePending}
		switch leaf.Status {
		case task.DoneSatisfied:
			jf.Action = task.JudgeActionApprove
		case task.DoneUnsatisfied:
			jf.Action = task.JudgeActionRequestChanges
		}
		judges[leaf.ID] = jf
	}
	return chain.Facts{Outputs: outputs, Judges: judges}
}

// placementParent maps a placement to the spawned session's parent: a child
// reviewer is parented under the work session; a sibling under the work
// session's own parent — or, when the work session has none, under its own
// implicit root (domain.ImplicitRootParent), so a parentless work session
// (e.g. an owner orchestrator pursuing a goal) can still spawn an independent
// sibling reviewer instead of sibling placement being unreachable there.
func placementParent(placement string, session *domain.Session, workName string) string {
	if placement == config.ChainPlacementChild {
		return workName
	}
	if session.ParentSession != "" {
		return session.ParentSession
	}
	return domain.ImplicitRootParent(workName)
}

// chainSpawnTag folds the chain id and instance into the spawned reviewer's
// session tag, keeping (chain, instance) pairs from colliding on one session.
func chainSpawnTag(chainID, instance string) string {
	return sanitizeTag(chainID + "-" + instance)
}

func sanitizeTag(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// resolveSpawnSessionName derives, offline, the session name a fire's spawn
// would resolve to — mirroring Up's name derivation (the provider resolver) so
// the idempotency check sees the same name Up will. A resource no resolver
// matches is an identity dispatch, whose name is the resource itself.
func resolveSpawnSessionName(cfg *config.Config, resource, workflow, tag string) (string, error) {
	disp, matched, err := dispatchResource(cfg, workflow, resource)
	if err != nil {
		return "", err
	}
	if matched {
		name := disp.Name
		if tag != "" {
			name = name + "+" + tag
		}
		return name, nil
	}
	return resource, nil
}
