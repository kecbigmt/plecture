package adapter

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type routePublisher struct {
	calls  []string
	counts []int
	err    error
}

func (p *routePublisher) Publish(_ context.Context, resource, _, _, _ string, _ map[string]string) (int, error) {
	p.calls = append(p.calls, resource)
	if p.err != nil {
		return 0, p.err
	}
	count := p.counts[0]
	p.counts = p.counts[1:]
	return count, nil
}

type routeFetcher struct{}

func (routeFetcher) fetchThreadReplies(_, _ string) ([]slack.Message, error) {
	return []slack.Message{{Msg: slack.Msg{Timestamp: "1700000000.000001", Text: "root", User: "U1"}}}, nil
}
func (routeFetcher) userDisplayName(user string) string { return user }

type routePermalink struct{}

func (routePermalink) permalink(_, _ string) (string, error) {
	return "https://example.slack.com/archives/C1/p1700000000000001", nil
}

func TestMentionRoutesThreadThenBotAndDropsOnlyAfterBothMiss(t *testing.T) {
	for _, tc := range []struct {
		name    string
		counts  []int
		want    []string
		wantLog string
	}{
		{"thread", []int{1}, []string{"https://example.slack.com/archives/C1/p1700000000000001"}, ""},
		{"bot", []int{0, 1}, []string{"https://example.slack.com/archives/C1/p1700000000000001", "slack://T1/UBOT"}, ""},
		{"drop", []int{0, 0}, []string{"https://example.slack.com/archives/C1/p1700000000000001", "slack://T1/UBOT"}, "no_subscriber"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs strings.Builder
			publisher := &routePublisher{counts: tc.counts}
			bot := &BotAdapter{bot: Bot{ChannelIDs: []string{"C1"}, AllowedUserIDs: []string{"U1"}}, teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(&logs, nil))}
			bot.routeMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> hello"})
			if strings.Join(publisher.calls, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("routes = %v, want %v", publisher.calls, tc.want)
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Fatalf("log = %s", logs.String())
			}
		})
	}
}

func TestMentionDoesNotFallBackAfterPublishFailureOrFailedAllowCheck(t *testing.T) {
	for _, tc := range []struct {
		name      string
		user      string
		failure   error
		wantCalls int
	}{
		{"publish failure", "U1", errors.New("storage unavailable"), 1},
		{"denied user", "U2", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			publisher := &routePublisher{counts: []int{0}, err: tc.failure}
			bot := &BotAdapter{bot: Bot{ChannelIDs: []string{"C1"}, AllowedUserIDs: []string{"U1"}}, teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			bot.routeMention(&slackevents.AppMentionEvent{User: tc.user, Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "hello"})
			if len(publisher.calls) != tc.wantCalls {
				t.Fatalf("calls = %v", publisher.calls)
			}
		})
	}
}

func TestDuplicateMentionEventPublishesOnce(t *testing.T) {
	publisher := &routePublisher{counts: []int{1}}
	bot := &BotAdapter{bot: Bot{ChannelIDs: []string{"C1"}, AllowedUserIDs: []string{"U1"}}, teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	event := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> hello"}
	bot.routeMention(event)
	bot.routeMention(event)
	if len(publisher.calls) != 1 {
		t.Fatalf("publish calls = %v", publisher.calls)
	}
}

func TestCoveredChannelKeepsFirstMentionPendingUntilThreadSubscription(t *testing.T) {
	dir := t.TempDir()
	pending, err := NewPendingStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := NewRecoveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	bots := []Bot{{ChannelIDs: []string{"C1", "C2"}}}
	coverage := NewCoverage(bots, "/run/feed.sock")
	if err := coverage.Replace([]QueryRegistration{{ID: "ops/mentions", Inputs: map[string]any{"socket_path": "/run/feed.sock", "channel_ids": []any{"C1"}}}}); err != nil {
		t.Fatal(err)
	}
	publisher := &routePublisher{counts: []int{0, 1, 1, 0, 1}}
	subs := NewSubscriptions()
	bot := &BotAdapter{bot: bots[0], teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), pending: pending, recovery: recovery, coverage: coverage, subs: subs}
	first := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> first"}
	bot.routeMention(first)
	if len(publisher.calls) != 1 || len(pending.List()) != 1 || !strings.Contains(pending.List()[0].Body, "root") {
		t.Fatalf("before subscription: calls = %v, pending = %+v", publisher.calls, pending.List())
	}
	subs.Apply([]Subscription{{SessionID: "session-1", Resource: SubscriptionResource{Locator: "https://example.slack.com/archives/C1/p1700000000000001"}}}, nil)
	if err := bot.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 2 || len(pending.List()) != 0 || !recovery.Seen("T1/UBOT/C1", first.TimeStamp) {
		t.Fatalf("after subscription: calls = %v, pending = %+v", publisher.calls, pending.List())
	}
	second := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000003", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> later"}
	bot.routeMention(second)
	if len(publisher.calls) != 3 || len(pending.List()) != 0 {
		t.Fatalf("later mention: calls = %v, pending = %+v", publisher.calls, pending.List())
	}
	other := &slackevents.AppMentionEvent{User: "U1", Channel: "C2", TimeStamp: "1700000000.000004", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> bot"}
	bot.routeMention(other)
	if len(publisher.calls) != 5 || publisher.calls[4] != "slack://T1/UBOT" {
		t.Fatalf("bot fallback calls = %v", publisher.calls)
	}
}

