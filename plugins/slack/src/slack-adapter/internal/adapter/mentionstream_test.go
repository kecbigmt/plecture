package adapter

import "testing"

func TestMentionStreamHasSubscribersReflectsRegistration(t *testing.T) {
	s := newMentionStream()
	if s.hasSubscribers() {
		t.Fatal("hasSubscribers() = true before any subscribe")
	}

	_, cancel := s.subscribe()
	if !s.hasSubscribers() {
		t.Fatal("hasSubscribers() = false after subscribe")
	}

	cancel()
	if s.hasSubscribers() {
		t.Fatal("hasSubscribers() = true after cancel")
	}
}

func TestMentionStreamPublishFansOutToEveryReader(t *testing.T) {
	s := newMentionStream()
	chA, cancelA := s.subscribe()
	defer cancelA()
	chB, cancelB := s.subscribe()
	defer cancelB()

	item := unboundMentionItem{Resource: "https://example.slack.com/archives/C1/p1", ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.1"}
	s.publish(item)

	got := <-chA
	if got != item {
		t.Errorf("reader A got %+v, want %+v", got, item)
	}
	got = <-chB
	if got != item {
		t.Errorf("reader B got %+v, want %+v", got, item)
	}
}

func TestMentionStreamPublishWithNoSubscribersDoesNotBlock(t *testing.T) {
	s := newMentionStream()
	// Must return, not hang: dispatchUnboundMention calls this from the
	// Slack event goroutine and must never block on an absent reader.
	s.publish(unboundMentionItem{Resource: "https://example.slack.com/archives/C1/p1"})
}

func TestMentionStreamPublishDropsForAFullReaderRatherThanBlocking(t *testing.T) {
	s := newMentionStream()
	ch, cancel := s.subscribe()
	defer cancel()

	for range mentionStreamBuffer + 5 {
		s.publish(unboundMentionItem{ThreadTS: "overflow"})
	}

	if len(ch) != mentionStreamBuffer {
		t.Fatalf("buffered items = %d, want the full buffer size %d", len(ch), mentionStreamBuffer)
	}
}

func TestMentionStreamCancelStopsFurtherDeliveryWithoutClosingTheChannel(t *testing.T) {
	s := newMentionStream()
	ch, cancel := s.subscribe()
	cancel()

	s.publish(unboundMentionItem{ThreadTS: "after-cancel"})

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("received an item after cancel, want none")
		}
		t.Fatal("channel closed after cancel, want it left open and simply unregistered")
	default:
	}
}
