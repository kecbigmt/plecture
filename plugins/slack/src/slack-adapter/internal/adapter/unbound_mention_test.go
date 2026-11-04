package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestUnboundMentionIsCaughtUpOnceAfterAdapterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	first := newTestAdapter(&Config{})
	first.broker = NewBroker(path, first.logger)
	first.permalinkResolver = &fakePermalinkResolver{link: "https://example.slack.com/archives/C1/p1700000000000001"}
	appearances, cancel := first.mentions.subscribe()
	defer cancel()
	first.handleAppMention(&slackevents.AppMentionEvent{
		User: "U1", Text: "<@U-bot> start", Channel: "C1",
		TimeStamp: "1700000001.000001", ThreadTimeStamp: "1700000000.000001",
	})
	item := <-appearances
	if item.ChannelID != "C1" || item.ThreadTS != "1700000000.000001" || item.MentionTS != "1700000001.000001" {
		t.Fatalf("population appearance = %+v", item)
	}

	a := newTestAdapter(&Config{})
	a.broker = NewBroker(path, a.logger)
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage("1700000000.000001", "U0", "root"),
		slackMessage("1700000001.000001", "U1", "<@U-bot> start"),
	}}
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	server := httptest.NewServer(http.HandlerFunc(a.HandleSubscribe))
	defer server.Close()
	for range 2 {
		if err := ThreadSubscription(context.Background(), server.Client(), server.URL, "subscribe", "population-session", item.Resource, ""); err != nil {
			t.Fatalf("resource subscription: %v", err)
		}
	}
	if len(publisher.events) != 1 || publisher.events[0].Type != "user.emit" || publisher.events[0].SessionName != "population-session" || !strings.Contains(publisher.events[0].Body, "<@U-bot> start") {
		t.Fatalf("events = %+v, want one triggering mention for the new session", publisher.events)
	}
}

func TestPendingMentionRemainsAfterFailedCatchUp(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.broker.RecordPendingMention("C1", "1700000000.000001", "1700000001.000001")
	fetcher := &fakeThreadFetcher{err: errors.New("Slack unavailable")}
	a.threadFetcher = fetcher
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	request := `{"session_name":"population-session","channel_id":"C1","thread_ts":"1700000000.000001"}`
	subscribe := func() {
		w := httptest.NewRecorder()
		a.HandleSubscribe(w, httptest.NewRequest(http.MethodPost, "/subscribe", strings.NewReader(request)))
		if w.Code != http.StatusOK {
			t.Fatalf("subscribe: %d %s", w.Code, w.Body.String())
		}
	}
	subscribe()
	if got := a.broker.PendingMention("C1", "1700000000.000001"); got != "1700000001.000001" {
		t.Fatalf("pending mention after failed fetch = %q", got)
	}
	fetcher.err = nil
	fetcher.messages = []slack.Message{slackMessage("1700000001.000001", "U1", "<@U-bot> start")}
	subscribe()
	if len(publisher.events) != 1 || a.broker.PendingMention("C1", "1700000000.000001") != "" {
		t.Fatalf("events = %+v, pending = %q", publisher.events, a.broker.PendingMention("C1", "1700000000.000001"))
	}
}

func TestPendingMentionExpiresBeforeSubscription(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	first := NewBroker(path, nil)
	first.RecordPendingMention("C1", "1700000000.000001", "1700000001.000001")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	state.PendingMentions[0].Recorded = time.Now().Add(-pendingMentionLifetime - time.Minute)
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	reloaded := NewBroker(path, nil)
	if got := reloaded.PendingMention("C1", "1700000000.000001"); got != "" {
		t.Fatalf("expired pending mention = %q", got)
	}
}

type fakePermalinkResolver struct {
	link  string
	err   error
	calls int
	// last records the arguments of the most recent call, for assertions
	// that need to see what was asked for, not just how many times.
	last struct{ channelID, ts string }
}

