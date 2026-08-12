package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/plecture/plect/app/internal/config"
	"github.com/plecture/plect/app/internal/task"
	contract "github.com/plecture/plect/contracts/state"
)

func checkActionForResult(sessionName, instance, resource string, dw *config.DoneWhen, st *contract.TaskState, result task.DoneWhenResult) CheckAction {
	maxRounds := doneWhenBudgetMaxRounds(dw)
	rounds := 0
	lastFingerprint := ""
	wasEscalated := false
	lastAutoRevivalRevision := ""
	if st.DoneWhen != nil {
		rounds = st.DoneWhen.Rounds
		lastFingerprint = st.DoneWhen.LastFingerprint
		wasEscalated = st.DoneWhen.LastAction == "escalate"
		lastAutoRevivalRevision = st.DoneWhen.LastAutoRevivalRevision
	}
	fingerprint := checkFingerprint(result)
	sameDoneWhenState := fingerprint != "" && fingerprint == lastFingerprint

	// Automatic post-exhaustion revival: a prior escalation exhausted
	// the round budget, but the resource has since observed a new revision that
	// left one or more judge leaves stale. Rather than re-escalating forever,
	// grant a fresh round budget so the tick can act again, and let the caller
	// (TickSession) deliver the standard re-evaluation kick to each stale
	// leaf's recorded reviewer session. Deduplicated by revision id: the same
	// revision never revives the budget (or kicks a reviewer) twice.
	var revivalRevision string
	var revivalReviewers []RevivalReviewer
	if maxRounds > 0 && wasEscalated && rounds >= maxRounds {
		if rev := currentRevisionFromResult(result); rev != "" && rev != lastAutoRevivalRevision {
			if reviewers := staleJudgeReviewers(result); len(reviewers) > 0 {
				revivalRevision = rev
				revivalReviewers = reviewers
				rounds = 0
				sameDoneWhenState = false
			}
		}
	}

	if result.Overall == task.DoneSatisfied {
		return CheckAction{
			SessionName: sessionName,
			Instance:    instance,
			Action:      "satisfied",
			MaxRounds:   maxRounds,
			Summary:     fmt.Sprintf("done_when satisfied for %s", instance),
			Fingerprint: fingerprint,
		}
	}

	unmetItems := unsatisfiedLeafItems(result)
	items := unmetItemSummaries(unmetItems)
	if result.Overall == task.DonePending {
		unmetItems = pendingJudgeItems(result)
		items = unmetItemSummaries(unmetItems)
		if len(unmetItems) == 0 {
			return CheckAction{SessionName: sessionName, Instance: instance, Action: "wait", MaxRounds: maxRounds, Fingerprint: fingerprint}
		}
	}
	if maxRounds > 0 && !sameDoneWhenState && rounds >= maxRounds {
		body := fmt.Sprintf("done_when exhausted after %d/%d round(s) for %s.\n\nUnmet items:\n%s", rounds, maxRounds, instance, unmetItemBulletList(unmetItems))
		return CheckAction{
			SessionName: sessionName,
			Instance:    instance,
			Action:      "escalate",
			Round:       rounds,
			MaxRounds:   maxRounds,
			Items:       items,
			UnmetItems:  unmetItems,
			Summary:     fmt.Sprintf("done_when exhausted for %s", instance),
			Body:        body,
			Fingerprint: fingerprint,
		}
	}
	nextRound := rounds
	if !sameDoneWhenState {
		nextRound = rounds + 1
	}
	if result.Overall == task.DonePending {
		cmd := reviewerDispatchCommand(resource, instance)
		judgeCmds := judgeCommands(sessionName, instance, unmetItems)
		var warnings []string
		if cmd == "" {
			warnings = append(warnings, "reviewer dispatch command unavailable: task instance has no resource")
			items = append(items, warnings...)
		}
		body := reviewRequiredBody(instance, nextRound, maxRounds, cmd, unmetItems, judgeCmds, warnings)
		return CheckAction{
			SessionName:      sessionName,
			Instance:         instance,
			Action:           "review_required",
			Round:            nextRound,
			MaxRounds:        maxRounds,
			Items:            items,
			UnmetItems:       unmetItems,
			Warnings:         warnings,
			Summary:          fmt.Sprintf("done_when review required for %s", instance),
			Body:             body,
			ReviewerCommand:  cmd,
			JudgeCommands:    judgeCmds,
			Fingerprint:      fingerprint,
			RevivalRevision:  revivalRevision,
			RevivalReviewers: revivalReviewers,
		}
	}
	body := fmt.Sprintf("done_when is unsatisfied for %s (round %s).\n\nAddress these unmet items:\n%s", instance, roundText(nextRound, maxRounds), unmetItemBulletList(unmetItems))
	if hint := mergeableStateHint(st.Outputs); hint != "" {
		body += "\n\n" + hint
	}
	return CheckAction{
		SessionName:      sessionName,
		Instance:         instance,
		Action:           "kick",
		Round:            nextRound,
		MaxRounds:        maxRounds,
		Items:            items,
		UnmetItems:       unmetItems,
		Summary:          fmt.Sprintf("done_when unsatisfied for %s", instance),
		Body:             body,
		Fingerprint:      fingerprint,
		RevivalRevision:  revivalRevision,
		RevivalReviewers: revivalReviewers,
	}
}

