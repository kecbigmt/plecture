package adapter

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestPrivateFeedDeliversOnlyMatchingChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.sock")
	feed, err := NewMentionFeed(path)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(map[string]any{"channel_ids": []string{"C1"}}); err != nil {
		t.Fatal(err)
	}
	feed.AcceptOne()
	if feed.Emit(PendingMention{ChannelID: "C2"}) {
		t.Fatal("unmatched channel was emitted")
	}
	if !feed.Emit(PendingMention{Resource: "https://example.slack.com/archives/C1/p1700000000000001", ChannelID: "C1", ThreadTS: "1700000000.000001", MentionTS: "1700000000.000002", UserID: "U1"}) {
		t.Fatal("matching channel was not emitted")
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	var item map[string]any
	if err := json.NewDecoder(conn).Decode(&item); err != nil || item["channel_id"] != "C1" || item["resource"] == "" {
		t.Fatalf("item = %+v, err = %v", item, err)
	}
}

func TestPrivateFeedRejectsStalePopulationRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feed.sock")
	coverage := NewCoverage([]Bot{{ChannelIDs: []string{"C1", "C2"}}}, path)
	if err := coverage.Replace([]QueryRegistration{{ID: "ops/mentions", Inputs: map[string]any{"socket_path": path, "channel_ids": []any{"C2"}}}}); err != nil {
		t.Fatal(err)
	}
	feed, err := NewMentionFeed(path, coverage)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(map[string]any{"channel_ids": []string{"C1"}}); err != nil {
		t.Fatal(err)
	}
	if err := feed.AcceptOne(); err == nil {
		t.Fatal("stale registration accepted")
	}
}
