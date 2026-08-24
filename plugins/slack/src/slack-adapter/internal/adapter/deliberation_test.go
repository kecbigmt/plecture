package adapter

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type fakeThreadFetcher struct {
	messages []slack.Message
	names    map[string]string
	err      error
	calls    int
}

func (f *fakeThreadFetcher) fetchThreadReplies(channelID, threadTS string) ([]slack.Message, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.messages, nil
}

func (f *fakeThreadFetcher) userDisplayName(userID string) string {
	if name := f.names[userID]; name != "" {
		return name
	}
	return userID
}

type recordingEventPublisher struct {
	events []publishedEvent
}

func (p *recordingEventPublisher) PublishSessionEvent(sessionName string, ev publishedEvent) error {
	ev.SessionName = sessionName
	p.events = append(p.events, ev)
	return nil
}

func TestHandleEventsAPIAppMentionRecordedPayload(t *testing.T) {
	raw, err := os.ReadFile("testdata/app_mention_bound_thread.json")
	if err != nil {
		t.Fatal(err)
	}
	event, err := slackevents.ParseEvent(raw, slackevents.OptionNoVerifyToken())
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}

	a := newTestAdapter(&Config{})
	publisher := &recordingEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{
		messages: []slack.Message{
			slackMessage("1000.000001", "U-root", "Review thread opened"),
			slackMessage("1000.000002", "U-alice", "Looks ready."),
		},
		names: map[string]string{"U-root": "Plecture", "U-alice": "Alice", "U-dana": "Dana"},
	}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:    "1000.000001",
		ChannelID:   "C-review",
		SessionName: "owner/repo-1",
	})

	a.handleEventsAPI(event)

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.events))
	}
	assertBodyContainsInOrder(t, publisher.events[0].Body, []string{
		"Plecture: Review thread opened",
		"Alice: Looks ready.",
		"Dana: <@U-bot> please act on this",
	})
}

func TestHandleAppMentionDeliversBoundThreadTranscript(t *testing.T) {
	a := newTestAdapter(&Config{})
	fetcher := &fakeThreadFetcher{
		messages: []slack.Message{
			slackMessage("1000.000001", "U-root", "Review thread opened"),
			slackMessage("1000.000002", "U-alice", "Finding looks valid."),
			slackMessage("1000.000003", "U-bob", "Check the nil path too."),
			slackMessage("1000.000004", "U-carol", "Wording is ready."),
		},
		names: map[string]string{
			"U-root":  "Plecture",
			"U-alice": "Alice",
			"U-bob":   "Bob",
			"U-carol": "Carol",
			"U-dana":  "Dana",
		},
	}
	publisher := &recordingEventPublisher{}
	a.threadFetcher = fetcher
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:    "1000.000001",
		ChannelID:   "C-review",
		SessionName: "owner/repo-1",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> please submit this",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.events))
	}
	ev := publisher.events[0]
	if ev.SessionName != "owner/repo-1" || ev.Type != "user.emit" || ev.Source != "slack" || ev.Direction != "inbound" {
		t.Fatalf("unexpected event envelope: %+v", ev)
	}
	wantOrdered := []string{
		"Plecture: Review thread opened",
		"Alice: Finding looks valid.",
		"Bob: Check the nil path too.",
		"Carol: Wording is ready.",
		"Dana: <@U-bot> please submit this",
	}
	assertBodyContainsInOrder(t, ev.Body, wantOrdered)
	if !strings.Contains(ev.Body, "1970-01-01T00:16:40.000001Z") {
		t.Fatalf("body should include Slack message times, got:\n%s", ev.Body)
	}
	sub, _ := a.broker.Find("1000.000001")
	if sub.DeliveredThrough != "1000.000005" {
		t.Fatalf("DeliveredThrough = %q, want mention timestamp", sub.DeliveredThrough)
	}
}

func TestHandleAppMentionDeliversOnlyDeltaAfterWatermark(t *testing.T) {
	a := newTestAdapter(&Config{})
	publisher := &recordingEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{
		messages: []slack.Message{
			slackMessage("1000.000001", "U-root", "Review thread opened"),
			slackMessage("1000.000002", "U-alice", "old reply one"),
			slackMessage("1000.000003", "U-bob", "old reply two"),
			slackMessage("1000.000006", "U-alice", "new reply one"),
			slackMessage("1000.000007", "U-bob", "new reply two"),
		},
		names: map[string]string{
			"U-root":  "Plecture",
			"U-alice": "Alice",
			"U-bob":   "Bob",
			"U-dana":  "Dana",
		},
	}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:         "1000.000001",
		ChannelID:        "C-review",
		SessionName:      "owner/repo-1",
		DeliveredThrough: "1000.000005",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> send the update",
		TimeStamp:       "1000.000008",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.events))
	}
	body := publisher.events[0].Body
	assertBodyContainsInOrder(t, body, []string{
		"Plecture: Review thread opened",
		"Alice: new reply one",
		"Bob: new reply two",
		"Dana: <@U-bot> send the update",
	})
	if strings.Contains(body, "old reply") {
		t.Fatalf("body should not redeliver old replies, got:\n%s", body)
	}
}

