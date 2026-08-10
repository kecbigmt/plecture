package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kecbigmt/sennit/app/internal/service"
	"github.com/kecbigmt/sennit/app/internal/task"
)

// renderDoneWhenSections omits any task instance without a done_when — its
// lifecycle state is out of scope for this section entirely (available via
// --json --full), not merely elided.
func TestRenderDoneWhenSections_NoDoneWhenInstancesProduceNoSection(t *testing.T) {
	var buf bytes.Buffer
	renderDoneWhenSections(&buf, []service.StatusTask{
		{Instance: "envfile", Status: "produced"},
		{Instance: "@workflow", Status: "produced"},
	})
	if buf.Len() != 0 {
		t.Errorf("expected no output for instances without done_when, got %q", buf.String())
	}
}

// A done_when-bearing instance renders a "Done when (<instance>)" block with
// round/action, one line per condition, and one line per chain — no raw
// outputs dump, no per-task "Task <name> <status>" line.
func TestRenderDoneWhenSections_UnsatisfiedJudgeBlock(t *testing.T) {
	var buf bytes.Buffer
	renderDoneWhenSections(&buf, []service.StatusTask{
		{
			Instance:  "initial",
			Status:    "produced",
			Rounds:    3,
			MaxRounds: 3,
			Action:    "review_required",
			DoneWhen: &task.DoneWhenResult{
				Overall: task.DonePending,
				Leaves: []task.DoneLeafResult{
					{Kind: "check", Status: task.DoneSatisfied, Output: "checks_status", Value: "SUCCESS", Observed: true},
					{Kind: "judge", Status: task.DonePending, ID: "ac-met", Expr: "acceptance criteria are satisfied", CurrentRevision: "9256fbb5bc027adb611dc2093d1a120e139e7193"},
				},
			},
			Chains: []service.StatusChain{
				{ChainID: "review", AlreadyActive: true, TargetSession: "owner/repo-1+review-initial"},
			},
			Outputs: map[string]any{"instruction": "a very long multi-line prompt blob"},
		},
	})
	out := buf.String()

	for _, want := range []string{
		"Done when (initial)",
		"round: 3/3",
		"action: review_required",
		"conditions",
		"✓ checks_status=SUCCESS",
		"⋯ ac-met  pending@current:9256fbb  (acceptance criteria are satisfied)",
		"chains",
		"review: already-active (owner/repo-1+review-initial)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "instruction") {
		t.Errorf("output leaked an output not referenced by done_when:\n%s", out)
	}
	if strings.Contains(out, "Task ") {
		t.Errorf("output must not contain a lifecycle \"Task <name> <status>\" line:\n%s", out)
	}
}

// A chain that did not fire renders its blocked reason so an operator can see
// why nothing will act automatically.
func TestStatusChainDetailLine_NotFiredDetail(t *testing.T) {
	tests := []struct {
		name string
		c    service.StatusChain
		want string
	}{
		{"when unmet", service.StatusChain{BlockedReason: "when_unmet"}, "not-fired (when not satisfied)"},
		{"missing outputs", service.StatusChain{BlockedReason: "outputs_missing", MissingOutputs: []string{"pr_url"}}, "not-fired (missing outputs: pr_url)"},
		{"fired", service.StatusChain{Fired: true, TargetSession: "s2"}, "fired (s2)"},
		{"already active", service.StatusChain{AlreadyActive: true, TargetSession: "s2"}, "already-active (s2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusChainDetailLine(tt.c)
			if got != tt.want {
				t.Errorf("statusChainDetailLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --full is JSON-only; passing it without --json must error before touching
// config/state.
func TestStatusCmd_FullRequiresJSON(t *testing.T) {
	origJSON, origFull := statusJSON, statusFull
	statusJSON, statusFull = false, true
	defer func() { statusJSON, statusFull = origJSON, origFull }()

	err := statusCmd.RunE(statusCmd, []string{"whatever"})
	if err == nil || !strings.Contains(err.Error(), "--full") {
		t.Fatalf("err = %v, want an error naming --full", err)
	}
}
