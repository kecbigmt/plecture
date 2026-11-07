package adapter

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

const (
	longThreadTS  = "1700000000.000001"
	longMentionTS = "1700009999.000001"
)

// longThreadMessages returns a root plus enough replies that a transcript of
// all of them is larger than a single process argument may be on Linux.
func longThreadMessages() []slack.Message {
	messages := []slack.Message{slackMessage(longThreadTS, "U-root", "Root")}
	filler := strings.Repeat("x", 300)
	for i := 1; i <= 600; i++ {
		user := "U-alice"
		if i%2 == 0 {
			user = "U-bob"
		}
		messages = append(messages, slackMessage(fmt.Sprintf("%d.000001", 1700000000+i), user, fmt.Sprintf("reply-%03d %s", i, filler)))
	}
	return messages
}

// installRecordingPlect puts a stub plect first on PATH that records each
// invocation's arguments, NUL-separated, so a test exercises the real exec
// path that the adapter publishes through.
func installRecordingPlect(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	script := "#!/bin/sh\nfor arg in \"$@\"; do printf '%s\\0' \"$arg\"; done >> '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "plect"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return out
}

func recordedPublishBody(t *testing.T, argsPath string) string {
	t.Helper()
	raw, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("plect was never executed: %v", err)
	}
	args := strings.Split(string(raw), "\x00")
	for i, arg := range args {
		if arg == "--body" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatalf("plect invocation has no --body: %q", args)
	return ""
}