func TestHandleAppMentionUnboundThreadDoesNotPublish(t *testing.T) {
	a := newTestAdapter(&Config{})
	publisher := &recordingEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{}
	a.eventPublisher = publisher

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if len(publisher.events) != 0 {
		t.Fatalf("published events = %d, want 0", len(publisher.events))
	}
}

func TestHandleAppMentionWrongChannelDoesNotPublish(t *testing.T) {
	a := newTestAdapter(&Config{})
	publisher := &recordingEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:    "1000.000001",
		ChannelID:   "C-bound",
		SessionName: "owner/repo-1",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-other",
	})

	if len(publisher.events) != 0 {
		t.Fatalf("published events = %d, want 0", len(publisher.events))
	}
}

func TestHandleAppMentionSubscriptionWithoutSessionNameDoesNotPublish(t *testing.T) {
	a := newTestAdapter(&Config{})
	fetcher := &fakeThreadFetcher{
		messages: []slack.Message{
			slackMessage("1000.000001", "U-root", "Review thread opened"),
		},
	}
	publisher := &recordingEventPublisher{}
	a.threadFetcher = fetcher
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:  "1000.000001",
		ChannelID: "C-review",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if fetcher.calls != 0 {
		t.Fatalf("fetchThreadReplies calls = %d, want 0", fetcher.calls)
	}
	if len(publisher.events) != 0 {
		t.Fatalf("published events = %d, want 0", len(publisher.events))
	}
}

func TestHandleAppMentionFetchFailureDoesNotPublishOrAdvanceWatermark(t *testing.T) {
	a := newTestAdapter(&Config{})
	publisher := &recordingEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{err: errors.New("slack unavailable")}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:         "1000.000001",
		ChannelID:        "C-review",
		SessionName:      "owner/repo-1",
		DeliveredThrough: "1000.000003",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if len(publisher.events) != 0 {
		t.Fatalf("published events = %d, want 0", len(publisher.events))
	}
	sub, _ := a.broker.Find("1000.000001")
	if sub.DeliveredThrough != "1000.000003" {
		t.Fatalf("DeliveredThrough = %q, want unchanged watermark", sub.DeliveredThrough)
	}
}

func TestHandleAppMentionFromBotDoesNotPublish(t *testing.T) {
	a := newTestAdapter(&Config{})
	publisher := &recordingEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:    "1000.000001",
		ChannelID:   "C-review",
		SessionName: "owner/repo-1",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-bot",
		BotID:           "B-bot",
		Text:            "<@U-bot> loop",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if len(publisher.events) != 0 {
		t.Fatalf("published events = %d, want 0", len(publisher.events))
	}
}

func TestHandleAppMentionFullThreadModeRedeliversEarlierReplies(t *testing.T) {
	a := newTestAdapter(&Config{DeliverFullThread: true})
	publisher := &recordingEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{
		messages: []slack.Message{
			slackMessage("1000.000001", "U-root", "Review thread opened"),
			slackMessage("1000.000002", "U-alice", "old reply"),
			slackMessage("1000.000006", "U-bob", "new reply"),
		},
		names: map[string]string{"U-root": "Plecture", "U-alice": "Alice", "U-bob": "Bob", "U-dana": "Dana"},
	}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{
		ThreadTS:         "1000.000001",
		ChannelID:        "C-review",
		SessionName:      "owner/repo-1",
		DeliveredThrough: "1000.000005",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> send everything",
		TimeStamp:       "1000.000008",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if len(publisher.events) != 1 {
		t.Fatalf("published events = %d, want 1", len(publisher.events))
	}
	body := publisher.events[0].Body
	if !strings.Contains(body, "Alice: old reply") || !strings.Contains(body, "Bob: new reply") {
		t.Fatalf("full-thread mode should include old and new replies, got:\n%s", body)
	}
}

func slackMessage(ts, user, text string) slack.Message {
	return slack.Message{Msg: slack.Msg{Timestamp: ts, User: user, Text: text}}
}

func assertBodyContainsInOrder(t *testing.T, body string, fragments []string) {
	t.Helper()
	pos := 0
	for _, fragment := range fragments {
		next := strings.Index(body[pos:], fragment)
		if next < 0 {
			t.Fatalf("body missing %q after offset %d:\n%s", fragment, pos, body)
		}
		pos += next + len(fragment)
	}
}
