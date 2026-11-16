package commands

import (
	"testing"

	"github.com/kecbigmt/plecture/app/internal/service"
)

func TestTickActionSummary_ReportsSuppressedAnnouncement(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action service.CheckAction
		want   string
	}{
		{"first", service.CheckAction{Action: "kick", Summary: "done_when unsatisfied"}, "done_when unsatisfied"},
		{"repeat", service.CheckAction{Action: "kick", Summary: "done_when unsatisfied", Unchanged: true}, "done_when unsatisfied (unchanged; no announcement sent)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tickActionSummary(tc.action); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}
