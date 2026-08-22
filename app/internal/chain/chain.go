// Package chain is the deterministic workflow-chaining engine's pure core: it
// decides whether a chain fires against a work session's raw facts, with no I/O
// and no session spawning. The service layer assembles Facts from persisted
// state and acts on the decision (spawning the workflow session at its
// placement); this package only answers "does it fire, and what does it wire".
package chain

import (
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/task"
)

// JudgeFact is the raw-fact view of one judge leaf at the current revision,
// projected from the done_when evaluation (not a stored gate state). Pending is
// true when the leaf has no usable verdict (missing / stale / self / wrong
// relation); Action is the current verdict's action ("approve" /
// "request_changes") or empty when pending.
type JudgeFact struct {
	Pending bool
	Action  string
}

// Facts is the raw-fact snapshot a chain's `when` evaluates against: the work
// instance's outputs plus its judge leaves, both read directly from
// (outputs, judge records, revision).
type Facts struct {
	// State is the pair of live roots a trigger reads: what the declared
	// observer publishes about the resource, and what the instance holds.
	State  task.CompletionState
	Judges map[string]JudgeFact
}

// WhenSatisfied reports whether every fact in `when` holds. An empty `when`
// returns false — config validation forbids it, so this is a defensive floor
// against an unconditional fire.
func WhenSatisfied(when config.ChainWhen, facts Facts) bool {
	if len(when.All) == 0 {
		return false
	}
	for _, fact := range when.All {
		if !factHolds(fact, facts) {
			return false
		}
	}
	return true
}

func factHolds(fact config.ChainWhenFact, facts Facts) bool {
	switch {
	case fact.JudgePending != "":
		return facts.Judges[fact.JudgePending].Pending
	case fact.JudgeAction != "":
		return facts.Judges[fact.JudgeAction].Action == fact.Is
	default:
		leaf := config.DoneWhenLeaf{
			Check: fact.Check,
			Eq:    fact.Eq,
			Ne:    fact.Ne,
			In:    fact.In,
			Gte:   fact.Gte,
			Lte:   fact.Lte,
			Expr:  fact.Expr,
		}
		return task.CheckLeafStatus(leaf, facts.State) == task.DoneSatisfied
	}
}

// WiredOutputs returns the work-output keys a chain's input bindings reference
// via `{{.Work.outputs.<key>}}`, sorted and de-duplicated. These are the
// outputs the chain's fire depends on: the second half of the firing condition
// is that every one is present (MissingOutputs). UndeclaredWiredOutputs then
// checks them against the upstream output contract, and RenderInputs binds them.
//
// Delegates to config.ChainWiredOutputs so the config package's load-time
// static validation (judge id / wiring checks against a task's own done_when
// and outputs_schema) shares one scan of the binding templates' parse trees.
func WiredOutputs(inputs map[string]string) ([]string, error) {
	return config.ChainWiredOutputs(inputs)
}

// MissingOutputs returns the wired outputs not present (or nil) in outputs.
func MissingOutputs(inputs map[string]string, outputs map[string]any) ([]string, error) {
	wired, err := WiredOutputs(inputs)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, k := range wired {
		if v, ok := outputs[k]; !ok || v == nil {
			missing = append(missing, k)
		}
	}
	return missing, nil
}

// UndeclaredWiredOutputs returns the wired output keys that the upstream node's
// published output contract does not declare. `declared` is the upstream
// outputs-schema property set. An empty
// `declared` means the upstream publishes no contract, so wiring is then
// unconstrained — matching how set-output and `plect task setup` treat a
// schema-less target. A non-empty result is a binding that names an output the
// contract never publishes (a typo or a private value), which must surface
// rather than wire silently.
func UndeclaredWiredOutputs(inputs map[string]string, declared []string) ([]string, error) {
	if len(declared) == 0 {
		return nil, nil
	}
	wired, err := WiredOutputs(inputs)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(declared))
	for _, d := range declared {
		set[d] = true
	}
	var bad []string
	for _, k := range wired {
		if !set[k] {
			bad = append(bad, k)
		}
	}
	return bad, nil
}

// WorkFacts is the `.Work` template context a chain's input bindings render
// against. The fields are exposed under the lowercase keys the ADR's binding
// templates name: `.Work.resource`, `.Work.session`, `.Work.workflow`,
// `.Work.instance`, `.Work.outputs.<key>`, and
// `.Work.done_when.pending_judge_ids`.
type WorkFacts struct {
	Resource        string
	Session         string
	Workflow        string
	Instance        string
	Outputs         map[string]any
	PendingJudgeIDs []string
}

// workTemplateData builds the `.Work` template context shared by RenderInputs
// and RenderWorkflow, so both render against the same vocabulary.
func workTemplateData(work WorkFacts) map[string]any {
	return map[string]any{
		"Work": map[string]any{
			"resource": work.Resource,
			"session":  work.Session,
			"workflow": work.Workflow,
			"instance": work.Instance,
			"outputs":  normalizeOutputs(work.Outputs),
			"done_when": map[string]any{
				"pending_judge_ids": strings.Join(work.PendingJudgeIDs, " "),
			},
		},
	}
}

// RenderInputs renders each `[chains.inputs]` binding against the work facts and
// returns the resolved downstream inputs. Rendering uses missingkey=error so a
// binding naming a context key that does not exist surfaces as an error instead
// of wiring an empty string — output presence is already gated by MissingOutputs
// before a fired chain reaches here. pending_judge_ids is exposed as a
// space-separated scalar so a `for id in {{...}}` reviewer setup can iterate it.
func RenderInputs(inputs map[string]string, work WorkFacts) (map[string]any, error) {
	data := workTemplateData(work)
	keys := make([]string, 0, len(inputs))
	for k := range inputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(inputs))
	for _, k := range keys {
		t, err := template.New("chain-input").Option("missingkey=error").Parse(inputs[k])
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", k, err)
		}
		var b strings.Builder
		if err := t.Execute(&b, data); err != nil {
			return nil, fmt.Errorf("input %q: %w", k, err)
		}
		out[k] = b.String()
	}
	return out, nil
}

// RenderWorkflow renders a chain's `workflow` field against the same
// vocabulary [chains.inputs] bindings use (e.g. `{{if eq .Work.workflow
// "claude"}}codex{{else}}claude{{end}}`), so a review chain can target the
// cross-tool reviewer for whichever workflow the work session itself used.
// missingkey=error, matching RenderInputs: a stray reference surfaces as an
// error instead of resolving to an empty workflow id.
func RenderWorkflow(workflowTmpl string, work WorkFacts) (string, error) {
	t, err := template.New("chain-workflow").Option("missingkey=error").Parse(workflowTmpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, workTemplateData(work)); err != nil {
		return "", err
	}
	return strings.TrimSpace(b.String()), nil
}

// normalizeOutputs renders whole-valued float64 outputs (JSON's only number
// type) as integers, so a numeric output like a PR number does not reach a
// binding as scientific notation (3.05e+06).
func normalizeOutputs(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if f, ok := v.(float64); ok && f == float64(int64(f)) {
			out[k] = int64(f)
			continue
		}
		out[k] = v
	}
	return out
}
