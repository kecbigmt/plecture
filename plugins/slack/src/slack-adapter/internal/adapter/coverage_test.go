package adapter

import (
	"strings"
	"testing"
)

func TestCoverageUsesQueryDeclarationsAcrossStreamDisconnect(t *testing.T) {
	bots := []Bot{{ChannelIDs: []string{"C1", "C2", "C3"}}}
	coverage := NewCoverage(bots, "/run/slack-feed.sock")
	queries := []QueryRegistration{{ID: "ops/mentions", Inputs: map[string]any{"socket_path": "/run/slack-feed.sock", "channel_ids": []any{"C1", "C2"}}}}
	if err := coverage.Replace(queries); err != nil {
		t.Fatal(err)
	}
	if coverage.Owner("C1") != "ops/mentions" || coverage.Owner("C2") != "ops/mentions" || coverage.Owner("C3") != "" {
		t.Fatalf("owners = %q, %q, %q", coverage.Owner("C1"), coverage.Owner("C2"), coverage.Owner("C3"))
	}
	if err := coverage.Replace([]QueryRegistration{{ID: "ops/mentions", Inputs: queries[0].Inputs}, {ID: "other/mentions", Inputs: map[string]any{"socket_path": "/run/slack-feed.sock", "channel_ids": []any{"C2"}}}}); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap error = %v", err)
	}
	if coverage.Owner("C2") != "ops/mentions" {
		t.Fatal("invalid replacement changed coverage")
	}
	for _, tt := range []struct {
		name   string
		inputs map[string]any
		want   string
	}{
		{"socket mismatch", map[string]any{"socket_path": "/run/other.sock", "channel_ids": []any{"C1"}}, "socket_path"},
		{"unconfigured channel", map[string]any{"socket_path": "/run/slack-feed.sock", "channel_ids": []any{"C4"}}, "unconfigured channel"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := coverage.Replace([]QueryRegistration{{ID: "other/mentions", Inputs: tt.inputs}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("replacement error = %v, want %q", err, tt.want)
			}
			if coverage.Owner("C1") != "ops/mentions" || coverage.Owner("C4") != "" {
				t.Fatalf("invalid replacement changed coverage: C1=%q, C4=%q", coverage.Owner("C1"), coverage.Owner("C4"))
			}
		})
	}
	if err := coverage.Replace(nil); err != nil || coverage.Owner("C1") != "" {
		t.Fatalf("removed coverage = %q, err = %v", coverage.Owner("C1"), err)
	}
}