func (f *fakePermalinkResolver) permalink(channelID, ts string) (string, error) {
	f.calls++
	f.last = struct{ channelID, ts string }{channelID, ts}
	if f.err != nil {
		return "", f.err
	}
	return f.link, nil
}

type recordingMentionHookRunner struct {
	err     error
	calls   int
	command string
	payload []byte
}

func (r *recordingMentionHookRunner) Run(command string, payload []byte) error {
	r.calls++
	r.command = command
	r.payload = payload
	return r.err
}

func TestHandleAppMentionRunsOnUnboundMentionHookForUnboundThread(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
	a.threadFetcher = &fakeThreadFetcher{}
	resolver := &fakePermalinkResolver{link: "https://example.slack.com/archives/C-review/p1000000001"}
	runner := &recordingMentionHookRunner{}
	a.permalinkResolver = resolver
	a.mentionHook = runner

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if runner.calls != 1 {
		t.Fatalf("hook calls = %d, want 1", runner.calls)
	}
	if runner.command != "/path/to/dispatch" {
		t.Errorf("command = %q, want the configured on_unbound_mention path", runner.command)
	}
	if resolver.last.channelID != "C-review" || resolver.last.ts != "1000.000001" {
		t.Errorf("permalink lookup = (%q, %q), want (C-review, thread root 1000.000001)", resolver.last.channelID, resolver.last.ts)
	}

	var payload unboundMentionPayload
	if err := json.Unmarshal(runner.payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	want := unboundMentionPayload{
		ChannelID: "C-review",
		ThreadTS:  "1000.000001",
		TS:        "1000.000005",
		User:      "U-dana",
		Text:      "<@U-bot> hello",
		Permalink: "https://example.slack.com/archives/C-review/p1000000001",
	}
	if payload != want {
		t.Errorf("payload = %+v, want %+v", payload, want)
	}
}

func TestHandleAppMentionTopLevelMentionSetsThreadTSToMessageTS(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
	a.threadFetcher = &fakeThreadFetcher{}
	resolver := &fakePermalinkResolver{link: "https://example.slack.com/archives/C-review/p1000000005"}
	runner := &recordingMentionHookRunner{}
	a.permalinkResolver = resolver
	a.mentionHook = runner

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:      "U-dana",
		Text:      "<@U-bot> hello",
		TimeStamp: "1000.000005",
		Channel:   "C-review",
	})

	var payload unboundMentionPayload
	if err := json.Unmarshal(runner.payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ThreadTS != "1000.000005" {
		t.Errorf("thread_ts = %q, want the message ts (1000.000005) for a top-level mention", payload.ThreadTS)
	}
	if payload.TS != "1000.000005" {
		t.Errorf("ts = %q, want 1000.000005", payload.TS)
	}
}

func TestHandleAppMentionBoundThreadDoesNotRunOnUnboundMentionHook(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
	a.threadFetcher = &fakeThreadFetcher{
		messages: []slack.Message{
			slackMessage("1000.000001", "U-root", "Review thread opened"),
		},
	}
	runner := &recordingMentionHookRunner{}
	a.mentionHook = runner
	a.eventPublisher = &recordingEventPublisher{}
	a.broker.Subscribe(Subscriber{
		ThreadTS:    "1000.000001",
		ChannelID:   "C-review",
		SessionName: "owner/repo-1",
	})

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if runner.calls != 0 {
		t.Fatalf("hook calls = %d, want 0 for a mention in a bound thread", runner.calls)
	}
}

func TestHandleAppMentionOnUnboundMentionUnsetDoesNotRunHook(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.threadFetcher = &fakeThreadFetcher{}
	runner := &recordingMentionHookRunner{}
	a.mentionHook = runner

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if runner.calls != 0 {
		t.Fatalf("hook calls = %d, want 0 when on_unbound_mention is unset", runner.calls)
	}
}

