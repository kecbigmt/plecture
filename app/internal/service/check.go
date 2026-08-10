package service

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kecbigmt/sennit/app/internal/config"
	"github.com/kecbigmt/sennit/app/internal/domain"
	"github.com/kecbigmt/sennit/app/internal/state"
	"github.com/kecbigmt/sennit/app/internal/task"
	contract "github.com/kecbigmt/sennit/contracts/state"
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

// CheckParams has no refresh option: check always reads persisted state (see
// CheckSession). Only tick refreshes dynamic outputs before evaluating.
type CheckParams struct {
	SessionName string
}

type CheckAction struct {
	SessionName     string           `json:"session_name"`
	Instance        string           `json:"instance"`
	Action          string           `json:"action"`
	Round           int              `json:"round,omitempty"`
	MaxRounds       int              `json:"max_rounds"`
	Items           []string         `json:"items,omitempty"`
	UnmetItems      []CheckUnmetItem `json:"unmet_items,omitempty"`
	Warnings        []string         `json:"warnings,omitempty"`
	Summary         string           `json:"summary,omitempty"`
	Body            string           `json:"body,omitempty"`
	ReviewerCommand string           `json:"reviewer_command,omitempty"`
	JudgeCommands   []string         `json:"judge_commands,omitempty"`
	Fingerprint     string           `json:"fingerprint,omitempty"`
}

type CheckResult struct {
	Actions []CheckAction `json:"actions,omitempty"`
	// Chains is the [[chains]] evaluation for this same tick/check — fired /
	// already-active / blocked, with the reason — evaluated against the same
	// facts as Actions. CheckSession (and sennit status, which shares the same
	// evaluation) always reports it as a dry-run plan (Spawned is always
	// false); TickSession spawns each fired, not-already-active entry.
	Chains []ChainSpawn `json:"chains,omitempty"`
	// Warnings carries config-level notices unrelated to any one instance —
	// currently just a surviving legacy chains/*.toml file, which the retired
	// dual-read no longer reads (config.LegacyChainsDirNotice).
	Warnings []string `json:"warnings,omitempty"`
}