func TestPendingReplayClearsAlreadyRecordedMentionWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	pending, err := NewPendingStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := NewRecoveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	mention := PendingMention{Key: "T1/UBOT/C1/1700000000.000002", Resource: "https://example.slack.com/archives/C1/p1700000000000001", ChannelID: "C1", ThreadTS: "1700000000.000001", MentionTS: "1700000000.000002"}
	if err := pending.Put(mention); err != nil {
		t.Fatal(err)
	}
	if err := recovery.Record("T1/UBOT/C1", mention.MentionTS); err != nil {
		t.Fatal(err)
	}
	publisher := &routePublisher{}
	bot := &BotAdapter{bot: Bot{ChannelIDs: []string{"C1"}}, teamID: "T1", botUserID: "UBOT", pending: pending, recovery: recovery, subs: NewSubscriptions(), publisher: publisher}
	if err := bot.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 0 || len(pending.List()) != 0 {
		t.Fatalf("calls = %v, pending = %+v", publisher.calls, pending.List())
	}
}

func TestRecoveredPendingMentionIsNotPublishedBeforeReplay(t *testing.T) {
	pending, err := NewPendingStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mention := PendingMention{Key: "T1/UBOT/C1/1700000000.000002", ChannelID: "C1", MentionTS: "1700000000.000002"}
	if err := pending.Put(mention); err != nil {
		t.Fatal(err)
	}
	publisher := &routePublisher{}
	bot := &BotAdapter{bot: Bot{ChannelIDs: []string{"C1"}}, teamID: "T1", botUserID: "UBOT", pending: pending, publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	bot.routeMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: mention.MentionTS, ThreadTimeStamp: "1700000000.000001"})
	if len(publisher.calls) != 0 {
		t.Fatalf("published pending recovery directly: %v", publisher.calls)
	}
}

func TestPendingMentionWithoutSubscriberRemainsQueuedAndIsLogged(t *testing.T) {
	pending, err := NewPendingStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mention := PendingMention{Key: "T1/UBOT/C1/1700000000.000002", Resource: "https://example.slack.com/archives/C1/p1700000000000001", ChannelID: "C1", ThreadTS: "1700000000.000001", MentionTS: "1700000000.000002"}
	if err := pending.Put(mention); err != nil {
		t.Fatal(err)
	}
	coverage := NewCoverage([]Bot{{ChannelIDs: []string{"C1"}}}, "/run/feed.sock")
	if err := coverage.Replace([]QueryRegistration{{ID: "ops/mentions", Inputs: map[string]any{"socket_path": "/run/feed.sock", "channel_ids": []any{"C1"}}}}); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	publisher := &routePublisher{}
	bot := &BotAdapter{bot: Bot{ChannelIDs: []string{"C1"}}, teamID: "T1", botUserID: "UBOT", pending: pending, coverage: coverage, subs: NewSubscriptions(), publisher: publisher, logger: slog.New(slog.NewTextHandler(&logs, nil))}
	if err := bot.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 0 || len(pending.List()) != 1 || !strings.Contains(logs.String(), "awaiting thread subscription") {
		t.Fatalf("calls = %v, pending = %+v, log = %s", publisher.calls, pending.List(), logs.String())
	}
}
