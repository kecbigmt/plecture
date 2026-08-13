package service

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

const outputKeyRevision = "revision"
const outputKeyMergeableState = "mergeable_state"

type JudgeParams struct {
	SessionName     string
	Instance        string
	LeafID          string
	Action          string
	Reason          string
	Revision        string
	ReviewerSession string
}

type JudgeResult struct {
	SessionName     string `json:"session_name"`
	Instance        string `json:"instance"`
	LeafID          string `json:"leaf_id"`
	Action          string `json:"action"`
	Revision        string `json:"revision"`
	ReviewerSession string `json:"reviewer_session,omitempty"`
}

func RecordJudge(cfg *config.Config, store *state.Store, params JudgeParams) (*JudgeResult, error) {
	if strings.TrimSpace(params.Instance) == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "task instance is required"}
	}
	if strings.TrimSpace(params.LeafID) == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "judge leaf id is required"}
	}
	if params.Action != task.JudgeActionApprove && params.Action != task.JudgeActionRequestChanges {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("judge action must be %q or %q", task.JudgeActionApprove, task.JudgeActionRequestChanges)}
	}
	if strings.TrimSpace(params.Reason) == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "reason is required"}
	}
	sessionName := params.SessionName
	if sessionName == "" {
		sessionName = os.Getenv("PLECT_SESSION_NAME")
	}
	if sessionName == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "no session in scope: pass --session or run inside a plect session pane"}
	}
	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return nil, err
	}
	st := session.Tasks[params.Instance]
	if st == nil {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("instance %q not found in session %s", params.Instance, resolvedName)}
	}
	revision := params.Revision
	if revision == "" {
		revision = currentRevision(st.Outputs)
	}
	if revision == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("revision is required because instance %q has no %q output", params.Instance, outputKeyRevision)}
	}
	reviewer := params.ReviewerSession
	if reviewer == "" {
		reviewer = os.Getenv("PLECT_SESSION_NAME")
	}
	if reviewer == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "reviewer session is required: pass --reviewer-session or run inside a reviewer plect session pane"}
	}
	allSessions := store.All()
	reviewerWorkflow := ""
	if rs := allSessions[reviewer]; rs != nil {
		reviewerWorkflow = rs.Workflow
	}
	relation := string(domain.RelationFromTarget(allSessions, resolvedName, reviewer))
	defs, err := cfg.LoadTaskDefinitions(session.WorkdirPath)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	def := defs[taskIDForInstance(params.Instance, st)]
	dw, err := effectiveDoneWhen(def.DoneWhen, st)
	if err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}
	if _, ok := findJudgeLeaf(dw, params.LeafID); !ok && dw != nil {
		return nil, &Error{Code: ErrInvalidInput, Message: fmt.Sprintf("judge leaf %q not found on instance %q", params.LeafID, params.Instance)}
	}
	// self-review is structurally rejected regardless of leaf policy: a session
	// can never satisfy its own judge leaf. Which relations a leaf accepts is a
	// projection-time policy (relation_not_accepted), not a record-time bar.
	if reviewer == resolvedName {
		return nil, &Error{Code: ErrInvalidInput, Message: "judge rejects self-review: a session cannot satisfy its own judge leaf"}
	}
	judge := &contract.DoneWhenJudge{
		LeafID:           params.LeafID,
		Action:           params.Action,
		Reason:           params.Reason,
		Revision:         revision,
		TargetSession:    resolvedName,
		Instance:         params.Instance,
		ReviewerSession:  reviewer,
		ReviewerWorkflow: reviewerWorkflow,
		Relation:         relation,
		CreatedAt:        time.Now(),
	}

	if err := store.Update(resolvedName, func(s *domain.Session) error {
		cur := s.Tasks[params.Instance]
		if cur == nil || cur.Status == contract.TaskStatusCleaned {
			return fmt.Errorf("instance %q is not live in session %s", params.Instance, resolvedName)
		}
		if cur.DoneWhen == nil {
			cur.DoneWhen = &contract.DoneWhenState{}
		}
		if cur.DoneWhen.Judges == nil {
			cur.DoneWhen.Judges = map[string]*contract.DoneWhenJudge{}
		}
		cur.DoneWhen.Judges[params.LeafID] = judge
		s.UpdatedAt = judge.CreatedAt
		return nil
	}); err != nil {
		return nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
	}

	// Builtin tick trigger (wiki verification-gate.md): a recorded judge ticks
	// the *target* session even with no `[tick]` declared, because judge is
	// plect's own concept. Best-effort like recordLifecycle — a failed append
	// must not unwind the verdict that was already durably recorded above.
	recordJudgeRecorded(store, resolvedName, judge)

	return &JudgeResult{
		SessionName:     resolvedName,
		Instance:        params.Instance,
		LeafID:          params.LeafID,
		Action:          params.Action,
		Revision:        revision,
		ReviewerSession: reviewer,
	}, nil
}