// currentRevisionFromResult reads the current resource revision off any judge
// leaf's evaluation (every judge leaf in one instance's result carries the
// same DoneWhenEvalContext.CurrentRevision).
func currentRevisionFromResult(result task.DoneWhenResult) string {
	for _, leaf := range result.Leaves {
		if leaf.Kind == "judge" && leaf.CurrentRevision != "" {
			return leaf.CurrentRevision
		}
	}
	return ""
}

// staleJudgeReviewers collects the recorded reviewer session for every judge
// leaf that is pending because its verdict predates the current revision
// (evalJudgeLeaf's "stale_judge" reason) — the sessions the automatic
// post-exhaustion revival kick targets.
func staleJudgeReviewers(result task.DoneWhenResult) []RevivalReviewer {
	var out []RevivalReviewer
	for _, leaf := range result.Leaves {
		if leaf.Kind != "judge" || leaf.PendingReason != "stale_judge" || leaf.ReviewerSession == "" {
			continue
		}
		out = append(out, RevivalReviewer{LeafID: leaf.ID, Session: leaf.ReviewerSession})
	}
	return out
}

func reviewerDispatchCommand(resource, instance string) string {
	if resource == "" {
		return ""
	}
	// Which reviewer workflow runs is a chaining concern, not a judge-leaf field;
	// this advisory suggestion defaults to claude until chaining (slice 6) owns it.
	return fmt.Sprintf("plect up %q --workflow claude --task review --tag %s", resource, reviewerTag(instance))
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

func reviewerTag(instance string) string {
	var b strings.Builder
	b.WriteString("review-")
	for _, r := range instance {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// doneWhenBudgetMaxRounds returns 0 when no budget is configured. The checker
// treats that as explicit unbounded work: it can keep requesting review, but
// it will not emit an escalation event.
func doneWhenBudgetMaxRounds(dw *config.DoneWhen) int {
	if dw == nil || len(dw.Budget) == 0 {
		return 0
	}
	v, ok := dw.Budget["max_rounds"]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return 0
	}
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
			leaf.ReviewerSession,
			leaf.ReviewerWorkflow,
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
		Kind:             leaf.Kind,
		Expr:             leaf.Expr,
		Status:           leaf.Status,
		ID:               leaf.ID,
		Output:           leaf.Output,
		Value:            leaf.Value,
		Observed:         leaf.Observed,
		Action:           leaf.Action,
		Reason:           leaf.Reason,
		PendingReason:    leaf.PendingReason,
		Revision:         leaf.Revision,
		CurrentRevision:  leaf.CurrentRevision,
		ReviewerSession:  leaf.ReviewerSession,
		ReviewerWorkflow: leaf.ReviewerWorkflow,
		Relation:         leaf.Relation,
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

// mergeableStateHint surfaces a PR merge conflict as an advisory kick note
// rather than a done_when leaf: a review host generally does not run CI on a
// change with merge conflicts, so checks_status would otherwise sit PENDING
// forever and the session would wait indefinitely for CI that will never
// arrive. "unknown" (mergeability still being computed) and "NULL" (nothing
// to read it from) are intentionally silent.
func mergeableStateHint(outputs map[string]any) string {
	v, ok := outputs[outputKeyMergeableState]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	if s != "dirty" {
		return ""
	}
	return "Note: mergeable_state=dirty — this PR conflicts with its base branch. Rebase or merge the base branch in before continuing."
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
			if item.ReviewerSession != "" {
				details = append(details, "reviewer="+item.ReviewerSession)
			}
			if item.ReviewerWorkflow != "" {
				details = append(details, "reviewer_workflow="+item.ReviewerWorkflow)
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

func reviewRequiredBody(instance string, round, maxRounds int, reviewerCommand string, items []CheckUnmetItem, judgeCmds []string, warnings []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "done_when needs independent review for %s (round %s).\n\n", instance, roundText(round, maxRounds))
	if reviewerCommand != "" {
		fmt.Fprintf(&b, "Dispatch reviewer:\n%s\n\n", reviewerCommand)
	} else {
		b.WriteString("Dispatch reviewer:\n")
		for _, warning := range warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
		b.WriteString("\n")
	}
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

func roundText(round, maxRounds int) string {
	if maxRounds == 0 {
		return fmt.Sprintf("%d/unbounded", round)
	}
	return fmt.Sprintf("%d/%d", round, maxRounds)
}
