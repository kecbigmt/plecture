package task

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
)

func strp(s string) *string { return &s }
func fp(f float64) *float64 { return &f }

func TestEvaluateTaskDoneWhen(t *testing.T) {
	tests := []struct {
		name    string
		dw      *config.DoneWhen
		outputs map[string]any
		want    DoneStatus
	}{
		{
			name:    "nil done_when is empty",
			dw:      nil,
			outputs: nil,
			want:    "",
		},
		{
			name:    "eq satisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "checks_status", Eq: strp("SUCCESS")}}},
			outputs: map[string]any{"checks_status": "SUCCESS"},
			want:    DoneSatisfied,
		},
		{
			name:    "eq present but false is unsatisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "checks_status", Eq: strp("SUCCESS")}}},
			outputs: map[string]any{"checks_status": "FAILURE"},
			want:    DoneUnsatisfied,
		},
		{
			name:    "missing output is pending, not unsatisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "checks_status", Eq: strp("SUCCESS")}}},
			outputs: map[string]any{},
			want:    DonePending,
		},
		{
			name:    "ne satisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "pr_state", Ne: strp("open")}}},
			outputs: map[string]any{"pr_state": "merged"},
			want:    DoneSatisfied,
		},
		{
			name:    "in satisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "pr_state", In: []any{"merged", "closed"}}}},
			outputs: map[string]any{"pr_state": "closed"},
			want:    DoneSatisfied,
		},
		{
			name:    "in not a member is unsatisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "pr_state", In: []any{"merged", "closed"}}}},
			outputs: map[string]any{"pr_state": "open"},
			want:    DoneUnsatisfied,
		},
		{
			name:    "gte satisfied (numeric, normalized int)",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "coverage", Gte: fp(80)}}},
			outputs: map[string]any{"coverage": float64(85)},
			want:    DoneSatisfied,
		},
		{
			name:    "gte below bound is unsatisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "coverage", Gte: fp(80)}}},
			outputs: map[string]any{"coverage": float64(70)},
			want:    DoneUnsatisfied,
		},
		{
			name:    "lte satisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "failures", Lte: fp(0)}}},
			outputs: map[string]any{"failures": float64(0)},
			want:    DoneSatisfied,
		},
		{
			name:    "gte against non-numeric value is unsatisfied",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "coverage", Gte: fp(80)}}},
			outputs: map[string]any{"coverage": "lots"},
			want:    DoneUnsatisfied,
		},
		{
			name: "conjunction: all satisfied",
			dw: &config.DoneWhen{All: []config.DoneWhenLeaf{
				{Check: "pr_state", Eq: strp("merged")},
				{Check: "checks_status", Eq: strp("SUCCESS")},
			}},
			outputs: map[string]any{"pr_state": "merged", "checks_status": "SUCCESS"},
			want:    DoneSatisfied,
		},
		{
			name: "conjunction: one unsatisfied wins over pending",
			dw: &config.DoneWhen{All: []config.DoneWhenLeaf{
				{Check: "pr_state", Eq: strp("merged")},       // unsatisfied
				{Check: "checks_status", Eq: strp("SUCCESS")}, // pending (missing)
			}},
			outputs: map[string]any{"pr_state": "open"},
			want:    DoneUnsatisfied,
		},
		{
			name: "conjunction: satisfied + pending is pending",
			dw: &config.DoneWhen{All: []config.DoneWhenLeaf{
				{Check: "pr_state", Eq: strp("merged")},       // satisfied
				{Check: "checks_status", Eq: strp("SUCCESS")}, // pending (missing)
			}},
			outputs: map[string]any{"pr_state": "merged"},
			want:    DonePending,
		},
		{
			name:    "judge leaf is pending without a judge",
			dw:      &config.DoneWhen{All: []config.DoneWhenLeaf{{Judge: "reviewer approved", ID: "ac-met"}}},
			outputs: map[string]any{},
			want:    DonePending,
		},
		{
			name: "judge leaf keeps a satisfied check pending",
			dw: &config.DoneWhen{All: []config.DoneWhenLeaf{
				{Check: "pr_state", Eq: strp("merged")},
				{Judge: "reviewer approved"},
			}},
			outputs: map[string]any{"pr_state": "merged"},
			want:    DonePending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateTaskDoneWhen(tt.dw, tt.outputs)
			if got.Overall != tt.want {
				t.Errorf("overall = %q, want %q (leaves: %+v)", got.Overall, tt.want, got.Leaves)
			}
		})
	}
}

