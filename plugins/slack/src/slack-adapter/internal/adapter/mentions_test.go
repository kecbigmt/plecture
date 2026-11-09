package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestMentionResourceDeliversAcrossThreadsAndReloadsWatermark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	a := newTestAdapter(&Config{})
	a.broker = NewBroker(path, slog.Default())
	a.broker.Subscribe(Subscriber{ThreadTS: mentionSubscriptionKey, SessionName: "orchestrator"})
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage("1700000000.000001", "U-root", "root"),
		slackMessage("1700000000.000002", "U-other", "old reply"),
	}}
	mention := &slackevents.AppMentionEvent{User: "U-person", Text: "<@U-bot> first", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C-one"}
	a.handleAppMention(mention)
	if len(publisher.events) != 1 || publisher.events[0].SessionName != "orchestrator" || publisher.events[0].Type != "user.emit" {
		t.Fatalf("first mention events = %+v", publisher.events)
	}
	assertBodyContainsInOrder(t, publisher.events[0].Body, []string{"root", "old reply", "first"})
	if publisher.events[0].Meta["mention_ts"] != "1700000000.000003" || publisher.events[0].Meta["channel_id"] != "C-one" {
		t.Fatalf("metadata = %+v", publisher.events[0].Meta)
	}

	a.broker = NewBroker(path, slog.Default())
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage("1700000000.000001", "U-root", "root"),
		slackMessage("1700000000.000002", "U-other", "old reply"),
		slackMessage("1700000000.000004", "U-other", "new reply"),
	}}
	mention.TimeStamp, mention.Text = "1700000000.000005", "<@U-bot> second"
	a.handleAppMention(mention)
	if len(publisher.events) != 2 || strings.Contains(publisher.events[1].Body, "old reply") {
		t.Fatalf("second mention events = %+v", publisher.events)
	}
	assertBodyContainsInOrder(t, publisher.events[1].Body, []string{"root", "new reply", "second"})
	mention.Channel, mention.ThreadTimeStamp, mention.TimeStamp = "C-two", "1700000001.000001", "1700000001.000003"
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000001.000001", "U-root", "other root")}}
	a.handleAppMention(mention)
	if len(publisher.events) != 3 || publisher.events[2].Meta["thread_ts"] != "1700000001.000001" {
		t.Fatalf("other thread events = %+v", publisher.events)
	}
	a.broker.Unsubscribe("orchestrator", mentionSubscriptionKey)
	a.handleAppMention(mention)
	if len(publisher.events) != 3 {
		t.Fatalf("events after unsubscribe = %+v", publisher.events)
	}
}

func TestMentionResourceSubscriptionRoutesAndUnsubscribes(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID, a.botUserID = "T123", "UBOT"
	server := httptest.NewServer(http.HandlerFunc(a.HandleSubscribe))
	defer server.Close()
	for _, resource := range []string{
		"slack://T123/mentions",
		"slack://T123/UBOT/mentions/other",
		"slack://T123/UBOT/mentions?channel=C1",
		"slack://T123/UBOT/mentions#fragment",
		"slack://T123:42/UBOT/mentions",
		"slack://T123/ubot/mentions",
		"slack://T123//mentions",
	} {
		if err := MentionSubscription(context.Background(), server.Client(), server.URL, "subscribe", "one", resource); err == nil {
			t.Fatalf("accepted invalid resource %q", resource)
		}
	}
	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "subscribe", "one", "slack://T999/UBOT/mentions"); err == nil {
		t.Fatal("accepted resource for another team")
	}
	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "subscribe", "one", "slack://T123/UBOT/mentions"); err != nil {
		t.Fatal(err)
	}
	if got := a.broker.ListMentions(); len(got) != 1 || got[0].SessionName != "one" {
		t.Fatalf("subscriptions = %+v", got)
	}
	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "unsubscribe", "one", "slack://T123/UBOT/mentions"); err != nil {
		t.Fatal(err)
	}
	if got := a.broker.ListMentions(); len(got) != 0 {
		t.Fatalf("subscriptions after unsubscribe = %+v", got)
	}
}

func TestMentionResourceAddsToThreadSubscriptionWithoutReceivingReplies(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.broker.Subscribe(Subscriber{ThreadTS: mentionSubscriptionKey, SessionName: "orchestrator"})
	a.broker.Subscribe(Subscriber{ThreadTS: "1700000000.000001", ChannelID: "C-one", SessionName: "thread"})
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "root")}}
	a.api = fakeSlackAPI(t)
	a.botUserID = "U-bot"
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.handleMessage(&slackevents.MessageEvent{User: "U-person", Text: "reply", ThreadTimeStamp: "1700000000.000001", Channel: "C-one"})
	if len(publisher.events) != 1 || publisher.events[0].SessionName != "thread" || publisher.events[0].Type != "slack.message" {
		t.Fatalf("reply events = %+v", publisher.events)
	}
	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-person", Text: "<@U-bot> hello", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C-one"})
	if len(publisher.events) != 3 {
		t.Fatalf("events = %+v", publisher.events)
	}
	seen := map[string]bool{}
	for _, event := range publisher.events[1:] {
		if event.Type != "user.emit" {
			t.Fatalf("mention event = %+v", event)
		}
		seen[event.SessionName] = true
	}
	if !seen["thread"] || !seen["orchestrator"] {
		t.Fatalf("mention recipients = %+v", seen)
	}
}

func TestMentionWatermarksExpireFromLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	state := persistedState{Version: 2, Subscribers: []Subscriber{{ThreadTS: mentionSubscriptionKey, SessionName: "one"}}, MentionWatermarks: []mentionWatermark{
		{SessionName: "one", ThreadTS: "1700000000.000001", Through: "1700000000.000005", UpdatedAt: time.Now().Add(-mentionWatermarkRetention - time.Hour)},
		{SessionName: "one", ThreadTS: "1700000001.000001", Through: "1700000001.000005", UpdatedAt: time.Now()},
	}}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(path, nil)
	if b.MentionWatermark("one", "1700000000.000001") != "" || b.MentionWatermark("one", "1700000001.000001") != "1700000001.000005" {
		t.Fatalf("loaded watermarks = %+v", b.mentions)
	}
}

func TestMentionPublishFailureLeavesWatermarkForRetry(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.broker.Subscribe(Subscriber{ThreadTS: mentionSubscriptionKey, SessionName: "one"})
	a.broker.Subscribe(Subscriber{ThreadTS: mentionSubscriptionKey, SessionName: "two"})
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "root")}}
	publisher := &selectivePublisher{}
	a.eventPublisher = publisher
	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-person", Text: "<@U-bot> act", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C-one"})
	if a.broker.MentionWatermark("one", "1700000000.000001") != "" || a.broker.MentionWatermark("two", "1700000000.000001") != "1700000000.000003" {
		t.Fatalf("watermarks after failure = %+v", a.broker.mentions)
	}
}

func TestMentionResubscribeAfterRemovalStartsFreshTranscript(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(*Adapter)
	}{
		{"exact", func(a *Adapter) { a.broker.Unsubscribe("orchestrator", mentionSubscriptionKey) }},
		{"session", func(a *Adapter) { a.broker.UnsubscribeBySession("orchestrator") }},
		{"unique", func(a *Adapter) { a.broker.UnsubscribeUniqueThread(mentionSubscriptionKey) }},
		{"sweep", func(a *Adapter) {
			a.SweepSubscriptions(context.Background(), func(context.Context) (map[string]bool, error) {
				return map[string]bool{}, nil
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subscribers.json")
			a := newTestAdapter(&Config{})
			a.broker = NewBroker(path, nil)
			a.broker.Subscribe(Subscriber{ThreadTS: mentionSubscriptionKey, SessionName: "orchestrator"})
			a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
				slackMessage("1700000000.000001", "U-root", "root"),
				slackMessage("1700000000.000002", "U-other", "earlier reply"),
			}}
			publisher := &recordingEventPublisher{}
			a.eventPublisher = publisher
			a.handleAppMention(&slackevents.AppMentionEvent{User: "U-person", Text: "<@U-bot> first", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C-one"})
			if got := a.broker.MentionWatermark("orchestrator", "1700000000.000001"); got != "1700000000.000003" {
				t.Fatalf("first watermark = %q", got)
			}

			tc.remove(a)
			a.broker = NewBroker(path, nil)
			a.broker.Subscribe(Subscriber{ThreadTS: mentionSubscriptionKey, SessionName: "orchestrator"})
			a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
				slackMessage("1700000000.000001", "U-root", "root"),
				slackMessage("1700000000.000002", "U-other", "earlier reply"),
				slackMessage("1700000000.000003", "U-person", "<@U-bot> first"),
			}}
			a.handleAppMention(&slackevents.AppMentionEvent{User: "U-person", Text: "<@U-bot> second", TimeStamp: "1700000000.000004", ThreadTimeStamp: "1700000000.000001", Channel: "C-one"})
			if len(publisher.events) != 2 {
				t.Fatalf("events = %+v", publisher.events)
			}
			assertBodyContainsInOrder(t, publisher.events[1].Body, []string{"root", "earlier reply", "first", "second"})
		})
	}
}

func TestMentionResourceAndUnboundPathsBothReceive(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
	a.broker.Subscribe(Subscriber{ThreadTS: mentionSubscriptionKey, SessionName: "orchestrator"})
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "root")}}
	a.eventPublisher = &recordingEventPublisher{}
	a.permalinkResolver = &fakePermalinkResolver{link: "https://example.slack.com/archives/CONe/p1000000001"}
	runner := &recordingMentionHookRunner{}
	a.mentionHook = runner
	stream, cancel := a.mentions.subscribe()
	defer cancel()
	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-person", Text: "<@U-bot> hello", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C-one"})
	if len(a.eventPublisher.(*recordingEventPublisher).events) != 1 || runner.calls != 1 || len(stream) != 1 {
		t.Fatalf("events=%d hook=%d stream=%d", len(a.eventPublisher.(*recordingEventPublisher).events), runner.calls, len(stream))
	}
}