type CheckUnmetItem struct {
	Kind             string          `json:"kind"`
	Expr             string          `json:"expr"`
	Status           task.DoneStatus `json:"status"`
	ID               string          `json:"id,omitempty"`
	Output           string          `json:"output,omitempty"`
	Value            string          `json:"value,omitempty"`
	Observed         bool            `json:"observed,omitempty"`
	Action           string          `json:"action,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	PendingReason    string          `json:"pending_reason,omitempty"`
	Revision         string          `json:"revision,omitempty"`
	CurrentRevision  string          `json:"current_revision,omitempty"`
	ReviewerSession  string          `json:"reviewer_session,omitempty"`
	ReviewerWorkflow string          `json:"reviewer_workflow,omitempty"`
	Relation         string          `json:"relation,omitempty"`
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
		sessionName = os.Getenv("SENNIT_SESSION_NAME")
	}
	if sessionName == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "no session in scope: pass --session or run inside a sennit session pane"}
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
		reviewer = os.Getenv("SENNIT_SESSION_NAME")
	}
	if reviewer == "" {
		return nil, &Error{Code: ErrInvalidInput, Message: "reviewer session is required: pass --reviewer-session or run inside a reviewer sennit session pane"}
	}
	allSessions := store.All()
	reviewerWorkflow := ""
	if rs := allSessions[reviewer]; rs != nil {
		reviewerWorkflow = rs.Workflow
	}
	relation := string(domain.RelationFromTarget(allSessions, resolvedName, reviewer))
	defs, err := cfg.LoadTaskDefinitions(session.WorktreePath)
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
	// sennit's own concept. Best-effort like recordLifecycle — a failed append
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

// computedAction bundles one instance's done_when evaluation with the
// record-time facts needed only by the actuator (sennit tick): whether the last
// persisted action was already "satisfied" (so a repeated `done` push can be
// skipped) and which instance key it belongs to. CheckSession discards these
// extras and reports only the action; TickSession consumes all three. result
// is the raw leaf-level evaluation the action was derived from — Status's work
// layer reuses it instead of re-evaluating done_when for the same instance.
type computedAction struct {
	instance         string
	action           CheckAction
	alreadySatisfied bool
	result           task.DoneWhenResult
}

// evaluateSessionActions runs the read-only half shared by CheckSession (sennit
// status / sennit_check) and TickSession (sennit tick): optionally refresh outputs,
// resolve the session, and evaluate
// done_when — and, against those same facts, [[chains]] — for every produced
// task instance. It never writes state, spawns a session, or publishes events:
// CheckSession returns its result verbatim (a dry-run chain plan), while
// TickSession additionally publishes/persists per action and spawns each
// fired, not-already-active chain. refresh is false for every CheckSession
// call: check reads persisted state only, so that repeated calls cannot
// themselves change what a session reports (story PR-C #4). Only tick
// refreshes dynamic outputs.
func evaluateSessionActions(cfg *config.Config, store *state.Store, sessionName string, refresh bool) (string, []computedAction, []ChainSpawn, []string, error) {
	if sessionName == "" {
		sessionName = os.Getenv("SENNIT_SESSION_NAME")
	}
	if sessionName == "" {
		return "", nil, nil, nil, &Error{Code: ErrInvalidInput, Message: "no session in scope: pass a session or run inside a sennit session pane"}
	}
	if refresh {
		if _, err := RefreshSessionOutputs(cfg, store, sessionName); err != nil {
			return "", nil, nil, nil, err
		}
	}
	resolvedName, session, err := resolveSession(cfg, store, sessionName)
	if err != nil {
		return "", nil, nil, nil, err
	}
	allSessions := store.All()
	defs, err := cfg.LoadTaskDefinitions(session.WorktreePath)
	if err != nil {
		return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("load task definitions: %v", err)}
	}
	legacyWarnings, err := cfg.LegacyChainsDirNotice()
	if err != nil {
		return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("legacy chains dir: %v", err)}
	}
	chains := config.TaskChains(defs)

	var computed []computedAction
	var chainPlan []ChainSpawn
	for _, key := range sortedTaskKeys(session.Tasks) {
		st := session.Tasks[key]
		if st == nil || st.Status != contract.TaskStatusProduced || key == contract.WorkflowPseudoNodeID {
			continue
		}
		taskID := taskIDForInstance(key, st)
		def := defs[taskID]
		dw, err := effectiveDoneWhen(def.DoneWhen, st)
		if err != nil {
			return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: err.Error()}
		}
		if dw == nil {
			continue
		}
		// Captured before a tick's persist overwrites LastAction, so the
		// `done` push it may issue fires exactly once per instance (ADR D8:
		// goal-loop Layer 1 owns `done` emission) rather than on every poll.
		alreadySatisfied := st.DoneWhen != nil && st.DoneWhen.LastAction == "satisfied"
		eval := task.EvaluateTaskDoneWhenWithContext(dw, st.Outputs, doneWhenEvalContext(resolvedName, st, allSessions))
		action := checkActionForResult(resolvedName, key, sessionResourceForCheck(session, st), dw, st, eval)
		if action.Action != "" {
			computed = append(computed, computedAction{instance: key, action: action, alreadySatisfied: alreadySatisfied, result: eval})
		}
		if len(chains) == 0 {
			continue
		}
		facts := buildChainFacts(st.Outputs, eval)
		resource := sessionResourceForCheck(session, st)
		// The upstream output contract: the published output keys a chain's
		// `{{.Work.outputs.X}}` bindings may reference. Empty when the task
		// declares no outputs schema (then wiring is unconstrained).
		upstreamOutputs, schemaErr := task.SchemaPropertyNames(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
		if schemaErr != nil {
			return "", nil, nil, nil, &Error{Code: ErrExecutionFailed, Message: fmt.Sprintf("task %q: outputs schema: %v", taskID, schemaErr)}
		}
		for _, ch := range chains {
			if ch.TaskID != "" && ch.TaskID != taskID {
				continue
			}
			sp := evalChain(cfg, store, ch, resolvedName, session, key, resource, facts, upstreamOutputs)
			sp.Task = taskID
			chainPlan = append(chainPlan, sp)
		}
	}
	return resolvedName, computed, chainPlan, legacyWarnings, nil
}

// CheckSession reports the same done_when/chain evaluation tick would act on,
// but only reports it: no round advances, no event is published, no session
// is woken or spawned, and no dynamic output is refreshed — it reads whatever
// tick (or the initial produce) last persisted. Calling it any number of
// times leaves state, event log, and session list unchanged (story PR-C #4;
// wiki verification-gate.md: "the target session's state does not change"). Use sennit tick
// to actually advance the gate, refresh outputs, and fire chains.
func CheckSession(cfg *config.Config, store *state.Store, params CheckParams) (*CheckResult, error) {
	_, computed, chainPlan, warnings, err := evaluateSessionActions(cfg, store, params.SessionName, false)
	if err != nil {
		return nil, err
	}
	actions := make([]CheckAction, 0, len(computed))
	for _, c := range computed {
		actions = append(actions, c.action)
	}
	return &CheckResult{Actions: actions, Chains: chainPlan, Warnings: warnings}, nil
}

func checkActionForResult(sessionName, instance, resource string, dw *config.DoneWhen, st *contract.TaskState, result task.DoneWhenResult) CheckAction {
	maxRounds := doneWhenBudgetMaxRounds(dw)
	rounds := 0
	lastFingerprint := ""
	if st.DoneWhen != nil {
		rounds = st.DoneWhen.Rounds
		lastFingerprint = st.DoneWhen.LastFingerprint
	}
	fingerprint := checkFingerprint(result)
	sameDoneWhenState := fingerprint != "" && fingerprint == lastFingerprint
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
			SessionName:     sessionName,
			Instance:        instance,
			Action:          "review_required",
			Round:           nextRound,
			MaxRounds:       maxRounds,
			Items:           items,
			UnmetItems:      unmetItems,
			Warnings:        warnings,
			Summary:         fmt.Sprintf("done_when review required for %s", instance),
			Body:            body,
			ReviewerCommand: cmd,
			JudgeCommands:   judgeCmds,
			Fingerprint:     fingerprint,
		}
	}
	body := fmt.Sprintf("done_when is unsatisfied for %s (round %s).\n\nAddress these unmet items:\n%s", instance, roundText(nextRound, maxRounds), unmetItemBulletList(unmetItems))
	if hint := mergeableStateHint(st.Outputs); hint != "" {
		body += "\n\n" + hint
	}
	return CheckAction{
		SessionName: sessionName,
		Instance:    instance,
		Action:      "kick",
		Round:       nextRound,
		MaxRounds:   maxRounds,
		Items:       items,
		UnmetItems:  unmetItems,
		Summary:     fmt.Sprintf("done_when unsatisfied for %s", instance),
		Body:        body,
		Fingerprint: fingerprint,
	}
}

func sessionResourceForCheck(session *domain.Session, st *contract.TaskState) string {
	if st.Resource != "" {
		return st.Resource
	}
	if session.ResourceID != "" {
		return session.ResourceID
	}
	return session.URL
}

func reviewerDispatchCommand(resource, instance string) string {
	if resource == "" {
		return ""
	}
	// Which reviewer workflow runs is a chaining concern, not a judge-leaf field;
	// this advisory suggestion defaults to claude until chaining (slice 6) owns it.
	return fmt.Sprintf("sennit up %q --workflow claude --task review --tag %s", resource, reviewerTag(instance))
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
	return fmt.Sprintf("sennit judge %s %q %q %q --reason %q", action, sessionName, instance, id, "<reason>")
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

func doneWhenEvalContext(sessionName string, st *contract.TaskState, sessions map[string]*domain.Session) task.DoneWhenEvalContext {
	return task.DoneWhenEvalContext{
		WorkSession:     sessionName,
		CurrentRevision: currentRevision(st.Outputs),
		Judges:          judgeInputs(doneWhenJudges(st.DoneWhen), sessionName, sessions),
	}
}

func doneWhenJudges(st *contract.DoneWhenState) map[string]*contract.DoneWhenJudge {
	if st == nil {
		return nil
	}
	return st.Judges
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

func sortedTaskKeys(tasks map[string]*contract.TaskState) []string {
	keys := make([]string, 0, len(tasks))
	for key := range tasks {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
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
// rather than a done_when leaf: GitHub never runs CI on a dirty PR, so
// checks_status would otherwise sit PENDING forever and the session would
// wait indefinitely for CI that will never arrive. "unknown" (GitHub
// still computing mergeability) and "NULL" (no PR to read it from) are
// intentionally silent.
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
