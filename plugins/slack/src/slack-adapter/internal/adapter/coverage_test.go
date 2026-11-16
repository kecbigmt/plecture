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
	if err := coverage.Replace(nil); err != nil || coverage.Owner("C1") != "" {
		t.Fatalf("removed coverage = %q, err = %v", coverage.Owner("C1"), err)
	}
}
