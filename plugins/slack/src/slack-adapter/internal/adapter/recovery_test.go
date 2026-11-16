package adapter

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type recoveryPublisher struct {
	calls  []string
	bodies []string
	fail   bool
}

func (p *recoveryPublisher) Publish(_ context.Context, resource, _, _, body string, _ map[string]string) (int, error) {
	p.calls = append(p.calls, resource)
	p.bodies = append(p.bodies, body)
	if p.fail {
		return 0, fmt.Errorf("publish failed")
	}
	return 1, nil
}

func TestRecoveryFindsRootsAndSubscribedThreadRepliesWithTranscript(t *testing.T) {
	now := time.Unix(1700001000, 0).UTC()
	var requests []string
	var logs strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		requests = append(requests, r.URL.Path+"?"+r.Form.Get("ts"))
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.history":
			fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1700000500.000001","text":"<@UBOT> root","user":"U1","reply_count":1}]}`)
		case "/conversations.replies":
			if r.Form.Get("ts") == "1699000000.000001" {
				fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1699000000.000001","text":"old root","user":"U1"},{"ts":"1700000501.000001","thread_ts":"1699000000.000001","text":"context","user":"U1"},{"ts":"1700000502.000001","thread_ts":"1699000000.000001","text":"<@UBOT> reply","user":"U1"}]}`)
			} else {
				fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1700000500.000001","text":"<@UBOT> root","user":"U1"},{"ts":"1700000503.000001","thread_ts":"1700000500.000001","text":"<@UBOT> recent reply","user":"U1"}]}`)
			}
		case "/chat.getPermalink":
			fmt.Fprint(w, `{"ok":true,"permalink":"https://example.slack.com/archives/C1/p1700000500000001"}`)
		case "/users.info":
			fmt.Fprint(w, `{"ok":true,"user":{"name":"person"}}`)
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
		}
	}))
	defer server.Close()
	store, err := NewRecoveryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &recoveryPublisher{}
	b := &BotAdapter{bot: Bot{ChannelID: "C1"}, api: slack.New("token", slack.OptionAPIURL(server.URL+"/")), teamID: "T1", botUserID: "UBOT", publisher: p, logger: slog.New(slog.NewTextHandler(&logs, nil)), recovery: store}
	b.fetcher = b
	b.linker = b
	if err := store.SetBoundary("T1/UBOT/C1", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	subs := NewSubscriptions()
	subs.Apply([]Subscription{{Resource: SubscriptionResource{Locator: "https://example.slack.com/archives/C1/p1699000000000001"}}}, nil)
	if err := b.Recover(context.Background(), now, subs); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 3 {
		t.Fatalf("published %d: %v; requests %v", len(p.calls), p.calls, requests)
	}
	if !strings.Contains(logs.String(), "recovered 3 mentions") {
		t.Fatalf("recovery log = %s", logs.String())
	}
	foundContext := false
	for _, body := range p.bodies {
		if strings.Contains(body, "context") && strings.Contains(body, "old root") {
			foundContext = true
		}
	}
	if !foundContext {
		t.Fatalf("missing transcript: %v", p.bodies)
	}
	if err := b.Recover(context.Background(), now, subs); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 3 {
		t.Fatalf("duplicate recovery: %v", p.calls)
	}
}

func TestLiveMentionIsNotReplayedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	store, err := NewRecoveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := &recoveryPublisher{}
	b := &BotAdapter{bot: Bot{ChannelID: "C1"}, teamID: "T1", botUserID: "UBOT", publisher: p, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), recovery: store}
	ev := &slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> hello"}
	b.routeMention(ev)
	reopened, err := NewRecoveryStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Seen("T1/UBOT/C1", "1700000000.000002") {
		t.Fatal("live mention was not durable")
	}
	b.recovery = reopened
	b.seen = nil
	b.routeMention(ev)
	if len(p.calls) != 1 {
		t.Fatalf("published twice: %v", p.calls)
	}
}

func TestFailedRecoveryPreservesBoundaryAndKeepsLiveRouting(t *testing.T) {
	now := time.Unix(1700001000, 0).UTC()
	var logs strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ok":false,"error":"not_in_channel"}`) }))
	defer server.Close()
	store, err := NewRecoveryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "T1/UBOT/C1"
	old := now.Add(-48 * time.Hour)
	if err := store.SetBoundary(key, old); err != nil {
		t.Fatal(err)
	}
	p := &recoveryPublisher{}
	b := &BotAdapter{bot: Bot{ChannelID: "C1"}, api: slack.New("token", slack.OptionAPIURL(server.URL+"/")), teamID: "T1", botUserID: "UBOT", publisher: p, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(&logs, nil)), recovery: store}
	if err := b.Recover(context.Background(), now, NewSubscriptions()); err == nil {
		t.Fatal("expected scan error")
	}
	if got := store.Boundary(key); !got.Equal(old) {
		t.Fatalf("boundary advanced to %v", got)
	}
	b.routeMention(&slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> hello"})
	if len(p.calls) != 1 || !strings.Contains(logs.String(), "not_in_channel") || !strings.Contains(logs.String(), "range") || !strings.Contains(logs.String(), "recovered 0 mentions") {
		t.Fatalf("calls=%v logs=%s", p.calls, logs.String())
	}
}

