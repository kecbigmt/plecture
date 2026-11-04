package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/slack-go/slack"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/slack-go/slack/slackevents"
)

func TestThreadSubscriptionsKeepIndependentWatermarks(t *testing.T) {
	b := NewBroker("", nil)
	b.Subscribe(Subscriber{SessionName: "one", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	b.Subscribe(Subscriber{SessionName: "two", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	if got := len(b.List()); got != 2 {
		t.Fatalf("subscriptions = %d, want 2", got)
	}
	b.MarkDelivered("one", "1700000000.000001", "1700000001.000001")
	var one, two Subscriber
	for _, sub := range b.List() {
		if sub.SessionName == "one" {
			one = sub
		} else {
			two = sub
		}
	}
	if one.DeliveredThrough == two.DeliveredThrough {
		t.Fatalf("watermarks are shared: one=%q two=%q", one.DeliveredThrough, two.DeliveredThrough)
	}
}

func TestReplyWithoutRuntimeSocketRemainsSubscribed(t *testing.T) {
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U1"}})
	a.api = fakeSlackAPI(t)
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{SessionName: "one", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	a.handleMessage(&slackevents.MessageEvent{User: "U1", Text: "reply", ThreadTimeStamp: "1700000000.000001", Channel: "C1"})
	if len(publisher.events) != 1 || publisher.events[0].Type != "slack.message" {
		t.Fatalf("events = %+v", publisher.events)
	}
	if len(a.broker.List()) != 1 {
		t.Fatal("subscription was dropped")
	}
}

func TestThreadMentionPublishesForEachSessionWithoutDuplicateMessage(t *testing.T) {
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U1"}})
	a.api = fakeSlackAPI(t)
	a.botUserID = "U-bot"
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage("1700000000.000001", "U-root", "Root"),
		slackMessage("1700000001.000001", "U2", "Earlier reply"),
	}}
	for _, session := range []string{"one", "two"} {
		a.broker.Subscribe(Subscriber{SessionName: session, ThreadTS: "1700000000.000001", ChannelID: "C1"})
	}
	a.broker.MarkDelivered("one", "1700000000.000001", "1700000001.000001")
	a.handleMessage(&slackevents.MessageEvent{User: "U1", Text: "<@U-bot> act", ThreadTimeStamp: "1700000000.000001", Channel: "C1"})
	if len(publisher.events) != 0 {
		t.Fatalf("mention was captured as slack.message: %+v", publisher.events)
	}
	a.handleAppMention(&slackevents.AppMentionEvent{User: "U1", Text: "<@U-bot> act", TimeStamp: "1700000002.000001", ThreadTimeStamp: "1700000000.000001", Channel: "C1"})
	if len(publisher.events) != 2 {
		t.Fatalf("events = %+v", publisher.events)
	}
	for _, ev := range publisher.events {
		if ev.Type != "user.emit" || ev.Meta["channel_id"] != "C1" || ev.Meta["thread_ts"] != "1700000000.000001" {
			t.Fatalf("event = %+v", ev)
		}
	}
	one, _ := a.broker.Find("one", "1700000000.000001")
	two, _ := a.broker.Find("two", "1700000000.000001")
	if one.DeliveredThrough != "1700000002.000001" || two.DeliveredThrough != "1700000002.000001" {
		t.Fatalf("watermarks: one=%q two=%q", one.DeliveredThrough, two.DeliveredThrough)
	}
}

func TestThreadReplyDoesNotAdvanceWatermark(t *testing.T) {
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U1"}})
	a.api = fakeSlackAPI(t)
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{SessionName: "one", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	a.handleMessage(&slackevents.MessageEvent{User: "U1", Text: "reply", ThreadTimeStamp: "1700000000.000001", Channel: "C1"})
	sub, _ := a.broker.Find("one", "1700000000.000001")
	if len(publisher.events) != 1 || publisher.events[0].Type != "slack.message" || sub.DeliveredThrough != "" {
		t.Fatalf("events=%+v watermark=%q", publisher.events, sub.DeliveredThrough)
	}
}

func TestThreadSubscriptionSurvivesRestartAndSweepKeepsDownSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	first := NewBroker(path, nil)
	first.Subscribe(Subscriber{SessionName: "down", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	first.Subscribe(Subscriber{SessionName: "gone", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	a := newTestAdapter(&Config{})
	a.broker = NewBroker(path, nil)
	a.SweepSubscriptions(context.Background(), func(context.Context) (map[string]bool, error) { return map[string]bool{"down": true}, nil })
	if len(a.broker.List()) != 1 {
		t.Fatalf("subscriptions after sweep = %+v", a.broker.List())
	}
	restored := NewBroker(path, nil)
	if _, ok := restored.Find("down", "1700000000.000001"); !ok {
		t.Fatal("down session subscription did not survive restart")
	}
	if _, ok := restored.Find("gone", "1700000000.000001"); ok {
		t.Fatal("destroyed session survived sweep")
	}
}

func TestThreadSubscriptionHTTPUsesSessionAndThreadIdentity(t *testing.T) {
	a := newTestAdapter(&Config{})
	for _, session := range []string{"one", "two"} {
		body := strings.NewReader(`{"session_name":"` + session + `","thread_ts":"1700000000.000001","channel_id":"C1"}`)
		w := httptest.NewRecorder()
		a.HandleSubscribe(w, httptest.NewRequest(http.MethodPost, "/subscribe", body))
		if w.Code != http.StatusOK {
			t.Fatalf("subscribe %s: %d %s", session, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	a.HandleSubscribe(w, httptest.NewRequest(http.MethodDelete, "/subscribe?session_name=one&thread_ts=1700000000.000001", nil))
	if w.Code != http.StatusNoContent || len(a.broker.List()) != 1 {
		t.Fatalf("delete: %d, subscriptions=%+v", w.Code, a.broker.List())
	}
	if _, ok := a.broker.Find("two", "1700000000.000001"); !ok {
		t.Fatal("other subscriber was removed")
	}
}

func TestParseThreadResource(t *testing.T) {
	cases := []struct {
		resource, channel, thread string
		valid                     bool
	}{
		{"https://acme.slack.com/archives/C123/p1700000000000001", "C123", "1700000000.000001", true},
		{"https://acme.slack.com/archives/C123/p1700000000000001?thread_ts=1700000000.000001&cid=C123", "C123", "1700000000.000001", true},
		{"https://acme.slack.com/archives/C123/p1700000001000001?thread_ts=1700000000.000001&cid=C123", "C123", "1700000000.000001", true},
		{"https://acme.slack.com/archives/C123/p1700000000000001?thread_ts=1700000000.000001&cid=C999", "", "", false},
	}
	for _, tc := range cases {
		channel, thread, err := ParseThreadResource(tc.resource)
		if (err == nil) != tc.valid || channel != tc.channel || thread != tc.thread {
			t.Errorf("ParseThreadResource(%q) = %q %q %v", tc.resource, channel, thread, err)
		}
	}
}

func TestBrokerDoesNotOverwriteOldLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	old := `{"subscribers":[{"session_name":"one","thread_ts":"1700000000.000001","channel_id":"C1","socket_path":"/gone"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(path, nil)
	if got := b.List(); len(got) != 0 {
		t.Fatalf("old ledger loaded: %+v", got)
	}
	b.Subscribe(Subscriber{SessionName: "one", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != old {
		t.Fatalf("old ledger was overwritten: %s", raw)
	}
}

func TestAdapterRejectsOldLedgerAtStartup(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	path := SubscribersStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"subscribers":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(&Config{}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported subscriber state version") {
		t.Fatalf("startup error = %v", err)
	}
}

func TestSweepSkipsRemovalWhenSessionListFails(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.broker.Subscribe(Subscriber{SessionName: "one", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	a.SweepSubscriptions(context.Background(), func(context.Context) (map[string]bool, error) { return nil, errors.New("unavailable") })
	if len(a.broker.List()) != 1 {
		t.Fatal("sweep dropped subscriber after list failure")
	}
}

func TestThreadSubscriptionCLIRequestUsesResourceIdentity(t *testing.T) {
	var got subscribeRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			json.NewDecoder(req.Body).Decode(&got)
			return
		}
		if req.URL.Query().Get("session_name") != "one" || req.URL.Query().Get("thread_ts") != "1700000000.000001" {
			t.Errorf("delete URL = %s", req.URL)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	resource := "https://acme.slack.com/archives/C1/p1700000000000001"
	if err := ThreadSubscription(context.Background(), server.Client(), server.URL, "subscribe", "one", resource, "1700000002.000001"); err != nil {
		t.Fatal(err)
	}
	if got.SessionName != "one" || got.ChannelID != "C1" || got.ThreadTS != "1700000000.000001" || got.CatchUpThrough != "1700000002.000001" {
		t.Fatalf("request = %+v", got)
	}
	if err := ThreadSubscription(context.Background(), server.Client(), server.URL, "unsubscribe", "one", resource, ""); err != nil {
		t.Fatal(err)
	}
}

type selectivePublisher struct{ events []publishedEvent }

func (p *selectivePublisher) PublishSessionEvent(session string, ev publishedEvent) error {
	if session == "one" {
		return errors.New("event log unavailable")
	}
	ev.SessionName = session
	p.events = append(p.events, ev)
	return nil
}

func TestMentionPublishFailureKeepsOnlyFailedSessionsWatermark(t *testing.T) {
	a := newTestAdapter(&Config{})
	publisher := &selectivePublisher{}
	a.eventPublisher = publisher
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U0", "root")}}
	for _, session := range []string{"one", "two"} {
		a.broker.Subscribe(Subscriber{SessionName: session, ChannelID: "C1", ThreadTS: "1700000000.000001"})
	}
	a.handleAppMention(&slackevents.AppMentionEvent{User: "U1", Text: "<@U-bot> act", TimeStamp: "1700000001.000001", ThreadTimeStamp: "1700000000.000001", Channel: "C1"})
	one, _ := a.broker.Find("one", "1700000000.000001")
	two, _ := a.broker.Find("two", "1700000000.000001")
	if one.DeliveredThrough != "" || two.DeliveredThrough != "1700000001.000001" || len(publisher.events) != 1 || publisher.events[0].SessionName != "two" {
		t.Fatalf("one=%q two=%q events=%+v", one.DeliveredThrough, two.DeliveredThrough, publisher.events)
	}
}

func TestUnsubscribeExactPairAndAcceptReleasedSocketField(t *testing.T) {
	a := newTestAdapter(&Config{})
	for _, thread := range []string{"1700000000.000001", "1700000001.000001"} {
		a.broker.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: thread})
	}
	w := httptest.NewRecorder()
	a.HandleSubscribe(w, httptest.NewRequest(http.MethodDelete, "/subscribe?session_name=one&thread_ts=1700000000.000001", nil))
	if w.Code != http.StatusNoContent || len(a.broker.List()) != 1 {
		t.Fatalf("delete: %d, subscriptions=%+v", w.Code, a.broker.List())
	}
	w = httptest.NewRecorder()
	body := strings.NewReader(`{"session_name":"one","thread_ts":"1700000000.000001","channel_id":"C1","socket_path":"/old"}`)
	a.HandleSubscribe(w, httptest.NewRequest(http.MethodPost, "/subscribe", body))
	if w.Code != http.StatusOK || len(a.broker.List()) != 2 {
		t.Fatalf("released request rejected: %d, subscriptions=%+v", w.Code, a.broker.List())
	}
	raw, _ := json.Marshal(a.broker.List())
	if strings.Contains(string(raw), "socket_path") {
		t.Fatalf("socket path persisted: %s", raw)
	}
}

func TestCatchUpAdvancesOnlyRequestedSession(t *testing.T) {
	a := newTestAdapter(&Config{})
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{
		slackMessage("1700000000.000001", "U0", "root"),
		slackMessage("1700000001.000001", "U1", "reply"),
	}}
	a.broker.Subscribe(Subscriber{SessionName: "two", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	body := strings.NewReader(`{"session_name":"one","channel_id":"C1","thread_ts":"1700000000.000001","catch_up_through":"1700000001.000001"}`)
	w := httptest.NewRecorder()
	a.HandleSubscribe(w, httptest.NewRequest(http.MethodPost, "/subscribe", body))
	if w.Code != http.StatusOK {
		t.Fatalf("subscribe: %d %s", w.Code, w.Body.String())
	}
	one, _ := a.broker.Find("one", "1700000000.000001")
	two, _ := a.broker.Find("two", "1700000000.000001")
	if one.DeliveredThrough != "1700000001.000001" || two.DeliveredThrough != "" || len(publisher.events) != 1 || publisher.events[0].SessionName != "one" {
		t.Fatalf("one=%q two=%q events=%+v", one.DeliveredThrough, two.DeliveredThrough, publisher.events)
	}
}

func TestBrokerReloadsIndependentSubscriptions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	first := NewBroker(path, nil)
	for _, session := range []string{"one", "two"} {
		first.Subscribe(Subscriber{SessionName: session, ChannelID: "C1", ThreadTS: "1700000000.000001"})
	}
	first.MarkDelivered("one", "1700000000.000001", "1700000001.000001")
	restored := NewBroker(path, nil)
	if len(restored.List()) != 2 {
		t.Fatalf("restored subscriptions = %+v", restored.List())
	}
	one, _ := restored.Find("one", "1700000000.000001")
	two, _ := restored.Find("two", "1700000000.000001")
	if one.DeliveredThrough != "1700000001.000001" || two.DeliveredThrough != "" {
		t.Fatalf("restored watermarks: one=%q two=%q", one.DeliveredThrough, two.DeliveredThrough)
	}
}

func TestBrokerUnsubscribeBySessionKeepsOtherSessions(t *testing.T) {
	b := NewBroker("", nil)
	for _, session := range []string{"one", "two"} {
		for _, ts := range []string{"1700000000.000001", "1700000001.000001"} {
			b.Subscribe(Subscriber{SessionName: session, ChannelID: "C1", ThreadTS: ts})
		}
	}
	if removed := b.UnsubscribeBySession("one"); len(removed) != 2 {
		t.Fatalf("removed = %+v", removed)
	}
	if len(b.List()) != 2 {
		t.Fatalf("other subscriptions removed: %+v", b.List())
	}
}

func TestThreadSubscriptionRejectsFailedRegistration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	err := ThreadSubscription(context.Background(), server.Client(), server.URL, "subscribe", "one", "https://acme.slack.com/archives/C1/p1700000000000001", "")
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("registration error = %v", err)
	}
}

func TestBrokerDoesNotOverwriteCorruptLedger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	old := `{"version":2,"subscribers":[`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	b := NewBroker(path, nil)
	if b.loadErr == nil {
		t.Fatal("corrupt ledger was accepted")
	}
	b.Subscribe(Subscriber{SessionName: "one", ThreadTS: "1700000000.000001", ChannelID: "C1"})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != old {
		t.Fatalf("corrupt ledger was overwritten: %s", raw)
	}
}

func TestThreadReplyUsesMentionAllowlistDefault(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.api = fakeSlackAPI(t)
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	a.handleMessage(&slackevents.MessageEvent{User: "U1", Text: "reply", Channel: "C1", ThreadTimeStamp: "1700000000.000001"})
	if len(publisher.events) != 1 {
		t.Fatalf("default allowlist dropped reply: %+v", publisher.events)
	}
}

func TestThreadReplyRejectsUserOutsideConfiguredAllowlist(t *testing.T) {
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-allowed"}})
	a.api = fakeSlackAPI(t)
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	a.handleMessage(&slackevents.MessageEvent{User: "U-other", Text: "reply", Channel: "C1", ThreadTimeStamp: "1700000000.000001"})
	if len(publisher.events) != 0 {
		t.Fatalf("disallowed reply published: %+v", publisher.events)
	}
}

func TestThreadReplyWithMentionWaitsForBotIdentity(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.api = fakeSlackAPI(t)
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	a.handleMessage(&slackevents.MessageEvent{User: "U1", Text: "<@U-bot> act", Channel: "C1", ThreadTimeStamp: "1700000000.000001"})
	if len(publisher.events) != 0 {
		t.Fatalf("possible app mention was published as a reply: %+v", publisher.events)
	}
}

func TestThreadReplyMentioningAnotherUserIsPublished(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/auth.test" {
			w.Write([]byte(`{"ok":true,"user_id":"U-bot"}`))
			return
		}
		w.Write([]byte(`{"ok":false,"error":"not_found"}`))
	}))
	defer server.Close()
	a := newTestAdapter(&Config{})
	a.api = slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
	publisher := &recordingEventPublisher{}
	a.eventPublisher = publisher
	a.broker.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	a.handleMessage(&slackevents.MessageEvent{User: "U1", Text: "<@U-other> please review", Channel: "C1", ThreadTimeStamp: "1700000000.000001"})
	if len(publisher.events) != 1 || publisher.events[0].Type != "slack.message" {
		t.Fatalf("events = %+v", publisher.events)
	}
}

func TestLiveSessionsReadsJSONDespiteStderrWarning(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "plect")
	content := "#!/bin/sh\nprintf '%s\\n' '[{\"session_name\":\"down\"}]'\nprintf '%s\\n' 'diagnostic' >&2\n"
	if err := os.WriteFile(script, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	live, err := LiveSessions(context.Background())
	if err != nil || !live["down"] {
		t.Fatalf("live=%v err=%v", live, err)
	}
}

func TestBySessionRejectsAmbiguousThread(t *testing.T) {
	b := NewBroker("", nil)
	b.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	b.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000001.000001"})
	if _, ok := b.BySession("one"); ok {
		t.Fatal("session-only lookup chose an arbitrary thread")
	}
}

func TestBrokerConcurrentUpdatesKeepLedgerReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subscribers.json")
	b := NewBroker(path, nil)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			session := fmt.Sprintf("session-%d", i)
			b.Subscribe(Subscriber{SessionName: session, ChannelID: "C1", ThreadTS: "1700000000.000001"})
			b.MarkDelivered(session, "1700000000.000001", "1700000001.000001")
		}(i)
	}
	group.Wait()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Version     int          `json:"version"`
		Subscribers []Subscriber `json:"subscribers"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Version != 2 || len(persisted.Subscribers) != 20 {
		t.Fatalf("ledger = %+v", persisted)
	}
}

func TestPublishedReplySetsStreamRecipient(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.api = fakeSlackAPI(t)
	a.teamID = "T-home"
	a.eventPublisher = &recordingEventPublisher{}
	a.broker.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	a.handleMessage(&slackevents.MessageEvent{User: "U1", UserTeam: "T-guest", Text: "reply", Channel: "C1", ThreadTimeStamp: "1700000000.000001"})
	if err := a.streamManager.Deliver("C1", "1700000000.000001", "msg", "", 0, "answer", true); err != nil {
		t.Fatal(err)
	}
	calls := a.streamManager.streamer.(*recordingStreamer).startCalls
	if len(calls) != 1 || calls[0].recipientUserID != "U1" || calls[0].teamID != "T-guest" {
		t.Fatalf("stream calls = %+v", calls)
	}
}

func TestFailedReplyPublicationDoesNotSetStreamRecipient(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.api = fakeSlackAPI(t)
	a.teamID = "T-home"
	a.eventPublisher = &selectivePublisher{}
	a.broker.Subscribe(Subscriber{SessionName: "one", ChannelID: "C1", ThreadTS: "1700000000.000001"})
	a.handleMessage(&slackevents.MessageEvent{User: "U1", Text: "reply", Channel: "C1", ThreadTimeStamp: "1700000000.000001"})
	if err := a.streamManager.Deliver("C1", "1700000000.000001", "msg", "", 0, "answer", true); err != nil {
		t.Fatal(err)
	}
	if calls := a.streamManager.streamer.(*recordingStreamer).startCalls; len(calls) != 0 {
		t.Fatalf("failed delivery set recipient: %+v", calls)
	}
}

func TestReleasedThreadOnlyDeleteCannotRemoveMultipleSubscribers(t *testing.T) {
	a := newTestAdapter(&Config{})
	for _, session := range []string{"one", "two"} {
		a.broker.Subscribe(Subscriber{SessionName: session, ChannelID: "C1", ThreadTS: "1700000000.000001"})
	}
	w := httptest.NewRecorder()
	a.HandleSubscribe(w, httptest.NewRequest(http.MethodDelete, "/subscribe?thread_ts=1700000000.000001", nil))
	if w.Code != http.StatusConflict || len(a.broker.List()) != 2 {
		t.Fatalf("legacy delete: status=%d, subscriptions=%+v", w.Code, a.broker.List())
	}
}
