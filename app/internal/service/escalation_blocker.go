package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
	"github.com/kecbigmt/plect/app/internal/state"
	"github.com/kecbigmt/plect/app/internal/task"
	contract "github.com/kecbigmt/plect/contracts/state"
)

const escalationBlockerTaskID = "escalation_blocker"

func isEscalationBlockerTask(st *contract.TaskState) bool {
	return st != nil && st.TaskID == escalationBlockerTaskID
}

func escalationBlockerInstance(child, instance string) string {
	sum := sha256.Sum256([]byte(child + "\x00" + instance))
	return escalationBlockerTaskID + "_" + hex.EncodeToString(sum[:])[:12]
}

func upsertEscalationBlockerForParent(store *state.Store, child, instance string, action CheckAction) error {
	childSession := store.Get(child)
	if childSession == nil || childSession.ParentSession == "" {
		return nil
	}
	parent := resolveTerminalTarget(childSession.ParentSession)
	if store.Get(parent) == nil {
		return nil
	}
	now := time.Now()
	return store.Update(parent, func(s *domain.Session) error {
		if s.Tasks == nil {
			s.Tasks = map[string]*contract.TaskState{}
		}
		key := escalationBlockerInstance(child, instance)
		for existingKey, st := range s.Tasks {
			if !isEscalationBlockerTask(st) {
				continue
			}
			if outputString(st.Outputs, "child_session") == child && outputString(st.Outputs, "child_instance") == instance {
				key = existingKey
				break
			}
		}
		st := s.Tasks[key]
		if st == nil {
			st = &contract.TaskState{
				Scope:   contract.TaskScopeSession,
				TaskID:  escalationBlockerTaskID,
				Status:  contract.TaskStatusProduced,
				Dynamic: true,
				Name:    key,
				Seq:     task.NextSeq(s.Tasks),
				SetupAt: now,
			}
			s.Tasks[key] = st
		}
		if outputString(st.Outputs, "child_fingerprint") != "" && outputString(st.Outputs, "child_fingerprint") != action.Fingerprint {
			st.DoneWhen = nil
		}
		st.Scope = contract.TaskScopeSession
		st.TaskID = escalationBlockerTaskID
		st.Status = contract.TaskStatusProduced
		st.Dynamic = true
		st.Resource = child
		st.Outputs = map[string]any{
			"child_session":     child,
			"child_instance":    instance,
			"child_fingerprint": action.Fingerprint,
			"child_summary":     action.Summary,
			"child_body":        action.Body,
			"max_rounds":        action.MaxRounds,
		}
		s.UpdatedAt = now
		return nil
	})
}

func checkEscalationBlockerAction(cfg *config.Config, store *state.Store, sessionName, instance string, st *contract.TaskState) CheckAction {
	child := outputString(st.Outputs, "child_session")
	childInstance := outputString(st.Outputs, "child_instance")
	maxRounds := outputInt(st.Outputs, "max_rounds")
	if maxRounds <= 0 {
		maxRounds = 1
	}
	if child == "" || childInstance == "" {
		return escalationBlockerSatisfied(sessionName, instance, maxRounds, "child blocker metadata is incomplete")
	}
	childAction, ok, err := childDoneWhenAction(cfg, store, child, childInstance)
	if err != nil {
		return escalationBlockerUnmet(sessionName, instance, st, maxRounds, blockerChildErrorFingerprint(child, childInstance, err), fmt.Sprintf("child done_when could not be evaluated: %v", err))
	}
	if !ok || childAction.Action == "satisfied" {
		return escalationBlockerSatisfied(sessionName, instance, maxRounds, fmt.Sprintf("child blocker resolved for %s/%s", child, childInstance))
	}
	fingerprint := strings.Join([]string{child, childInstance, childAction.Action, childAction.Fingerprint}, "\x00")
	summary := fmt.Sprintf("child blocker still unmet for %s/%s", child, childInstance)
	if childAction.Summary != "" {
		summary = childAction.Summary
	}
	return escalationBlockerUnmet(sessionName, instance, st, maxRounds, fingerprint, summary)
}