func TestFirstStartDoesNotFetchEarlierMessages(t *testing.T) {
	now := time.Unix(1700001000, 0).UTC()
	var calls int
	var oldest string
	var logs strings.Builder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		oldest = r.Form.Get("oldest")
		fmt.Fprint(w, `{"ok":true,"messages":[]}`)
	}))
	defer server.Close()
	store, err := NewRecoveryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := &BotAdapter{bot: Bot{ChannelID: "C1"}, api: slack.New("token", slack.OptionAPIURL(server.URL+"/")), teamID: "T1", botUserID: "UBOT", logger: slog.New(slog.NewTextHandler(&logs, nil)), recovery: store, startup: now.Add(-time.Minute)}
	if err := b.Recover(context.Background(), now, NewSubscriptions()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || oldest != slackTime(b.startup) || !store.Boundary("T1/UBOT/C1").Equal(now) || !strings.Contains(logs.String(), "first start recovery skipped") {
		t.Fatalf("calls=%d oldest=%s boundary=%v logs=%s", calls, oldest, store.Boundary("T1/UBOT/C1"), logs.String())
	}
	if err := b.Recover(context.Background(), now.Add(time.Minute), NewSubscriptions()); err != nil {
		t.Fatal(err)
	}
	if oldest != slackTime(b.startup) {
		t.Fatalf("subsequent scan reached before first start: %s", oldest)
	}
}

func TestSubscriptionDeltasSelectOnlyConfiguredChannelThreads(t *testing.T) {
	subs := NewSubscriptions()
	root := Subscription{SessionID: "session", Source: "runtime", Resource: SubscriptionResource{Locator: "https://example.slack.com/archives/C1/p1699000000000001"}}
	reply := Subscription{SessionID: "other", Source: "runtime", Resource: SubscriptionResource{Locator: "https://example.slack.com/archives/C1/p1700000000000002?thread_ts=1699000000.000001&cid=C1"}}
	other := Subscription{SessionID: "else", Source: "runtime", Resource: SubscriptionResource{Locator: "https://example.slack.com/archives/C2/p1699000000000001"}}
	subs.Apply([]Subscription{root, reply, other}, nil)
	if got := subs.ThreadRoots("C1"); len(got) != 1 || got[0] != "1699000000.000001" {
		t.Fatalf("roots=%v", got)
	}
	subs.Apply(nil, []Subscription{root, reply})
	if got := subs.ThreadRoots("C1"); len(got) != 0 {
		t.Fatalf("removed roots=%v", got)
	}
}

func TestRecoveryRetriesUnpublishedMentionWithoutAdvancingBoundary(t *testing.T) {
	now := time.Unix(1700001000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/conversations.history":
			fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1700000500.000001","text":"<@UBOT> hello","user":"U1"}]}`)
		default:
			t.Errorf("unexpected API %s", r.URL.Path)
		}
	}))
	defer server.Close()
	store, err := NewRecoveryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "T1/UBOT/C1"
	old := now.Add(-time.Hour)
	if err := store.SetBoundary(key, old); err != nil {
		t.Fatal(err)
	}
	p := &recoveryPublisher{fail: true}
	b := &BotAdapter{bot: Bot{ChannelID: "C1"}, api: slack.New("token", slack.OptionAPIURL(server.URL+"/")), teamID: "T1", botUserID: "UBOT", publisher: p, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), recovery: store}
	if err := b.Recover(context.Background(), now, NewSubscriptions()); err == nil {
		t.Fatal("expected publish failure")
	}
	if !store.Boundary(key).Equal(old) || store.Seen(key, "1700000500.000001") {
		t.Fatal("failed mention advanced recovery state")
	}
	p.fail = false
	if err := b.Recover(context.Background(), now, NewSubscriptions()); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 2 || !store.Seen(key, "1700000500.000001") {
		t.Fatalf("retry calls=%v", p.calls)
	}
}

func TestRecoveryDoesNotAdvancePastAnInFlightLiveMention(t *testing.T) {
	now := time.Unix(1700001000, 0).UTC()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"ok":true,"messages":[{"ts":"1700000500.000001","text":"<@UBOT> hello","user":"U1"}]}`)
	}))
	defer server.Close()
	store, err := NewRecoveryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := "T1/UBOT/C1"
	old := now.Add(-time.Hour)
	if err := store.SetBoundary(key, old); err != nil {
		t.Fatal(err)
	}
	p := &recoveryPublisher{}
	b := &BotAdapter{bot: Bot{ChannelID: "C1"}, api: slack.New("token", slack.OptionAPIURL(server.URL+"/")), teamID: "T1", botUserID: "UBOT", publisher: p, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), recovery: store}
	if !b.claimMention("C1/1700000500.000001") {
		t.Fatal("claim failed")
	}
	if err := b.Recover(context.Background(), now, NewSubscriptions()); err == nil {
		t.Fatal("scan advanced past live work")
	}
	if !store.Boundary(key).Equal(old) {
		t.Fatal("boundary advanced")
	}
	b.releaseMention("C1/1700000500.000001")
	if err := b.Recover(context.Background(), now, NewSubscriptions()); err != nil {
		t.Fatal(err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("calls=%v", p.calls)
	}
}
