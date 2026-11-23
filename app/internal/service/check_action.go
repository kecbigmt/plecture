package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kecbigmt/plecture/app/internal/domain"
	"github.com/kecbigmt/plecture/app/internal/task"
	contract "github.com/kecbigmt/plecture/contracts/state"
)

func checkActionForResult(sessionName, instance, resource string, heartbeatBudget int, st *contract.TaskState, result task.DoneWhenResult, trigger TickTrigger, liveChildren []LiveChild) CheckAction {
	heartbeatTicks := 0
	heartbeatEscalations := 0
	if st.DoneWhen != nil {
		heartbeatTicks = st.DoneWhen.HeartbeatTicks
		heartbeatEscalations = st.DoneWhen.HeartbeatEscalations
	}
	fingerprint := checkFingerprint(result)

	if result.Overall == task.DoneSatisfied {
		return CheckAction{
			SessionName:      sessionName,
			Instance:         instance,
			Action:           "satisfied",
			HeartbeatBudget:  heartbeatBudget,
			Summary:          fmt.Sprintf("done_when satisfied for %s", instance),
			Fingerprint:      fingerprint,
			HeartbeatChanged: true,
		}
	}

	unmetItems := unsatisfiedLeafItems(result)
	items := unmetItemSummaries(unmetItems)
	if result.Overall == task.DonePending {
		unmetItems = pendingJudgeItems(result)
		items = unmetItemSummaries(unmetItems)
	}

	nextHeartbeatTicks := heartbeatTicks
	consumedHeartbeat := false
	if trigger == TickTriggerHeartbeat {
		nextHeartbeatTicks++
		consumedHeartbeat = true
	}
	if len(unmetItems) == 0 && result.Overall == task.DonePending {
		return CheckAction{
			SessionName:      sessionName,
			Instance:         instance,
			Action:           "wait",
			HeartbeatTicks:   nextHeartbeatTicks,
			HeartbeatBudget:  heartbeatBudget,
			Fingerprint:      fingerprint,
			HeartbeatChanged: consumedHeartbeat,
		}
	}

	if heartbeatBudget > 0 && consumedHeartbeat && nextHeartbeatTicks >= heartbeatBudget {
		nextEscalation := heartbeatEscalations + 1
		body := fmt.Sprintf("done_when remains unmet for %s after %d/%d heartbeat tick(s).\n\nUnmet items:\n%s", instance, nextHeartbeatTicks, heartbeatBudget, unmetItemBulletList(unmetItems))
		return CheckAction{
			SessionName:         sessionName,
			Instance:            instance,
			Action:              "escalate",
			HeartbeatTicks:      nextHeartbeatTicks,
			HeartbeatBudget:     heartbeatBudget,
			HeartbeatEscalation: nextEscalation,
			HeartbeatChanged:    true,
			Items:               items,
			UnmetItems:          unmetItems,
			Summary:             fmt.Sprintf("done_when non-convergence for %s", instance),
			Body:                body,
			Fingerprint:         fingerprint,
			EscalationKind:      escalationKindDoneWhenNonConvergence,
		}
	}

	if result.Overall == task.DonePending {
		judgeCmds := judgeCommands(sessionName, instance, unmetItems)
		body := reviewRequiredBody(instance, nextHeartbeatTicks, heartbeatBudget, unmetItems, judgeCmds)
		return CheckAction{
			SessionName:      sessionName,
			Instance:         instance,
			Action:           "review_required",
			HeartbeatTicks:   nextHeartbeatTicks,
			HeartbeatBudget:  heartbeatBudget,
			HeartbeatChanged: consumedHeartbeat,
			Items:            items,
			UnmetItems:       unmetItems,
			Summary:          fmt.Sprintf("done_when review required for %s", instance),
			Body:             body,
			JudgeCommands:    judgeCmds,
			Fingerprint:      fingerprint,
		}
	}
	body := fmt.Sprintf("done_when is unsatisfied for %s (heartbeat budget %s).\n\nAddress these unmet items:\n%s", instance, heartbeatBudgetText(nextHeartbeatTicks, heartbeatBudget), unmetItemBulletList(unmetItems))
	if len(liveChildren) > 0 {
		body += "\n\n" + liveChildrenBulletList(liveChildren)
	}
	return CheckAction{
		SessionName:      sessionName,
		Instance:         instance,
		Action:           "kick",
		HeartbeatTicks:   nextHeartbeatTicks,
		HeartbeatBudget:  heartbeatBudget,
		HeartbeatChanged: consumedHeartbeat,
		Items:            items,
		UnmetItems:       unmetItems,
		Summary:          fmt.Sprintf("done_when unsatisfied for %s", instance),
		Body:             body,
		Fingerprint:      fingerprint,
		LiveChildren:     liveChildren,
	}
}

func judgeCommands(sessionName, instance string, items []CheckUnmetItem) []string {
	var out []string
	for _, item := range items {
		if item.Kind != "judge" || item.ID == "" {
			continue
		}
		out = append(out,
			judgeCommand("approve", sessionName, instance, item.ID),
			judgeCommand("request-changes", sessionName, instance, item.ID),
		)
	}
	return out
}

func judgeCommand(action, sessionName, instance, id string) string {
	return fmt.Sprintf("plect judge %s %q %q %q --reason %q", action, sessionName, instance, id, "<reason>")
}

