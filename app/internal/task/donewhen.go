package task

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kecbigmt/plect/app/internal/config"
	"github.com/kecbigmt/plect/app/internal/domain"
)

// DoneStatus is the evaluation result of a done_when leaf (and, aggregated,
// of a whole done_when).
//
//   - DoneSatisfied   — the predicate holds (✓).
//   - DoneUnsatisfied — the output is present and the predicate is false (✗).
//   - DonePending     — the predicate cannot be evaluated yet: a check leaf
//     reads an output the instance has not produced, or a judge leaf has no
//     reviewer action.
type DoneStatus string

const (
	DoneSatisfied   DoneStatus = "satisfied"
	DoneUnsatisfied DoneStatus = "unsatisfied"
	DonePending     DoneStatus = "pending"
)

const (
	JudgeActionApprove        = "approve"
	JudgeActionRequestChanges = "request_changes"
)

// Judge is the read-only reviewer action consumed by the done_when evaluator.
// Callers own persistence and convert from their state representation at the
// boundary.
type Judge struct {
	LeafID           string `json:"leaf_id"`
	Action           string `json:"action"`
	Reason           string `json:"reason,omitempty"`
	Revision         string `json:"revision,omitempty"`
	ReviewerSession  string `json:"reviewer_session,omitempty"`
	ReviewerWorkflow string `json:"reviewer_workflow,omitempty"`
	Relation         string `json:"relation,omitempty"`
}

// DoneWhenEvalContext supplies judge actions and the current opaque revision
// for the task instance under evaluation.
type DoneWhenEvalContext struct {
	WorkSession     string
	CurrentRevision string
	Judges          map[string]Judge
}