func TestEvaluateTaskDoneWhen_Judges(t *testing.T) {
	dw := &config.DoneWhen{All: []config.DoneWhenLeaf{{Judge: "acceptance criteria", ID: "ac-met"}}}
	outputs := map[string]any{"revision": "sha1"}

	t.Run("approve action from current independent review satisfies", func(t *testing.T) {
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1", ReviewerSession: "review", Relation: "sibling", Reason: "tests pass"},
			},
		})
		if got.Overall != DoneSatisfied {
			t.Fatalf("overall = %q, want satisfied: %+v", got.Overall, got.Leaves)
		}
	})

	t.Run("changes requested current judge is unsatisfied", func(t *testing.T) {
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionRequestChanges, Revision: "sha1", ReviewerSession: "review", Relation: "sibling", Reason: "missing AC"},
			},
		})
		if got.Overall != DoneUnsatisfied || got.Leaves[0].Reason != "missing AC" {
			t.Fatalf("got %+v, want unsatisfied with reason", got)
		}
	})

	t.Run("stale judge is pending", func(t *testing.T) {
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha2",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1", ReviewerSession: "review", Relation: "sibling"},
			},
		})
		if got.Overall != DonePending || got.Leaves[0].PendingReason != "stale_judge" {
			t.Fatalf("got %+v, want stale pending", got)
		}
	})

	t.Run("self review is pending", func(t *testing.T) {
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1", ReviewerSession: "work"},
			},
		})
		if got.Overall != DonePending || got.Leaves[0].PendingReason != "self_review" {
			t.Fatalf("got %+v, want self-review pending", got)
		}
	})

	t.Run("empty reviewer is self review pending", func(t *testing.T) {
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1"},
			},
		})
		if got.Overall != DonePending || got.Leaves[0].PendingReason != "self_review" {
			t.Fatalf("got %+v, want empty-reviewer pending", got)
		}
	})

	t.Run("default policy accepts a parent reviewer", func(t *testing.T) {
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1", ReviewerSession: "orchestrator", Relation: "parent"},
			},
		})
		if got.Overall != DoneSatisfied {
			t.Fatalf("got %+v, want parent reviewer satisfied", got)
		}
	})

	t.Run("default policy rejects a child reviewer as relation_not_accepted", func(t *testing.T) {
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1", ReviewerSession: "work+child", Relation: "child"},
			},
		})
		if got.Overall != DonePending || got.Leaves[0].PendingReason != "relation_not_accepted" {
			t.Fatalf("got %+v, want child reviewer relation_not_accepted", got)
		}
	})

	t.Run("explicit child policy accepts a child reviewer", func(t *testing.T) {
		dw := &config.DoneWhen{All: []config.DoneWhenLeaf{{Judge: "acceptance criteria", ID: "ac-met", Relation: []string{"child"}}}}
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1", ReviewerSession: "work+child", Relation: "child"},
			},
		})
		if got.Overall != DoneSatisfied {
			t.Fatalf("got %+v, want explicitly-allowed child reviewer satisfied", got)
		}
	})

	t.Run("explicit policy that omits sibling rejects a sibling reviewer", func(t *testing.T) {
		dw := &config.DoneWhen{All: []config.DoneWhenLeaf{{Judge: "acceptance criteria", ID: "ac-met", Relation: []string{"parent"}}}}
		got := EvaluateTaskDoneWhenWithContext(dw, outputs, DoneWhenEvalContext{
			WorkSession:     "work",
			CurrentRevision: "sha1",
			Judges: map[string]Judge{
				"ac-met": {Action: JudgeActionApprove, Revision: "sha1", ReviewerSession: "review", Relation: "sibling"},
			},
		})
		if got.Overall != DonePending || got.Leaves[0].PendingReason != "relation_not_accepted" {
			t.Fatalf("got %+v, want sibling rejected under parent-only policy", got)
		}
	})
}

