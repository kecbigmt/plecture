package chain

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/task"
)

func strp(s string) *string { return &s }

func TestWhenSatisfied(t *testing.T) {
	facts := Facts{
		State: task.CompletionState{Self: map[string]any{"checks_status": "SUCCESS"}},
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
				{Check: "self.state.checks_status", In: []any{"SUCCESS", "NULL"}},
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
			when: config.ChainWhen{All: []config.ChainWhenFact{{Check: "self.state.checks_status", Eq: strp("FAILURE")}}},
			want: false,
		},
		{
			name: "check pending (missing output) is not satisfied",
			when: config.ChainWhen{All: []config.ChainWhenFact{{Check: "self.state.absent", Eq: strp("x")}}},
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
				{Check: "self.state.checks_status", Eq: strp("FAILURE")},
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
