package adapter

import "testing"

func TestPendingMentionSurvivesRestartUntilDelivered(t *testing.T) {
	dir := t.TempDir()
	store, err := NewPendingStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mention := PendingMention{Key: "T1/UBOT/C1/1700000000.000002", Resource: "https://example.slack.com/archives/C1/p1700000000000001", ChannelID: "C1", ThreadTS: "1700000000.000001", MentionTS: "1700000000.000002", UserID: "U1", Summary: "mention", Body: "transcript"}
	if err := store.Put(mention); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPendingStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := restarted.List(); len(got) != 1 || got[0].Body != mention.Body {
		t.Fatalf("pending after restart = %+v", got)
	}
	if err := restarted.Remove(mention.Key); err != nil {
		t.Fatal(err)
	}
	if got := restarted.List(); len(got) != 0 {
		t.Fatalf("pending after delivery = %+v", got)
	}
}