func findJudgeLeaf(dw *config.DoneWhen, id string) (config.DoneWhenLeaf, bool) {
	if dw == nil {
		return config.DoneWhenLeaf{}, false
	}
	for i, leaf := range dw.All {
		if strings.TrimSpace(leaf.Judge) == "" {
			continue
		}
		if task.JudgeLeafID(i, leaf) == id {
			return leaf, true
		}
	}
	return config.DoneWhenLeaf{}, false
}

func judgeInputs(src map[string]*contract.DoneWhenJudge, workSession string, sessions map[string]*domain.Session) map[string]task.Judge {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]task.Judge, len(src))
	for id, v := range src {
		if v == nil {
			continue
		}
		// Relation presence marks the record shape: every new record stamps a
		// non-empty relation (RelationFromTarget never returns empty), so its
		// stamped fields are record-time facts honored verbatim — including a
		// legitimately empty reviewer workflow. Only a legacy record (written
		// before the fields existed, so no relation) is filled from the live
		// tree, which would otherwise let a new verdict's projection drift.
		relation := v.Relation
		reviewerWorkflow := v.ReviewerWorkflow
		if relation == "" {
			relation = string(domain.RelationFromTarget(sessions, workSession, v.ReviewerSession))
			if s := sessions[v.ReviewerSession]; s != nil {
				reviewerWorkflow = s.Workflow
			}
		}
		out[id] = task.Judge{
			LeafID:           v.LeafID,
			Action:           v.Action,
			Reason:           v.Reason,
			Revision:         v.Revision,
			ReviewerSession:  v.ReviewerSession,
			ReviewerWorkflow: reviewerWorkflow,
			Relation:         relation,
		}
	}
	return out
}

func doneWhenJudges(st *contract.DoneWhenState) map[string]*contract.DoneWhenJudge {
	if st == nil {
		return nil
	}
	return st.Judges
}

func doneWhenEvalContext(sessionName string, st *contract.TaskState, sessions map[string]*domain.Session) task.DoneWhenEvalContext {
	return task.DoneWhenEvalContext{
		WorkSession:     sessionName,
		CurrentRevision: currentRevision(st.Outputs),
		Judges:          judgeInputs(doneWhenJudges(st.DoneWhen), sessionName, sessions),
	}
}

func effectiveDoneWhen(base *config.DoneWhen, st *contract.TaskState) (*config.DoneWhen, error) {
	if st == nil || len(st.ExtraDoneWhen) == 0 {
		return base, nil
	}
	var extra config.DoneWhen
	if err := json.Unmarshal(st.ExtraDoneWhen, &extra); err != nil {
		return nil, fmt.Errorf("instance %q extra_done_when: %w", st.Name, err)
	}
	if err := extra.Validate(); err != nil {
		return nil, fmt.Errorf("instance %q extra_done_when: %w", st.Name, err)
	}
	if base == nil {
		return &extra, nil
	}
	merged := *base
	merged.All = make([]config.DoneWhenLeaf, 0, len(base.All)+len(extra.All))
	merged.All = append(merged.All, base.All...)
	merged.All = append(merged.All, extra.All...)
	if merged.Budget == nil {
		merged.Budget = extra.Budget
	}
	return &merged, nil
}

func currentRevision(outputs map[string]any) string {
	if len(outputs) == 0 {
		return ""
	}
	v, ok := outputs[outputKeyRevision]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}
