package chain

import (
	"reflect"
	"testing"

	"github.com/plecture/plect/app/internal/config"
)

func strp(s string) *string { return &s }

func TestWhenSatisfied(t *testing.T) {
	facts := Facts{
		Outputs: map[string]any{"checks_status": "SUCCESS"},
		Judges: map[string]JudgeFact{
			"ac-met": {Pending: true},
			"solves": {Pending: false, Action: "request_changes"},
		},
	}
	tests := []struct {
		name string
		when config.ChainWhen
		want bool
	}{
		{
			name: "all hold",
			when: config.ChainWhen{All: []config.ChainWhenFact{
				{JudgePending: "ac-met"},
				{Check: "checks_status", In: []any{"SUCCESS", "NULL"}},
				{JudgeAction: "solves", Is: "request_changes"},
			}},
			want: true,
		},
		{
			name: "judge_pending false",
			when: config.ChainWhen{All: []config.ChainWhenFact{{JudgePending: "solves"}}},
			want: false,
		},
		{
			name: "check unsatisfied",
			when: config.ChainWhen{All: []config.ChainWhenFact{{Check: "checks_status", Eq: strp("FAILURE")}}},
			want: false,
		},
		{
			name: "check pending (missing output) is not satisfied",
			when: config.ChainWhen{All: []config.ChainWhenFact{{Check: "absent", Eq: strp("x")}}},
			want: false,
		},
		{
			name: "judge_action mismatch",
			when: config.ChainWhen{All: []config.ChainWhenFact{{JudgeAction: "solves", Is: "approve"}}},
			want: false,
		},
		{
			name: "empty when never fires",
			when: config.ChainWhen{},
			want: false,
		},
		{
			name: "one of several fails",
			when: config.ChainWhen{All: []config.ChainWhenFact{
				{JudgePending: "ac-met"},
				{Check: "checks_status", Eq: strp("FAILURE")},
			}},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := WhenSatisfied(tc.when, facts); got != tc.want {
				t.Fatalf("WhenSatisfied = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWiredOutputs(t *testing.T) {
	inputs := map[string]string{
		"revision":     "{{.Work.outputs.revision}}",
		"resource":     "{{.Work.resource}}",
		"work_session": "{{.Work.session}}",
		"judge_ids":    "{{.Work.done_when.pending_judge_ids}}",
		"combo":        "rev={{.Work.outputs.revision}} kind={{.Work.outputs.resource_kind}}",
	}
	got, err := WiredOutputs(inputs)
	if err != nil {
		t.Fatalf("WiredOutputs: %v", err)
	}
	want := []string{"resource_kind", "revision"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WiredOutputs = %v, want %v", got, want)
	}
}

func TestMissingOutputs(t *testing.T) {
	inputs := map[string]string{
		"revision": "{{.Work.outputs.revision}}",
		"kind":     "{{.Work.outputs.resource_kind}}",
	}
	missing, err := MissingOutputs(inputs, map[string]any{"revision": "sha1"})
	if err != nil {
		t.Fatalf("MissingOutputs: %v", err)
	}
	if !reflect.DeepEqual(missing, []string{"resource_kind"}) {
		t.Fatalf("missing = %v, want [resource_kind]", missing)
	}

	none, err := MissingOutputs(inputs, map[string]any{"revision": "sha1", "resource_kind": "pull"})
	if err != nil {
		t.Fatalf("MissingOutputs: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("missing = %v, want none", none)
	}
}

func TestUndeclaredWiredOutputs(t *testing.T) {
	inputs := map[string]string{
		"revision": "{{.Work.outputs.revision}}",
		"kind":     "{{.Work.outputs.resource_kind}}",
		"session":  "{{.Work.session}}", // not an output ref, never flagged
	}

	// resource_kind is wired but not published by the contract → flagged.
	bad, err := UndeclaredWiredOutputs(inputs, []string{"revision"})
	if err != nil {
		t.Fatalf("UndeclaredWiredOutputs: %v", err)
	}
	if !reflect.DeepEqual(bad, []string{"resource_kind"}) {
		t.Fatalf("bad = %v, want [resource_kind]", bad)
	}

	// All wired outputs declared → none flagged.
	none, err := UndeclaredWiredOutputs(inputs, []string{"revision", "resource_kind"})
	if err != nil {
		t.Fatalf("UndeclaredWiredOutputs: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("bad = %v, want none", none)
	}

	// No published contract → unconstrained, nothing flagged.
	unconstrained, err := UndeclaredWiredOutputs(inputs, nil)
	if err != nil {
		t.Fatalf("UndeclaredWiredOutputs: %v", err)
	}
	if len(unconstrained) != 0 {
		t.Fatalf("bad = %v, want none for schema-less upstream", unconstrained)
	}
}

func TestRenderInputs(t *testing.T) {
	inputs := map[string]string{
		"resource":     "{{.Work.resource}}",
		"work_session": "{{.Work.session}}",
		"revision":     "{{.Work.outputs.revision}}",
		"pr":           "pr-{{.Work.outputs.number}}",
		"judge_ids":    "{{.Work.done_when.pending_judge_ids}}",
	}
	work := WorkFacts{
		Resource:        "https://github.com/o/r/pull/7",
		Session:         "o/r-7",
		Outputs:         map[string]any{"revision": "sha1", "number": float64(7)},
		PendingJudgeIDs: []string{"ac-met", "solves"},
	}
	got, err := RenderInputs(inputs, work)
	if err != nil {
		t.Fatalf("RenderInputs: %v", err)
	}
	want := map[string]any{
		"resource":     "https://github.com/o/r/pull/7",
		"work_session": "o/r-7",
		"revision":     "sha1",
		"pr":           "pr-7", // whole float64 rendered as int, not 7e+00
		"judge_ids":    "ac-met solves",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderInputs = %#v, want %#v", got, want)
	}
}

func TestRenderInputs_Instance(t *testing.T) {
	inputs := map[string]string{"instance": "{{.Work.instance}}"}
	work := WorkFacts{Session: "owner/_orchestrator", Instance: "goal_stage3_demo"}
	got, err := RenderInputs(inputs, work)
	if err != nil {
		t.Fatalf("RenderInputs: %v", err)
	}
	if got["instance"] != "goal_stage3_demo" {
		t.Fatalf("instance = %v, want %q", got["instance"], "goal_stage3_demo")
	}
}

// A binding naming a context key that does not exist surfaces as an error
// rather than wiring an empty string.
func TestRenderInputs_MissingKeyErrors(t *testing.T) {
	inputs := map[string]string{"x": "{{.Work.outputs.absent}}"}
	if _, err := RenderInputs(inputs, WorkFacts{Outputs: map[string]any{"revision": "sha1"}}); err == nil {
		t.Fatal("expected error for absent output reference, got nil")
	}
}

// The cross-tool review-chain template (config/plect/tasks/work.toml) resolves
// to the opposite tool from whichever workflow the work session itself used.
func TestRenderWorkflow_CrossTool(t *testing.T) {
	tmpl := `{{if eq .Work.workflow "claude"}}codex{{else}}claude{{end}}`
	tests := []struct {
		workSessionWorkflow string
		want                string
	}{
		{"claude", "codex"},
		{"codex", "claude"},
	}
	for _, tc := range tests {
		got, err := RenderWorkflow(tmpl, WorkFacts{Workflow: tc.workSessionWorkflow})
		if err != nil {
			t.Fatalf("RenderWorkflow(%q): %v", tc.workSessionWorkflow, err)
		}
		if got != tc.want {
			t.Errorf("RenderWorkflow(work.workflow=%q) = %q, want %q", tc.workSessionWorkflow, got, tc.want)
		}
	}
}

// A plain string (no template actions) renders to itself unchanged — an owner
// overlay that fixes the reviewer workflow doesn't need template syntax.
func TestRenderWorkflow_PlainString(t *testing.T) {
	got, err := RenderWorkflow("claude", WorkFacts{Workflow: "codex"})
	if err != nil {
		t.Fatalf("RenderWorkflow: %v", err)
	}
	if got != "claude" {
		t.Fatalf("RenderWorkflow = %q, want %q", got, "claude")
	}
}

// A workflow template referencing a context key that does not exist surfaces
// as an error rather than resolving to an empty workflow id.
func TestRenderWorkflow_MissingKeyErrors(t *testing.T) {
	if _, err := RenderWorkflow("{{.Work.outputs.absent}}", WorkFacts{}); err == nil {
		t.Fatal("expected error for absent output reference, got nil")
	}
}

// A nil output value counts as missing — the work session produced the key but
// has no value yet, so the binding cannot be wired.
func TestMissingOutputs_NilCountsMissing(t *testing.T) {
	inputs := map[string]string{"revision": "{{.Work.outputs.revision}}"}
	missing, err := MissingOutputs(inputs, map[string]any{"revision": nil})
	if err != nil {
		t.Fatalf("MissingOutputs: %v", err)
	}
	if !reflect.DeepEqual(missing, []string{"revision"}) {
		t.Fatalf("missing = %v, want [revision]", missing)
	}
}
