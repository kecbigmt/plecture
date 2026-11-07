package adapter

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type failOnceMentionPublisher struct {
	events []publishedEvent
	fail   bool
}

func (p *failOnceMentionPublisher) PublishSessionEvent(sessionName string, ev publishedEvent) error {
	if p.fail {
		p.fail = false
		return errors.New("temporary failure")
	}
	ev.SessionName = sessionName
	p.events = append(p.events, ev)
	return nil
}

func TestBoundThreadMentionDeliversFromMessageEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	state := `{"version":2,"subscribers":[{"session_name":"work","thread_ts":"1700000000.000001","channel_id":"C1"}]}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestAdapter(&Config{})
	a.broker = NewBroker(path, nil)
	a.botUserID = "U-bot"
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage("1700000000.000001", "U-root", "Root"),
		slackMessage("1700000000.000002", "U-other", "Earlier reply"),
	}}
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher

	a.handleEventsAPI(parseMentionFallbackEvent(t, messageMentionPayload))

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %+v, want one user.emit", publisher.events)
	}
	ev := publisher.events[0]
	if ev.SessionName != "work" || ev.Type != "user.emit" || ev.Meta["mention_ts"] != "1700000000.000003" {
		t.Fatalf("published event = %+v", ev)
	}
	assertBodyContainsInOrder(t, ev.Body, []string{"Root", "Earlier reply", "<@U-bot> act"})
	if sub, _ := a.broker.Find("work", "1700000000.000001"); sub.DeliveredThrough != "1700000000.000003" {
		t.Fatalf("watermark = %q, want mention timestamp", sub.DeliveredThrough)
	}
}

func TestUnboundMessageMentionDeliversToMentionSubscriber(t *testing.T) {
	for _, payload := range []string{
		topLevelMessageMentionPayload,
		messageMentionPayload,
	} {
		t.Run(payload, func(t *testing.T) {
			a := newTestAdapter(&Config{})
			a.botUserID = "U-bot"
			a.broker.Subscribe(Subscriber{SessionName: "observer", ThreadTS: mentionSubscriptionKey})
			rootTS := "1700000000.000001"
			if payload == topLevelMessageMentionPayload {
				rootTS = "1700000000.000003"
			}
			a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage(rootTS, "U-person", "<@U-bot> act")}}
			publisher := &recordingEventPublisher{}
			a.eventPublisher = publisher

			a.handleEventsAPI(parseMentionFallbackEvent(t, payload))

			if len(publisher.events) != 1 || publisher.events[0].SessionName != "observer" || publisher.events[0].Type != "user.emit" {
				t.Fatalf("published events = %+v, want one mention delivery", publisher.events)
			}
			if publisher.events[0].Meta["mention_ts"] != "1700000000.000003" {
				t.Fatalf("metadata = %+v", publisher.events[0].Meta)
			}
			if payload == topLevelMessageMentionPayload && bytes.Count([]byte(publisher.events[0].Body), []byte("<@U-bot> act")) != 1 {
				t.Fatalf("top-level transcript repeats mention: %q", publisher.events[0].Body)
			}
		})
	}
}

func TestUnboundMentionIsPublishedOnceAcrossEventTypes(t *testing.T) {
	for _, order := range []struct{ first, last string }{
		{messageMentionPayload, appMentionPayload},
		{appMentionPayload, messageMentionPayload},
		{topLevelMessageMentionPayload, topLevelAppMentionPayload},
		{topLevelAppMentionPayload, topLevelMessageMentionPayload},
	} {
		a := newTestAdapter(&Config{})
		a.botUserID = "U-bot"
		a.broker.Subscribe(Subscriber{SessionName: "observer", ThreadTS: mentionSubscriptionKey})
		a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "Root")}}
		publisher := &recordingEventPublisher{}
		a.eventPublisher = publisher
		a.handleEventsAPI(parseMentionFallbackEvent(t, order.first))
		a.handleEventsAPI(parseMentionFallbackEvent(t, order.last))
		if len(publisher.events) != 1 {
			t.Fatalf("order %q then %q: events = %+v", order.first, order.last, publisher.events)
		}
	}
}

func TestUnboundMessageMentionPublishFailureCanRetryFromAppMention(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.botUserID = "U-bot"
	a.broker.Subscribe(Subscriber{SessionName: "observer", ThreadTS: mentionSubscriptionKey})
	a.threadFetcher = &fakeThreadFetcher{}
	publisher := &failOnceMentionPublisher{fail: true}
	a.eventPublisher = publisher
	a.handleEventsAPI(parseMentionFallbackEvent(t, topLevelMessageMentionPayload))
	if through := a.broker.MentionWatermark("observer", "1700000000.000003"); through != "" {
		t.Fatalf("failed publication advanced watermark to %q", through)
	}
	a.handleEventsAPI(parseMentionFallbackEvent(t, topLevelAppMentionPayload))
	if len(publisher.events) != 1 || publisher.events[0].Type != "user.emit" {
		t.Fatalf("retry events = %+v, want one user.emit", publisher.events)
	}
}

func TestMentionLogIdentifiesEventType(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.botUserID = "U-bot"
	a.broker.Subscribe(Subscriber{SessionName: "observer", ThreadTS: mentionSubscriptionKey})
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "Root")}}
	a.eventPublisher = &recordingEventPublisher{}
	var log bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&log, nil))
	a.handleEventsAPI(parseMentionFallbackEvent(t, messageMentionPayload))
	if !bytes.Contains(log.Bytes(), []byte("received message mention")) {
		t.Fatalf("message mention log = %s", log.String())
	}
	log.Reset()
	a.handleEventsAPI(parseMentionFallbackEvent(t, appMentionPayload))
	if !bytes.Contains(log.Bytes(), []byte("received app mention")) {
		t.Fatalf("app mention log = %s", log.String())
	}
}

func TestBoundThreadMentionIsPublishedOnceAcrossEventTypesAndRestart(t *testing.T) {
	for _, order := range []struct {
		name  string
		first string
		last  string
	}{
		{"message first", messageMentionPayload, appMentionPayload},
		{"app mention first", appMentionPayload, messageMentionPayload},
	} {
		t.Run(order.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subscribers.json")
			a := newTestAdapter(&Config{})
			a.broker = NewBroker(path, nil)
			a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: "1700000000.000001", ChannelID: "C1"})
			a.broker.Subscribe(Subscriber{SessionName: "observer", ThreadTS: mentionSubscriptionKey})
			a.botUserID = "U-bot"
			a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "Root")}}
			publisher := &recordingEventPublisher{}
			a.eventPublisher = publisher

			a.handleEventsAPI(parseMentionFallbackEvent(t, order.first))
			a.broker = NewBroker(path, nil)
			a.handleEventsAPI(parseMentionFallbackEvent(t, order.last))

			if len(publisher.events) != 2 {
				t.Fatalf("published events = %+v, want one per subscriber", publisher.events)
			}
			for _, ev := range publisher.events {
				if ev.Type != "user.emit" || ev.Meta["mention_ts"] != "1700000000.000003" {
					t.Fatalf("published event = %+v", ev)
				}
			}
		})
	}
}

func TestMessageMentionPublishFailureCanRetryFromAppMention(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.botUserID = "U-bot"
	a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "Root")}}
	publisher := &failOnceMentionPublisher{fail: true}
	a.eventPublisher = publisher

	a.handleEventsAPI(parseMentionFallbackEvent(t, messageMentionPayload))
	if sub, _ := a.broker.Find("work", "1700000000.000001"); sub.DeliveredThrough != "" {
		t.Fatalf("failed publication advanced watermark to %q", sub.DeliveredThrough)
	}
	a.handleEventsAPI(parseMentionFallbackEvent(t, appMentionPayload))
	if len(publisher.events) != 1 || publisher.events[0].Type != "user.emit" {
		t.Fatalf("retry events = %+v, want one user.emit", publisher.events)
	}
}

func parseMentionFallbackEvent(t *testing.T, raw string) slackevents.EventsAPIEvent {
	t.Helper()
	ev, err := slackevents.ParseEvent([]byte(raw), slackevents.OptionNoVerifyToken())
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

const messageMentionPayload = `{"type":"event_callback","event":{"type":"message","user":"U-person","text":"<@U-bot> act","ts":"1700000000.000003","thread_ts":"1700000000.000001","channel":"C1"}}`
const appMentionPayload = `{"type":"event_callback","event":{"type":"app_mention","user":"U-person","text":"<@U-bot> act","ts":"1700000000.000003","thread_ts":"1700000000.000001","channel":"C1"}}`
const topLevelMessageMentionPayload = `{"type":"event_callback","event":{"type":"message","user":"U-person","text":"<@U-bot> act","ts":"1700000000.000003","channel":"C1"}}`
const topLevelAppMentionPayload = `{"type":"event_callback","event":{"type":"app_mention","user":"U-person","text":"<@U-bot> act","ts":"1700000000.000003","channel":"C1"}}`