func TestHandleAppMentionDisallowedUserDoesNotRunHook(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch", AllowedUserIDs: []string{"U-allowed"}})
	a.threadFetcher = &fakeThreadFetcher{}
	runner := &recordingMentionHookRunner{}
	a.mentionHook = runner

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-outsider",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if runner.calls != 0 {
		t.Fatalf("hook calls = %d, want 0 for a user outside allowed_user_ids", runner.calls)
	}
}

func TestDispatchUnboundMentionSkipsHookWhenPermalinkLookupFails(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
	a.threadFetcher = &fakeThreadFetcher{}
	resolver := &fakePermalinkResolver{err: errors.New("permalink unavailable")}
	runner := &recordingMentionHookRunner{}
	a.permalinkResolver = resolver
	a.mentionHook = runner

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if runner.calls != 0 {
		t.Fatalf("hook calls = %d, want 0 when the permalink lookup fails", runner.calls)
	}
}

func TestHandleAppMentionPublishesToStreamAlongsideOnUnboundMentionHook(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
	a.threadFetcher = &fakeThreadFetcher{}
	a.permalinkResolver = &fakePermalinkResolver{link: "https://example.slack.com/archives/C-review/p1000000001"}
	a.mentionHook = &recordingMentionHookRunner{}
	ch, cancel := a.mentions.subscribe()
	defer cancel()

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	runner := a.mentionHook.(*recordingMentionHookRunner)
	if runner.calls != 1 {
		t.Fatalf("hook calls = %d, want 1 (coexistence: the hook still fires)", runner.calls)
	}

	select {
	case item := <-ch:
		want := unboundMentionItem{
			Resource:  "https://example.slack.com/archives/C-review/p1000000001",
			ChannelID: "C-review",
			ThreadTS:  "1000.000001",
			MentionTS: "1000.000005",
			UserID:    "U-dana",
		}
		if item != want {
			t.Errorf("stream item = %+v, want %+v", item, want)
		}
	default:
		t.Fatal("stream received nothing (coexistence: the stream must also fire)")
	}
}

func TestHandleAppMentionPublishesToStreamWithoutOnUnboundMentionConfigured(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.threadFetcher = &fakeThreadFetcher{}
	a.permalinkResolver = &fakePermalinkResolver{link: "https://example.slack.com/archives/C-review/p1000000001"}
	ch, cancel := a.mentions.subscribe()
	defer cancel()

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	select {
	case <-ch:
	default:
		t.Fatal("stream received nothing; a subscribe-only deployment (no on_unbound_mention) must still see the mention")
	}
}

func TestHandleAppMentionSkipsPermalinkLookupWithNoHookAndNoStreamReaders(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.threadFetcher = &fakeThreadFetcher{}
	resolver := &fakePermalinkResolver{link: "https://example.slack.com/archives/C-review/p1000000001"}
	a.permalinkResolver = resolver

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if resolver.calls != 0 {
		t.Fatalf("permalink lookups = %d, want 0 when neither the hook nor the stream would use it", resolver.calls)
	}
}

func TestDispatchUnboundMentionCommandFailureIsLoggedAndNotRetried(t *testing.T) {
	a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
	a.threadFetcher = &fakeThreadFetcher{}
	a.permalinkResolver = &fakePermalinkResolver{link: "https://example.slack.com/archives/C-review/p1000000001"}
	runner := &recordingMentionHookRunner{err: errors.New("dispatch command failed")}
	a.mentionHook = runner

	var logs bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&logs, nil))

	a.handleAppMention(&slackevents.AppMentionEvent{
		User:            "U-dana",
		Text:            "<@U-bot> hello",
		TimeStamp:       "1000.000005",
		ThreadTimeStamp: "1000.000001",
		Channel:         "C-review",
	})

	if runner.calls != 1 {
		t.Fatalf("hook calls = %d, want exactly 1 (no retry) when the command exits with an error", runner.calls)
	}
	if !strings.Contains(logs.String(), "dispatch command failed") {
		t.Errorf("expected the command's exit error to be logged, got: %q", logs.String())
	}
}