func TestMentionInLongThreadWithConvertedLedgerEntryIsPublished(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	// A version-1 entry converted to version 2: no delivered_through, a
	// subscription start long before the mention, and no mention watermark.
	state := `{"version":2,"subscribers":[{"thread_ts":"` + longThreadTS + `","channel_id":"C1","session_name":"work","since":"2023-11-01T00:00:00+09:00"}]}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	argsPath := installRecordingPlect(t)
	a := newTestAdapter(&Config{})
	a.broker = NewBroker(path, nil)
	a.botUserID = "U-bot"
	a.eventPublisher = cliEventPublisher{}
	fetcher := &fakeThreadFetcher{messages: longThreadMessages()}
	a.threadFetcher = fetcher

	a.handleEventsAPI(parseMentionFallbackEvent(t, `{"type":"event_callback","event":{"type":"message","user":"U-person","text":"<@U-bot> act","ts":"`+longMentionTS+`","thread_ts":"`+longThreadTS+`","channel":"C1"}}`))

	body := recordedPublishBody(t, argsPath)
	assertBodyContainsInOrder(t, body, []string{"Root", "earlier messages omitted", "reply-600", "<@U-bot> act"})
	if strings.Contains(body, "reply-001 ") {
		t.Fatalf("transcript kept the oldest reply instead of omitting it")
	}
	if sub, _ := a.broker.Find("work", longThreadTS); sub.DeliveredThrough != longMentionTS {
		t.Fatalf("watermark = %q, want mention timestamp", sub.DeliveredThrough)
	}
	if fetcher.nameCalls > 4 {
		t.Fatalf("display name lookups = %d, want at most one per distinct user", fetcher.nameCalls)
	}
}

func TestCatchUpOfLongThreadIsPublished(t *testing.T) {
	argsPath := installRecordingPlect(t)
	a := newTestAdapter(&Config{})
	a.eventPublisher = cliEventPublisher{}
	fetcher := &fakeThreadFetcher{messages: longThreadMessages()}
	a.threadFetcher = fetcher
	sub := a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: longThreadTS, ChannelID: "C1"})

	sub = a.catchUpThread(sub, "1700000600.000001")

	body := recordedPublishBody(t, argsPath)
	assertBodyContainsInOrder(t, body, []string{"Root", "earlier messages omitted", "reply-600"})
	if sub.DeliveredThrough != "1700000600.000001" {
		t.Fatalf("watermark = %q, want catch-up bound", sub.DeliveredThrough)
	}
	if fetcher.nameCalls > 3 {
		t.Fatalf("display name lookups = %d, want at most one per distinct user", fetcher.nameCalls)
	}
}

func TestShortTranscriptIsNotTruncated(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.botUserID = "U-bot"
	a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage("1700000000.000001", "U-root", "Root"),
		slackMessage("1700000000.000002", "U-other", "Earlier reply"),
	}}
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher

	a.handleEventsAPI(parseMentionFallbackEvent(t, messageMentionPayload))

	if len(publisher.events) != 1 || strings.Contains(publisher.events[0].Body, "omitted") {
		t.Fatalf("published events = %+v, want one untruncated transcript", publisher.events)
	}
}

func TestMentionEarlyReturnsLogTheirReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    Config
		setup  func(a *Adapter)
		event  *slackevents.AppMentionEvent
		reason string
	}{
		{
			name:   "bot message",
			event:  &slackevents.AppMentionEvent{BotID: "B1", User: "U-person", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C1"},
			reason: "bot_message",
		},
		{
			name:   "user not allowed",
			cfg:    Config{AllowedUserIDs: []string{"U-allowed"}},
			event:  &slackevents.AppMentionEvent{User: "U-person", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C1"},
			reason: "user_not_allowed",
		},
		{
			name:   "missing thread timestamp",
			event:  &slackevents.AppMentionEvent{User: "U-person", Channel: "C1"},
			reason: "missing_thread_ts",
		},
		{
			name: "already delivered",
			setup: func(a *Adapter) {
				a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: "1700000000.000001", ChannelID: "C1", DeliveredThrough: "1700000000.000003"})
			},
			event:  &slackevents.AppMentionEvent{User: "U-person", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C1"},
			reason: "no_pending_subscriber",
		},
		{
			name: "subscription in another channel",
			setup: func(a *Adapter) {
				a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: "1700000000.000001", ChannelID: "C2"})
			},
			event:  &slackevents.AppMentionEvent{User: "U-person", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C1"},
			reason: "no_pending_subscriber",
		},
		{
			name: "unbound mention already dispatched",
			cfg:  Config{OnUnboundMention: "/path/to/dispatch"},
			setup: func(a *Adapter) {
				a.broker.RecordPendingMention("C1", "1700000000.000001", "1700000000.000003")
			},
			event:  &slackevents.AppMentionEvent{User: "U-person", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C1"},
			reason: "already_dispatched",
		},
		{
			name:   "unbound mention without a consumer",
			event:  &slackevents.AppMentionEvent{User: "U-person", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C1"},
			reason: "no_unbound_consumer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			a := newTestAdapter(&cfg)
			a.threadFetcher = &fakeThreadFetcher{}
			a.eventPublisher = &recordingEventPublisher{}
			a.mentionHook = &recordingMentionHookRunner{}
			var log bytes.Buffer
			a.logger = slog.New(slog.NewTextHandler(&log, nil))
			if tc.setup != nil {
				tc.setup(a)
			}

			a.handleAppMention(tc.event)

			if !strings.Contains(log.String(), "level=INFO") || !strings.Contains(log.String(), "reason="+tc.reason) {
				t.Fatalf("log does not state reason %q at INFO:\n%s", tc.reason, log.String())
			}
		})
	}
}

func TestMessageMentionWithoutBotIdentityLogsItsReason(t *testing.T) {
	a := newTestAdapter(&Config{})
	var log bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&log, nil))

	a.handleMessage(&slackevents.MessageEvent{User: "U-person", Text: "<@U-bot> act", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Channel: "C1"})

	if !strings.Contains(log.String(), "level=INFO") || !strings.Contains(log.String(), "reason=bot_identity_unknown") {
		t.Fatalf("log does not state why the mention was dropped:\n%s", log.String())
	}
}

func TestDeliveredMentionIsLogged(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.botUserID = "U-bot"
	a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "Root")}}
	a.eventPublisher = &recordingEventPublisher{}
	var log bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&log, nil))

	a.handleEventsAPI(parseMentionFallbackEvent(t, messageMentionPayload))

	if !strings.Contains(log.String(), "app mention delivered") || !strings.Contains(log.String(), "session_name=work") {
		t.Fatalf("delivery was not logged:\n%s", log.String())
	}
}

func TestMessageEventMentionEntryGuardsLogTheirReason(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     Config
		payload string
		reason  string
	}{
		{
			name:    "bot message",
			payload: `{"type":"event_callback","event":{"type":"message","bot_id":"B1","user":"U-person","text":"<@U-bot> act","ts":"1700000000.000003","thread_ts":"1700000000.000001","channel":"C1"}}`,
			reason:  "bot_or_subtype",
		},
		{
			name:    "user not allowed",
			cfg:     Config{AllowedUserIDs: []string{"U-allowed"}},
			payload: messageMentionPayload,
			reason:  "user_not_allowed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			a := newTestAdapter(&cfg)
			a.botUserID = "U-bot"
			a.eventPublisher = &recordingEventPublisher{}
			var log bytes.Buffer
			a.logger = slog.New(slog.NewTextHandler(&log, nil))

			a.handleEventsAPI(parseMentionFallbackEvent(t, tc.payload))

			if !strings.Contains(log.String(), "msg=\"message mention skipped\" reason="+tc.reason) {
				t.Fatalf("log does not state reason %q at INFO:\n%s", tc.reason, log.String())
			}
		})
	}
}

// Ordinary bot posts and edits arrive constantly; only a dropped mention is
// worth an INFO line.
func TestNonMentionMessageDropStaysOutOfInfoLog(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.botUserID = "U-bot"
	var log bytes.Buffer
	a.logger = slog.New(slog.NewTextHandler(&log, nil))

	a.handleEventsAPI(parseMentionFallbackEvent(t, `{"type":"event_callback","event":{"type":"message","bot_id":"B1","user":"U-other-bot","text":"progress update","ts":"1700000000.000003","thread_ts":"1700000000.000001","channel":"C1"}}`))

	if strings.Contains(log.String(), "skipped") {
		t.Fatalf("non-mention drop logged at INFO:\n%s", log.String())
	}
}

// Each "あ" is one three-byte UTF-8 character, so these texts exercise
// a byte budget that a character count would underestimate.
func TestOversizedRetainedLinesStayWithinTranscriptBudget(t *testing.T) {
	huge := func(n int) string { return strings.Repeat("あ", n) }
	for _, tc := range []struct {
		name    string
		root    string
		mention string
	}{
		{name: "root", root: "Root " + huge(45000), mention: "<@U-bot> act"},
		{name: "mention", root: "Root", mention: "<@U-bot> act " + huge(45000)},
		{name: "root and mention", root: "Root " + huge(30000), mention: "<@U-bot> act " + huge(15000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argsPath := installRecordingPlect(t)
			a := newTestAdapter(&Config{})
			a.botUserID = "U-bot"
			a.eventPublisher = cliEventPublisher{}
			a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: longThreadTS, ChannelID: "C1"})
			a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
				slackMessage(longThreadTS, "U-root", tc.root),
				slackMessage("1700000001.000001", "U-alice", "reply"),
			}}

			a.handleAppMention(&slackevents.AppMentionEvent{User: "U-person", Text: tc.mention, TimeStamp: longMentionTS, ThreadTimeStamp: longThreadTS, Channel: "C1"})

			body := recordedPublishBody(t, argsPath)
			assertWithinTranscriptBudget(t, body)
			assertBodyContainsInOrder(t, body, []string{"Root", "<@U-bot> act"})
			if sub, _ := a.broker.Find("work", longThreadTS); sub.DeliveredThrough != longMentionTS {
				t.Fatalf("watermark = %q, want mention timestamp", sub.DeliveredThrough)
			}
		})
	}
}

func TestCatchUpWithOversizedRootStaysWithinTranscriptBudget(t *testing.T) {
	argsPath := installRecordingPlect(t)
	a := newTestAdapter(&Config{})
	a.eventPublisher = cliEventPublisher{}
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage(longThreadTS, "U-root", "Root "+strings.Repeat("あ", 45000)),
		slackMessage("1700000001.000001", "U-alice", "reply"),
	}}
	sub := a.broker.Subscribe(Subscriber{SessionName: "work", ThreadTS: longThreadTS, ChannelID: "C1"})

	sub = a.catchUpThread(sub, "1700000001.000001")

	body := recordedPublishBody(t, argsPath)
	assertWithinTranscriptBudget(t, body)
	if !strings.Contains(body, "Root") || sub.DeliveredThrough != "1700000001.000001" {
		t.Fatalf("catch-up body or watermark wrong: watermark=%q", sub.DeliveredThrough)
	}
}

func assertWithinTranscriptBudget(t *testing.T, body string) {
	t.Helper()
	if len(body) > maxTranscriptBytes {
		t.Fatalf("transcript is %d bytes, over the %d-byte budget", len(body), maxTranscriptBytes)
	}
	if !utf8.ValidString(body) {
		t.Fatalf("transcript was cut inside a UTF-8 sequence")
	}
}