// TestEvaluateTaskDoneWhen_TaskResourceStatus pins the truth table the shipped
// work/review/respond/investigate done_when encodes: resource_kind gates
// which resource kinds are eligible at all, and FAILURE is distinguishable
// from PENDING (not collapsed). issue_status is deliberately absent: a
// task's own completion no longer waits on the issue actually being
// closed — that is the integrator's responsibility, not this done_when's — so
// an issue resource with no linked PR yet (checks_status NULL) is done as
// soon as its own judges are satisfied.
func TestEvaluateTaskDoneWhen_TaskResourceStatus(t *testing.T) {
	dw := &config.DoneWhen{All: []config.DoneWhenLeaf{
		{Check: "resource_kind", In: []any{"pull", "issue"}},
		{Check: "checks_status", In: []any{"SUCCESS", "NULL"}},
	}}
	out := func(kind, checks string) map[string]any {
		return map[string]any{"resource_kind": kind, "checks_status": checks}
	}
	tests := []struct {
		name    string
		outputs map[string]any
		want    DoneStatus
	}{
		{"pr checks success is done", out("pull", "SUCCESS"), DoneSatisfied},
		{"pr checks failure is unsatisfied", out("pull", "FAILURE"), DoneUnsatisfied},
		{"pr checks pending is not done", out("pull", "PENDING"), DoneUnsatisfied},
		{"issue with no linked PR is done", out("issue", "NULL"), DoneSatisfied},
		{"issue with a green linked PR is done", out("issue", "SUCCESS"), DoneSatisfied},
		{"unknown resource never done", out("unknown", "NULL"), DoneUnsatisfied},
		{"unfetched outputs stay pending", map[string]any{}, DonePending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvaluateTaskDoneWhen(dw, tt.outputs); got.Overall != tt.want {
				t.Errorf("overall = %q, want %q (leaves: %+v)", got.Overall, tt.want, got.Leaves)
			}
		})
	}
}

func TestEvaluateTaskDoneWhen_NormalizesIntegers(t *testing.T) {
	// JSON unmarshal leaves integers as float64; eq compares via the normalized
	// int string form, so an integral coverage value still matches "3".
	dw := &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "count", Eq: strp("3")}}}
	if got := EvaluateTaskDoneWhen(dw, map[string]any{"count": float64(3)}); got.Overall != DoneSatisfied {
		t.Errorf("overall = %q, want satisfied", got.Overall)
	}
}

func TestEvaluateTaskDoneWhen_LeafCarriesObservedValue(t *testing.T) {
	dw := &config.DoneWhen{All: []config.DoneWhenLeaf{
		{Check: "workdir_dirty", Eq: strp("0")},
		{Check: "checks_status", Eq: strp("SUCCESS")},
		{Judge: "reviewer approved"},
	}}
	got := EvaluateTaskDoneWhen(dw, map[string]any{"workdir_dirty": "2"})

	dirty := got.Leaves[0]
	if !dirty.Observed || dirty.Value != "2" || dirty.Output != "workdir_dirty" {
		t.Errorf("observed leaf = %+v, want output=workdir_dirty value=2 observed", dirty)
	}
	if dirty.Status != DoneUnsatisfied {
		t.Errorf("workdir_dirty=2 eq 0 → %q, want unsatisfied", dirty.Status)
	}

	unobserved := got.Leaves[1]
	if unobserved.Observed || unobserved.Value != "" || unobserved.Status != DonePending {
		t.Errorf("unobserved leaf = %+v, want pending with no value", unobserved)
	}

	if judge := got.Leaves[2]; judge.Output != "" || judge.Observed {
		t.Errorf("judge leaf = %+v, want no output/value", judge)
	}
}

func TestResolveDefinition_Requires(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"checks_status": map[string]any{"type": "string", "mutable": true},
		},
	}
	base := config.TaskDefinition{ID: "review", Scope: "session", Setup: shellStub("echo '{}'"), OutputsSchema: schema}

	t.Run("valid: check in requires in schema", func(t *testing.T) {
		def := base
		def.Requires = []string{"checks_status"}
		def.DoneWhen = &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "checks_status", Eq: strp("SUCCESS")}}}
		if _, err := ResolveDefinition(def, "review"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("done_when check not in requires", func(t *testing.T) {
		def := base
		def.Requires = []string{"checks_status"}
		def.DoneWhen = &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "pr_state", Eq: strp("merged")}}}
		if _, err := ResolveDefinition(def, "review"); err == nil {
			t.Error("expected error: check not declared in requires")
		}
	})

	t.Run("requires not in outputs schema", func(t *testing.T) {
		def := base
		def.Requires = []string{"nope"}
		if _, err := ResolveDefinition(def, "review"); err == nil {
			t.Error("expected error: requires names an undeclared output")
		}
	})

	t.Run("empty requires is unconstrained", func(t *testing.T) {
		def := base
		def.DoneWhen = &config.DoneWhen{All: []config.DoneWhenLeaf{{Check: "anything", Eq: strp("x")}}}
		if _, err := ResolveDefinition(def, "review"); err != nil {
			t.Errorf("no requires should be unconstrained, got %v", err)
		}
	})
}
