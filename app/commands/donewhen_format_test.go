package commands

import (
	"testing"

	"github.com/kecbigmt/plect/app/internal/task"
)

func TestFormatDoneWhen_ShowsCurrentValues(t *testing.T) {
	dw := &task.DoneWhenResult{
		Overall: task.DoneUnsatisfied,
		Leaves: []task.DoneLeafResult{
			{Kind: "check", Status: task.DoneSatisfied, Output: "checks_status", Value: "SUCCESS", Observed: true},
			{Kind: "check", Status: task.DoneUnsatisfied, Output: "workdir_dirty", Value: "2", Observed: true},
		},
	}
	got := formatDoneWhen(dw)
	want := "✗ unsatisfied (1/2) [checks_status=SUCCESS workdir_dirty=2]"
	if got != want {
		t.Errorf("formatDoneWhen() = %q, want %q", got, want)
	}
}

func TestFormatDoneWhen_UnobservedReadsAsQuestionMark(t *testing.T) {
	dw := &task.DoneWhenResult{
		Overall: task.DonePending,
		Leaves: []task.DoneLeafResult{
			{Kind: "check", Status: task.DonePending, Output: "workdir_dirty"},
			{Kind: "judge", Status: task.DonePending, ID: "ac-met", PendingReason: "missing_judge"},
		},
	}
	got := formatDoneWhen(dw)
	want := "⋯ pending (0/2) [workdir_dirty=? ac-met=missing_judge]"
	if got != want {
		t.Errorf("formatDoneWhen() = %q, want %q", got, want)
	}
}

func TestFormatDoneWhen_ShowsJudge(t *testing.T) {
	dw := &task.DoneWhenResult{
		Overall: task.DoneSatisfied,
		Leaves: []task.DoneLeafResult{
			{Kind: "judge", Status: task.DoneSatisfied, ID: "ac-met", Action: "approve", Reason: "verified", Revision: "sha1", CurrentRevision: "sha1"},
		},
	}
	got := formatDoneWhen(dw)
	want := "✓ satisfied (1/1) [ac-met=approve:verified@sha1]"
	if got != want {
		t.Errorf("formatDoneWhen() = %q, want %q", got, want)
	}
}

func TestFormatDoneWhen_ShowsStaleJudgeRevision(t *testing.T) {
	dw := &task.DoneWhenResult{
		Overall: task.DonePending,
		Leaves: []task.DoneLeafResult{
			{
				Kind:            "judge",
				Status:          task.DonePending,
				ID:              "ac-met",
				Action:          "approve",
				Revision:        "sha1",
				CurrentRevision: "sha2",
				PendingReason:   "stale_judge",
			},
		},
	}
	got := formatDoneWhen(dw)
	want := "⋯ pending (0/1) [ac-met=approve@sha1!=sha2/stale_judge]"
	if got != want {
		t.Errorf("formatDoneWhen() = %q, want %q", got, want)
	}
}
