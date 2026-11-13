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
			bot := &BotAdapter{bot: Bot{ChannelID: "C1", AllowedUserIDs: []string{"U1"}}, teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(&logs, nil))}
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
			bot := &BotAdapter{bot: Bot{ChannelID: "C1", AllowedUserIDs: []string{"U1"}}, teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			bot.routeMention(&slackevents.AppMentionEvent{User: tc.user, Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "hello"})
			if len(publisher.calls) != tc.wantCalls {
				t.Fatalf("calls = %v", publisher.calls)
			}
		})
	}
}

func TestDuplicateMentionEventPublishesOnce(t *testing.T) {
	publisher := &routePublisher{counts: []int{1}}
	bot := &BotAdapter{bot: Bot{ChannelID: "C1", AllowedUserIDs: []string{"U1"}}, teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	event := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> hello"}
	bot.routeMention(event)
	bot.routeMention(event)
	if len(publisher.calls) != 1 {
		t.Fatalf("publish calls = %v", publisher.calls)
	}
}