func childDoneWhenAction(cfg *config.Config, store *state.Store, child, instance string) (CheckAction, bool, error) {
	s := store.Get(child)
	if s == nil {
		return CheckAction{}, false, nil
	}
	st := s.Tasks[instance]
	if st == nil || st.Status == contract.TaskStatusCleaned {
		return CheckAction{}, false, nil
	}
	_, computed, _, _, err := evaluateSessionActions(cfg, store, child, false)
	if err != nil {
		return CheckAction{}, false, err
	}
	for _, c := range computed {
		if c.instance == instance {
			return c.action, true, nil
		}
	}
	return CheckAction{}, false, nil
}

func escalationBlockerSatisfied(sessionName, instance string, maxRounds int, summary string) CheckAction {
	return CheckAction{
		SessionName: sessionName,
		Instance:    instance,
		Action:      "satisfied",
		MaxRounds:   maxRounds,
		Summary:     summary,
		Fingerprint: summary,
	}
}

func escalationBlockerUnmet(sessionName, instance string, st *contract.TaskState, maxRounds int, fingerprint, summary string) CheckAction {
	rounds := 0
	lastAction := ""
	lastFingerprint := ""
	if st.DoneWhen != nil {
		rounds = st.DoneWhen.Rounds
		lastAction = st.DoneWhen.LastAction
		lastFingerprint = st.DoneWhen.LastFingerprint
	}
	if lastAction == "escalate" && lastFingerprint == fingerprint {
		return CheckAction{
			SessionName: sessionName,
			Instance:    instance,
			Action:      "sleep",
			Round:       rounds,
			MaxRounds:   maxRounds,
			Items:       []string{summary},
			Summary:     fmt.Sprintf("escalation blocker sleeping for %s", instance),
			Fingerprint: fingerprint,
		}
	}
	if lastFingerprint != "" && lastFingerprint != fingerprint {
		rounds = 0
	}
	if rounds >= maxRounds {
		body := fmt.Sprintf("Escalation blocker exhausted after %d/%d round(s) for %s.\n\n%s", rounds, maxRounds, instance, blockerBody(st, summary))
		return CheckAction{
			SessionName: sessionName,
			Instance:    instance,
			Action:      "escalate",
			Round:       rounds,
			MaxRounds:   maxRounds,
			Items:       []string{summary},
			UnmetItems:  []CheckUnmetItem{blockerUnmetItem(st, summary)},
			Summary:     fmt.Sprintf("escalation blocker exhausted for %s", instance),
			Body:        body,
			Fingerprint: fingerprint,
		}
	}
	nextRound := rounds + 1
	body := fmt.Sprintf("Child session responsibility was transferred to this session (round %s).\n\n%s", roundText(nextRound, maxRounds), blockerBody(st, summary))
	return CheckAction{
		SessionName: sessionName,
		Instance:    instance,
		Action:      "kick",
		Round:       nextRound,
		MaxRounds:   maxRounds,
		Items:       []string{summary},
		UnmetItems:  []CheckUnmetItem{blockerUnmetItem(st, summary)},
		Summary:     fmt.Sprintf("remove child blocker for %s", outputString(st.Outputs, "child_session")),
		Body:        body,
		Fingerprint: fingerprint,
	}
}

func blockerBody(st *contract.TaskState, summary string) string {
	var b strings.Builder
	child := outputString(st.Outputs, "child_session")
	instance := outputString(st.Outputs, "child_instance")
	fmt.Fprintf(&b, "Child: %s\nInstance: %s\nCurrent state: %s", child, instance, summary)
	if original := outputString(st.Outputs, "child_body"); original != "" {
		fmt.Fprintf(&b, "\n\nOriginal escalation:\n%s", original)
	}
	return b.String()
}

func blockerUnmetItem(st *contract.TaskState, summary string) CheckUnmetItem {
	return CheckUnmetItem{
		Kind:     "check",
		Expr:     fmt.Sprintf("child %s/%s blocker resolved", outputString(st.Outputs, "child_session"), outputString(st.Outputs, "child_instance")),
		Status:   task.DoneUnsatisfied,
		Observed: true,
		Value:    summary,
	}
}

func blockerChildErrorFingerprint(child, instance string, err error) string {
	return strings.Join([]string{child, instance, "error", err.Error()}, "\x00")
}

func outputString(outputs map[string]any, key string) string {
	if outputs == nil {
		return ""
	}
	v, ok := outputs[key]
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func outputInt(outputs map[string]any, key string) int {
	if outputs == nil {
		return 0
	}
	switch x := outputs[key].(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(x)
		return n
	default:
		return 0
	}
}
