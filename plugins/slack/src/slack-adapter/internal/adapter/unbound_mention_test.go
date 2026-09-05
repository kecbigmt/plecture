package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

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
