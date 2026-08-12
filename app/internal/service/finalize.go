package service

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cradel-dev/cradel/app/internal/config"
	"github.com/cradel-dev/cradel/app/internal/domain"
	"github.com/cradel-dev/cradel/app/internal/state"
	"github.com/cradel-dev/cradel/app/internal/task"
)

// FinalizeTaskParams are the inputs to FinalizeTask (`sennit task finalize`).
type FinalizeTaskParams struct {
	Instance    string
	SessionName string
}

// FinalizeTaskResult reports the finalization outcome.
type FinalizeTaskResult struct {
	SessionName string `json:"session_name"`
	Instance    string `json:"instance"`
	ResourceID  string `json:"resource_id,omitempty"`
	Definition  string `json:"resource_definition,omitempty"`
	Finalized   bool   `json:"finalized"`
}

// FinalizeTask is the generic finalization step ADR "goal-as-task" D4
// requires between a task instance's done_when being satisfied and its
// teardown: reconfirm the instance's done_when is satisfied at the current
// revision, then let the bound resource's definition record completion (if it
// declares a `finalize` script — no OKF/local-okf definition exists yet, so
// today this is almost always a no-op outside tests). It is "gate + record"
// only — it never tears the instance down; the caller runs `sennit task cleanup`
// separately once it's done observing the finalized instance. `cleanup`
// itself stays unaware of any of this (it never gains completion semantics —
// abort/destroy must still just tear down).
//
// Refuses (no record, instance untouched) when done_when is not currently
// satisfied — finalization must never be the thing that forces completion.
//
// Dynamic outputs are refreshed first, exactly as `tick` does before it
// evaluates: reconfirming against merely-persisted (possibly stale) outputs
// would let a revision approved before the last refresh — or before a new
// commit landed — pass a check it should no longer pass, and finalize an
// instance whose resource has since moved on.
func FinalizeTask(cfg *config.Config, store *state.Store, params FinalizeTaskParams) (*FinalizeTaskResult, error) {
	if strings.TrimSpace(params.Instance) == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "instance is required"}
	}
	sessionName := params.SessionName
	if sessionName == "" {
		sessionName = os.Getenv("SENNIT_SESSION_NAME")
	}
	if sessionName == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "no session in scope: run inside a sennit session pane (SENNIT_SESSION_NAME) or pass --session"}
	}

	// A failed individual output fetch is not a top-level error (tick/check
	// tolerate it, leaving the prior value in place — wiki task-model.md:
	// "if a re-fetch fails, the previous value is left as-is"). finalize cannot make
	// the same trade: it must reconfirm at the CURRENT revision, so a fetch
	// that just failed leaves that reconfirmation unproven, not merely stale.
	// Fail closed rather than silently evaluate against the untouched old
	// value as if it were freshly confirmed.
	refreshed, err := RefreshInstanceOutputs(cfg, store, sessionName, params.Instance)
	if err != nil {
		return nil, err
	}
	if failed := failedOutputNames(refreshed); len(failed) > 0 {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("instance %q: dynamic output(s) %v failed to refresh; finalize refuses to reconfirm against stale values", params.Instance, failed)}
	}

	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return nil, err
	}
	st := session.Tasks[params.Instance]
	if st == nil {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("instance %q not found in session %s", params.Instance, resolvedName)}
	}

	defs, err := cfg.LoadTaskDefinitions(session.WorktreePath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	def := defs[taskIDForInstance(params.Instance, st)]
	dw, derr := effectiveDoneWhen(def.DoneWhen, st)
	if derr != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: derr.Error()}
	}
	if dw == nil {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("instance %q declares no done_when; finalize has nothing to reconfirm", params.Instance)}
	}

	allSessions := store.All()
	eval := task.EvaluateTaskDoneWhenWithContext(dw, st.Outputs, doneWhenEvalContext(resolvedName, st, allSessions))
	if eval.Overall != task.DoneSatisfied {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("instance %q done_when is %s, not satisfied; finalize refuses to record completion or clean up", params.Instance, eval.Overall)}
	}

	resourceID := st.Resource
	if resourceID == "" {
		resourceID = session.ResourceID
	}
	result := &FinalizeTaskResult{SessionName: resolvedName, Instance: params.Instance, ResourceID: resourceID}

	if resourceID != "" {
		resDefs, rerr := cfg.LoadResourceDefs()
		if rerr != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: rerr.Error()}
		}
		ran, matched, ferr := task.FinalizeResource(resDefs, task.FinalizeResourceParams{
			ResourceID:  resourceID,
			Instance:    params.Instance,
			SessionName: resolvedName,
			Revision:    currentRevision(st.Outputs),
			Judges:      judgeEvidenceFromLeaves(eval.Leaves),
		})
		if ferr != nil {
			return nil, &Error{Code: ErrExecutionFailed, Message: ferr.Error()}
		}
		result.Finalized = ran
		result.Definition = matched.ID
	}

	if err := store.Update(resolvedName, func(s *domain.Session) error {
		if cur := s.Tasks[params.Instance]; cur != nil {
			cur.FinalizedAt = time.Now()
		}
		s.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("persist finalized_at: %v", err)}
	}

	return result, nil
}

// failedOutputNames returns the names of any dynamic outputs whose refresh
// fetch failed, in the order RefreshInstanceOutputs reported them.
func failedOutputNames(results []OutputRefreshResult) []string {
	var out []string
	for _, r := range results {
		if r.Error != "" {
			out = append(out, r.Name)
		}
	}
	return out
}

// judgeEvidenceFromLeaves collects the satisfied judge leaves' evidence from a
// done_when evaluation, for the resource's finalize script to cite (who
// approved what, at which revision).
func judgeEvidenceFromLeaves(leaves []task.DoneLeafResult) []task.FinalizeJudgeEvidence {
	var out []task.FinalizeJudgeEvidence
	for _, leaf := range leaves {
		if leaf.Kind != "judge" {
			continue
		}
		out = append(out, task.FinalizeJudgeEvidence{
			ID:               leaf.ID,
			Reason:           leaf.Reason,
			Revision:         leaf.Revision,
			ReviewerSession:  leaf.ReviewerSession,
			ReviewerWorkflow: leaf.ReviewerWorkflow,
			Relation:         leaf.Relation,
		})
	}
	return out
}