func checkFingerprint(result task.DoneWhenResult) string {
	parts := make([]string, 0, len(result.Leaves)+1)
	parts = append(parts, string(result.Overall))
	for _, leaf := range result.Leaves {
		parts = append(parts, strings.Join([]string{
			leaf.Kind,
			leaf.ID,
			leaf.Output,
			string(leaf.Status),
			leaf.Value,
			leaf.Action,
			leaf.Revision,
			leaf.CurrentRevision,
			leaf.JudgeSession,
			leaf.JudgeWorkflow,
			leaf.Relation,
			leaf.Reason,
			leaf.PendingReason,
		}, "\x00"))
	}
	return strings.Join(parts, "\x01")
}

func unsatisfiedLeafItems(result task.DoneWhenResult) []CheckUnmetItem {
	var out []CheckUnmetItem
	for _, leaf := range result.Leaves {
		if leaf.Status != task.DoneUnsatisfied {
			continue
		}
		out = append(out, checkUnmetItem(leaf))
	}
	slices.SortFunc(out, compareCheckUnmetItem)
	return out
}

func pendingJudgeItems(result task.DoneWhenResult) []CheckUnmetItem {
	var out []CheckUnmetItem
	for _, leaf := range result.Leaves {
		if leaf.Kind != "judge" || leaf.Status != task.DonePending {
			continue
		}
		out = append(out, checkUnmetItem(leaf))
	}
	slices.SortFunc(out, compareCheckUnmetItem)
	return out
}

func checkUnmetItem(leaf task.DoneLeafResult) CheckUnmetItem {
	return CheckUnmetItem{
		Kind:            leaf.Kind,
		Expr:            leaf.Expr,
		Status:          leaf.Status,
		ID:              leaf.ID,
		Output:          leaf.Output,
		Value:           leaf.Value,
		Observed:        leaf.Observed,
		Action:          leaf.Action,
		Reason:          leaf.Reason,
		PendingReason:   leaf.PendingReason,
		Revision:        leaf.Revision,
		CurrentRevision: leaf.CurrentRevision,
		JudgeSession:    leaf.JudgeSession,
		JudgeWorkflow:   leaf.JudgeWorkflow,
		Relation:        leaf.Relation,
	}
}

func compareCheckUnmetItem(a, b CheckUnmetItem) int {
	return strings.Compare(unmetItemSummary(a), unmetItemSummary(b))
}

func unmetItemSummaries(items []CheckUnmetItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = unmetItemSummary(item)
	}
	return out
}

func unmetItemSummary(item CheckUnmetItem) string {
	summary := item.Expr
	if item.Kind == "check" {
		if item.Observed {
			summary = fmt.Sprintf("%s (observed %s)", summary, item.Value)
		} else {
			summary = fmt.Sprintf("%s (unobserved)", summary)
		}
	}
	if item.Kind == "judge" {
		if item.ID != "" {
			summary = fmt.Sprintf("%s (%s)", summary, item.ID)
		}
		switch {
		case item.PendingReason != "":
			summary = fmt.Sprintf("%s: %s", summary, item.PendingReason)
		case item.Reason != "":
			summary = fmt.Sprintf("%s: %s", summary, item.Reason)
		}
	}
	return summary
}

func unmetItemBulletList(items []CheckUnmetItem) string {
	if len(items) == 0 {
		return "- (none)"
	}
	lines := make([]string, len(items))
	for i, item := range items {
		lines[i] = "- " + unmetItemSummary(item)
		if item.Kind == "judge" {
			var details []string
			if item.Action != "" {
				details = append(details, "action="+item.Action)
			}
			if item.JudgeSession != "" {
				details = append(details, "judge="+item.JudgeSession)
			}
			if item.JudgeWorkflow != "" {
				details = append(details, "judge_workflow="+item.JudgeWorkflow)
			}
			if item.Relation != "" {
				details = append(details, "relation="+item.Relation)
			}
			if item.Revision != "" {
				details = append(details, "judge_revision="+item.Revision)
			}
			if item.CurrentRevision != "" {
				details = append(details, "current_revision="+item.CurrentRevision)
			}
			if len(details) > 0 {
				lines[i] += "\n  " + strings.Join(details, " ")
			}
		}
	}
	return strings.Join(lines, "\n")
}

func liveChildrenBulletList(children []LiveChild) string {
	lines := make([]string, 0, len(children)+1)
	lines = append(lines, "Live children:")
	for _, c := range children {
		lines = append(lines, fmt.Sprintf("- %s (run=%s health=%s last_tick=%dm ago)", c.Name, c.Run, orUndeclared(c.Health), c.MinutesSinceTick))
	}
	return strings.Join(lines, "\n")
}

func orUndeclared(h domain.HealthState) domain.HealthState {
	if h == "" {
		return domain.HealthUndeclared
	}
	return h
}

func reviewRequiredBody(instance string, heartbeatTicks, heartbeatBudget int, items []CheckUnmetItem, judgeCmds []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "done_when needs independent review for %s (heartbeat budget %s).\n\n", instance, heartbeatBudgetText(heartbeatTicks, heartbeatBudget))
	b.WriteString("Review these judge leaves:\n")
	b.WriteString(unmetItemBulletList(items))
	if len(judgeCmds) > 0 {
		b.WriteString("\n\nReviewer must record one action per judge leaf:\n")
		for _, cmd := range judgeCmds {
			fmt.Fprintf(&b, "- %s\n", cmd)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func heartbeatBudgetText(heartbeatTicks, heartbeatBudget int) string {
	if heartbeatBudget == 0 {
		return fmt.Sprintf("%d/unbounded", heartbeatTicks)
	}
	return fmt.Sprintf("%d/%d", heartbeatTicks, heartbeatBudget)
}