// DoneLeafResult is one leaf's evaluation. A check leaf also carries the read
// output's last-fetched Value (Observed=false until produced) — acquisition and
// evaluation are separate, so the value is displayable on its own.
type DoneLeafResult struct {
	Kind             string     `json:"kind"` // "check" | "judge"
	Expr             string     `json:"expr"` // human-readable rendering of the leaf
	Status           DoneStatus `json:"status"`
	ID               string     `json:"id,omitempty"`
	Output           string     `json:"output,omitempty"`
	Value            string     `json:"value,omitempty"`
	Observed         bool       `json:"observed,omitempty"`
	Action           string     `json:"action,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	Revision         string     `json:"revision,omitempty"`
	CurrentRevision  string     `json:"current_revision,omitempty"`
	ReviewerSession  string     `json:"reviewer_session,omitempty"`
	ReviewerWorkflow string     `json:"reviewer_workflow,omitempty"`
	Relation         string     `json:"relation,omitempty"`
	PendingReason    string     `json:"pending_reason,omitempty"`
}

// DoneWhenResult is the per-instance evaluation of a task's done_when.
// Overall is satisfied only when every leaf is satisfied; it is unsatisfied
// when any leaf is unsatisfied; otherwise (some leaves still pending) it is
// pending.
type DoneWhenResult struct {
	Overall DoneStatus       `json:"overall"`
	Leaves  []DoneLeafResult `json:"leaves,omitempty"`
}

// validateTaskRequires enforces the `requires` contract: when an
// task declares `requires`, every done_when check leaf must read a required
// output, and every required output must be a property of the task's outputs
// schema. This makes the done_when ↔ observed-output wiring explicit and catches
// typos at compile time. A nil/empty `requires` is unconstrained (opt-in).
func validateTaskRequires(def config.TaskDefinition) error {
	if len(def.Requires) == 0 {
		return nil
	}
	required := make(map[string]bool, len(def.Requires))
	for _, r := range def.Requires {
		required[r] = true
	}
	if def.DoneWhen != nil {
		for i, leaf := range def.DoneWhen.All {
			if strings.TrimSpace(leaf.Check) == "" {
				continue
			}
			if !required[leaf.Check] {
				return fmt.Errorf("done_when.all[%d] reads output %q which is not declared in `requires` %v", i, leaf.Check, def.Requires)
			}
		}
	}
	props, err := SchemaPropertyNames(def.OutputsSchema, def.ResolvedOutputsSchemaPath())
	if err != nil {
		return fmt.Errorf("outputs schema: %w", err)
	}
	if len(props) > 0 {
		declared := make(map[string]bool, len(props))
		for _, p := range props {
			declared[p] = true
		}
		for _, r := range def.Requires {
			if !declared[r] {
				return fmt.Errorf("`requires` names output %q which the outputs schema does not declare", r)
			}
		}
	}
	return nil
}

// EvaluateTaskDoneWhen evaluates a task's done_when against a single
// instance's outputs. Each check leaf compares the named output's value with
// the leaf's operator; a judge leaf reads persisted reviewer action.
func EvaluateTaskDoneWhen(dw *config.DoneWhen, outputs map[string]any) DoneWhenResult {
	return EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{})
}

// EvaluateTaskDoneWhenWithContext evaluates a done_when with reviewer action
// context for judge leaves. Evaluation is read-only: stale, missing, or
// self-review action is pending; current request_changes is unsatisfied.
func EvaluateTaskDoneWhenWithContext(dw *config.DoneWhen, outputs map[string]any, ctx DoneWhenEvalContext) DoneWhenResult {
	if dw == nil || len(dw.All) == 0 {
		return DoneWhenResult{}
	}
	normalized := normalizeOutputs(outputs)
	if normalized == nil {
		normalized = map[string]any{}
	}

	leaves := make([]DoneLeafResult, 0, len(dw.All))
	satisfied, unsatisfied := 0, 0
	for i, leaf := range dw.All {
		var res DoneLeafResult
		if strings.TrimSpace(leaf.Judge) != "" {
			res = evalJudgeLeaf(i, leaf, ctx)
		} else {
			val, observed := normalized[leaf.Check]
			observed = observed && val != nil
			res = DoneLeafResult{
				Kind:     "check",
				Expr:     checkExpr(leaf),
				Status:   evalCheckLeaf(leaf, normalized),
				Output:   leaf.Check,
				Observed: observed,
			}
			if observed {
				res.Value = toString(val)
			}
		}
		switch res.Status {
		case DoneSatisfied:
			satisfied++
		case DoneUnsatisfied:
			unsatisfied++
		}
		leaves = append(leaves, res)
	}

	overall := DonePending
	switch {
	case unsatisfied > 0:
		overall = DoneUnsatisfied
	case satisfied == len(leaves):
		overall = DoneSatisfied
	}
	return DoneWhenResult{Overall: overall, Leaves: leaves}
}

func evalJudgeLeaf(i int, leaf config.DoneWhenLeaf, ctx DoneWhenEvalContext) DoneLeafResult {
	id := JudgeLeafID(i, leaf)
	res := DoneLeafResult{
		Kind:            "judge",
		ID:              id,
		Expr:            leaf.Judge,
		Status:          DonePending,
		CurrentRevision: ctx.CurrentRevision,
	}
	judge, ok := ctx.Judges[id]
	if !ok {
		res.PendingReason = "missing_judge"
		return res
	}
	res.Action = judge.Action
	res.Reason = judge.Reason
	res.Revision = judge.Revision
	res.ReviewerSession = judge.ReviewerSession
	res.ReviewerWorkflow = judge.ReviewerWorkflow
	res.Relation = judge.Relation
	rel := domain.SessionRelation(judge.Relation)
	// self-review is structurally rejected: a verdict from the work session (or
	// one with no attributable reviewer) can never satisfy a judge leaf.
	if judge.ReviewerSession == "" || rel == domain.RelationSelf || (ctx.WorkSession != "" && judge.ReviewerSession == ctx.WorkSession) {
		res.PendingReason = "self_review"
		return res
	}
	if !domain.JudgeRelationAccepted(acceptedJudgeRelations(leaf), rel) {
		res.PendingReason = "relation_not_accepted"
		return res
	}
	if ctx.CurrentRevision == "" {
		res.PendingReason = "missing_current_revision"
		return res
	}
	if judge.Revision != ctx.CurrentRevision {
		res.PendingReason = "stale_judge"
		return res
	}
	switch judge.Action {
	case JudgeActionApprove:
		res.Status = DoneSatisfied
	case JudgeActionRequestChanges:
		res.Status = DoneUnsatisfied
	default:
		res.PendingReason = "unknown_judge"
	}
	return res
}

// acceptedJudgeRelations converts a leaf's declared relation policy to domain
// values; nil (leaf declares none) lets JudgeRelationAccepted apply the default.
func acceptedJudgeRelations(leaf config.DoneWhenLeaf) []domain.SessionRelation {
	if len(leaf.Relation) == 0 {
		return nil
	}
	out := make([]domain.SessionRelation, len(leaf.Relation))
	for i, r := range leaf.Relation {
		out[i] = domain.SessionRelation(r)
	}
	return out
}

// JudgeLeafID returns the stable storage key for a judge leaf. Configs should
// set explicit ids; the index fallback keeps older configs addressable.
func JudgeLeafID(i int, leaf config.DoneWhenLeaf) string {
	if strings.TrimSpace(leaf.ID) != "" {
		return strings.TrimSpace(leaf.ID)
	}
	return fmt.Sprintf("judge:%d", i)
}

// CheckLeafStatus evaluates a single check leaf against raw (un-normalized)
// outputs. It is the check-fact primitive the chaining engine's `when` shares
// with done_when, so a chain `{ check = ..., in = [...] }` reads identically to
// the same done_when leaf.
func CheckLeafStatus(leaf config.DoneWhenLeaf, outputs map[string]any) DoneStatus {
	return evalCheckLeaf(leaf, normalizeOutputs(outputs))
}

// evalCheckLeaf applies the leaf's comparison operator to the named output. A
// missing output is pending (data not yet observed); a present output that
// fails the predicate is unsatisfied. gte/lte against a non-numeric value reads
// as unsatisfied — the value exists but cannot satisfy a numeric bound.
func evalCheckLeaf(leaf config.DoneWhenLeaf, outputs map[string]any) DoneStatus {
	val, ok := outputs[leaf.Check]
	if !ok || val == nil {
		return DonePending
	}
	switch {
	case leaf.Eq != nil:
		return boolStatus(toString(val) == *leaf.Eq)
	case leaf.Ne != nil:
		return boolStatus(toString(val) != *leaf.Ne)
	case leaf.In != nil:
		target := toString(val)
		for _, item := range leaf.In {
			if toString(item) == target {
				return DoneSatisfied
			}
		}
		return DoneUnsatisfied
	case leaf.Gte != nil:
		f, ok := toFloat(val)
		if !ok {
			return DoneUnsatisfied
		}
		return boolStatus(f >= *leaf.Gte)
	case leaf.Lte != nil:
		f, ok := toFloat(val)
		if !ok {
			return DoneUnsatisfied
		}
		return boolStatus(f <= *leaf.Lte)
	}
	// No operator — config validation rejects this; treat defensively as pending.
	return DonePending
}

// checkExpr renders a check leaf for display, e.g. `coverage gte 80`.
func checkExpr(leaf config.DoneWhenLeaf) string {
	switch {
	case leaf.Eq != nil:
		return fmt.Sprintf("%s eq %q", leaf.Check, *leaf.Eq)
	case leaf.Ne != nil:
		return fmt.Sprintf("%s ne %q", leaf.Check, *leaf.Ne)
	case leaf.In != nil:
		return fmt.Sprintf("%s in %v", leaf.Check, leaf.In)
	case leaf.Gte != nil:
		return fmt.Sprintf("%s gte %v", leaf.Check, *leaf.Gte)
	case leaf.Lte != nil:
		return fmt.Sprintf("%s lte %v", leaf.Check, *leaf.Lte)
	}
	return leaf.Check
}

func boolStatus(b bool) DoneStatus {
	if b {
		return DoneSatisfied
	}
	return DoneUnsatisfied
}

// toString renders an output value for string-equality / membership checks.
// Numbers arrive normalized (int64 for integral values), so 3 → "3".
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}
