package adapter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// fakeSlackAPI returns a slack.Client pointed at a local server that answers
// every method with a generic failure. handleMessage's GetUserInfo call
// needs a non-nil, network-free api client; the display-name lookup falling
// back to the raw user id on error doesn't affect what these tests assert.
func fakeSlackAPI(t *testing.T) *slack.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":false,"error":"user_not_found"}`))
	}))
	t.Cleanup(server.Close)
	return slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))
}

func TestHandleMessage_SuccessfulDeliveryDoesNotSetThreadStatus(t *testing.T) {
	socketPath, msgs := startCapturingListener(t)
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-dana"}})
	a.api = fakeSlackAPI(t)
	poster := a.poster.(*recordingPoster)
	a.broker.Subscribe(Subscriber{
		ThreadTS:    "1111.000",
		ChannelID:   "C123",
		SocketPath:  socketPath,
		SessionName: "owner/repo-1",
	})

	a.handleMessage(&slackevents.MessageEvent{
		User:            "U-dana",
		Text:            "please continue",
		ThreadTimeStamp: "1111.000",
		Channel:         "C123",
	})

	select {
	case <-msgs:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the message to be delivered")
	}
	if len(poster.statusCalls) != 0 {
		t.Errorf("SetThreadStatus calls = %d, want 0 (no receipt-time shimmer)", len(poster.statusCalls))
	}
}

func TestHandleMessage_DeliveryFailureDoesNotSetThreadStatus(t *testing.T) {
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-dana"}})
	a.api = fakeSlackAPI(t)
	poster := a.poster.(*recordingPoster)
	a.broker.Subscribe(Subscriber{
		ThreadTS:    "1111.000",
		ChannelID:   "C123",
		SocketPath:  filepath.Join(t.TempDir(), "vanished.sock"),
		SessionName: "owner/repo-1",
	})

	a.handleMessage(&slackevents.MessageEvent{
		User:            "U-dana",
		Text:            "please continue",
		ThreadTimeStamp: "1111.000",
		Channel:         "C123",
	})

	if len(poster.statusCalls) != 0 {
		t.Errorf("SetThreadStatus calls = %d, want 0 (delivery failed)", len(poster.statusCalls))
	}
	if len(poster.calls) != 1 {
		t.Fatalf("PostToThread calls = %d, want 1 (the warning post)", len(poster.calls))
	}
	if !strings.Contains(poster.calls[0].Text, "Failed to deliver") {
		t.Errorf("warning post text = %q, want it to mention delivery failure", poster.calls[0].Text)
	}
}

func streamStartsAfter(t *testing.T, a *Adapter, channelID, threadTS string) []startCall {
	t.Helper()
	if err := a.streamManager.Deliver(channelID, threadTS, "msg-1", "", 0, "reply", true); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	return a.streamManager.streamer.(*recordingStreamer).startCalls
}

func streamStartsAfter2(t *testing.T, a *Adapter, channelID, threadTS string) []startCall {
	t.Helper()
	if err := a.streamManager.Deliver(channelID, threadTS, "msg-2", "", 0, "reply", true); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	return a.streamManager.streamer.(*recordingStreamer).startCalls
}

func TestHandleMessage_TriggeringUserBecomesStreamRecipient(t *testing.T) {
	for _, tc := range []struct {
		name    string
		allowed []string
	}{
		{name: "one allowed user", allowed: []string{"U-dana"}},
		{name: "several allowed users", allowed: []string{"U-dana", "U-eli"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socketPath, msgs := startCapturingListener(t)
			a := newTestAdapter(&Config{AllowedUserIDs: tc.allowed})
			a.api = fakeSlackAPI(t)
			a.eventPublisher = &recordingEventPublisher{}
			a.eventPublisher = &recordingEventPublisher{}
			a.teamID = "T-home"
			a.broker.Subscribe(Subscriber{ThreadTS: "1111.000", ChannelID: "C123", SocketPath: socketPath, SessionName: "owner/repo-1"})

			a.handleMessage(&slackevents.MessageEvent{User: "U-dana", Text: "hi", ThreadTimeStamp: "1111.000", Channel: "C123"})
			<-msgs

			starts := streamStartsAfter(t, a, "C123", "1111.000")
			if len(starts) != 1 || starts[0].recipientUserID != "U-dana" || starts[0].teamID != "T-home" {
				t.Fatalf("StartStream calls = %+v, want one for U-dana/T-home", starts)
			}
		})
	}
}

func TestHandleMessage_SharedChannelSenderTeamIsRecipientTeam(t *testing.T) {
	socketPath, msgs := startCapturingListener(t)
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-dana"}})
	a.api = fakeSlackAPI(t)
	a.eventPublisher = &recordingEventPublisher{}
	a.teamID = "T-home"
	a.broker.Subscribe(Subscriber{ThreadTS: "1111.000", ChannelID: "C123", SocketPath: socketPath, SessionName: "owner/repo-1"})

	a.handleMessage(&slackevents.MessageEvent{User: "U-dana", Text: "hi", ThreadTimeStamp: "1111.000", Channel: "C123", UserTeam: "T-partner"})
	<-msgs

	starts := streamStartsAfter(t, a, "C123", "1111.000")
	if len(starts) != 1 || starts[0].teamID != "T-partner" {
		t.Fatalf("StartStream calls = %+v, want the sender's own team T-partner", starts)
	}
}

func TestHandleMessage_EachReplyGoesToItsOwnTriggeringSenderAndThreadsStaySeparate(t *testing.T) {
	socketA, msgsA := startCapturingListener(t)
	socketB, msgsB := startCapturingListener(t)
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-dana", "U-eli"}})
	a.api = fakeSlackAPI(t)
	a.eventPublisher = &recordingEventPublisher{}
	a.teamID = "T-home"
	a.broker.Subscribe(Subscriber{ThreadTS: "1111.000", ChannelID: "C123", SocketPath: socketA, SessionName: "owner/repo-1"})
	a.broker.Subscribe(Subscriber{ThreadTS: "2222.000", ChannelID: "C123", SocketPath: socketB, SessionName: "owner/repo-2"})

	a.handleMessage(&slackevents.MessageEvent{User: "U-dana", Text: "one", ThreadTimeStamp: "1111.000", Channel: "C123"})
	<-msgsA
	a.handleMessage(&slackevents.MessageEvent{User: "U-eli", Text: "two", ThreadTimeStamp: "2222.000", Channel: "C123"})
	<-msgsB
	a.handleMessage(&slackevents.MessageEvent{User: "U-eli", Text: "three", ThreadTimeStamp: "1111.000", Channel: "C123"})
	<-msgsA

	if err := a.streamManager.Deliver("C123", "2222.000", "msg-1", "", 0, "reply", true); err != nil {
		t.Fatalf("thread 2222.000: %v", err)
	}
	if err := a.streamManager.Deliver("C123", "1111.000", "msg-1", "", 0, "reply", true); err != nil {
		t.Fatalf("thread 1111.000, first reply: %v", err)
	}
	starts := streamStartsAfter2(t, a, "C123", "1111.000")
	if len(starts) != 3 {
		t.Fatalf("StartStream calls = %+v, want 3", starts)
	}
	if starts[0].threadTS != "2222.000" || starts[0].recipientUserID != "U-eli" {
		t.Errorf("thread 2222.000 start = %+v, want U-eli", starts[0])
	}
	if starts[1].threadTS != "1111.000" || starts[1].recipientUserID != "U-dana" {
		t.Errorf("thread 1111.000 first reply = %+v, want U-dana, whose message it answers", starts[1])
	}
	if starts[2].threadTS != "1111.000" || starts[2].recipientUserID != "U-eli" {
		t.Errorf("thread 1111.000 second reply = %+v, want U-eli", starts[2])
	}
}

func TestHandleMessage_DisallowedUserIsNeverStreamRecipient(t *testing.T) {
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-dana"}})
	a.api = fakeSlackAPI(t)
	a.eventPublisher = &recordingEventPublisher{}
	a.teamID = "T-home"
	a.broker.Subscribe(Subscriber{ThreadTS: "1111.000", ChannelID: "C123", SocketPath: "/nonexistent", SessionName: "owner/repo-1"})

	a.handleMessage(&slackevents.MessageEvent{User: "U-intruder", Text: "hi", ThreadTimeStamp: "1111.000", Channel: "C123"})

	if starts := streamStartsAfter(t, a, "C123", "1111.000"); len(starts) != 0 {
		t.Fatalf("StartStream calls = %+v, want none for a thread whose only speaker was rejected", starts)
	}
}

func TestHandleAppMention_MentionerBecomesStreamRecipientWithoutAllowlist(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T-home"
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1000.000001", "U-eli", "<@U-bot> go")}}
	a.eventPublisher = &recordingEventPublisher{}
	a.broker.Subscribe(Subscriber{ThreadTS: "1000.000001", ChannelID: "C-review", SessionName: "owner/repo-1"})

	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-eli", Text: "<@U-bot> go", TimeStamp: "1000.000002", ThreadTimeStamp: "1000.000001", Channel: "C-review"})

	starts := streamStartsAfter(t, a, "C-review", "1000.000001")
	if len(starts) != 1 || starts[0].recipientUserID != "U-eli" || starts[0].teamID != "T-home" {
		t.Fatalf("StartStream calls = %+v, want one for U-eli/T-home", starts)
	}
}

func TestHandleAppMention_UnboundMentionerIsRecipientOnlyWhenDispatchSucceeds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hookErr   error
		linkErr   error
		wantStart bool
	}{
		{name: "hook succeeds", wantStart: true},
		{name: "hook fails", hookErr: errors.New("exit status 1")},
		{name: "permalink fails", linkErr: errors.New("no permalink")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter(&Config{OnUnboundMention: "/path/to/dispatch"})
			a.teamID = "T-home"
			a.permalinkResolver = &fakePermalinkResolver{link: "https://example.slack.com/p1", err: tc.linkErr}
			a.mentionHook = &recordingMentionHookRunner{err: tc.hookErr}

			a.handleAppMention(&slackevents.AppMentionEvent{User: "U-eli", Text: "<@U-bot> go", TimeStamp: "1000.000001", Channel: "C-review"})

			starts := streamStartsAfter(t, a, "C-review", "1000.000001")
			if got := len(starts) == 1 && starts[0].recipientUserID == "U-eli"; got != tc.wantStart {
				t.Fatalf("StartStream calls = %+v, want a stream for U-eli: %v", starts, tc.wantStart)
			}
		})
	}
}

func TestHandleAppMention_UnboundMentionWithNoDispatchTargetIsNotARecipient(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T-home"

	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-eli", Text: "<@U-bot> go", TimeStamp: "1000.000001", Channel: "C-review"})

	if starts := streamStartsAfter(t, a, "C-review", "1000.000001"); len(starts) != 0 {
		t.Fatalf("StartStream calls = %+v, want none: no session was started for this mention", starts)
	}
}

func TestHandleAppMention_BoundMentionWhoseEventFailedToPublishIsNotARecipient(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T-home"
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1000.000001", "U-eli", "<@U-bot> go")}}
	a.eventPublisher = failingEventPublisher{}
	a.broker.Subscribe(Subscriber{ThreadTS: "1000.000001", ChannelID: "C-review", SessionName: "owner/repo-1"})

	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-eli", Text: "<@U-bot> go", TimeStamp: "1000.000002", ThreadTimeStamp: "1000.000001", Channel: "C-review"})

	if starts := streamStartsAfter(t, a, "C-review", "1000.000001"); len(starts) != 0 {
		t.Fatalf("StartStream calls = %+v, want none: the session never received this mention", starts)
	}
}

func TestHandleMessage_RejectedDeliveryDoesNotQueueTheSenderForTheNextReply(t *testing.T) {
	socketPath, msgs := startCapturingListener(t)
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-dana", "U-eli"}})
	a.api = fakeSlackAPI(t)
	a.eventPublisher = &recordingEventPublisher{}
	a.teamID = "T-home"
	a.broker.Subscribe(Subscriber{ThreadTS: "1111.000", ChannelID: "C123", SocketPath: filepath.Join(t.TempDir(), "gone.sock"), SessionName: "owner/repo-1"})

	a.handleMessage(&slackevents.MessageEvent{User: "U-dana", Text: "lost", ThreadTimeStamp: "1111.000", Channel: "C123"})
	a.broker.Subscribe(Subscriber{ThreadTS: "1111.000", ChannelID: "C123", SocketPath: socketPath, SessionName: "owner/repo-1"})
	a.handleMessage(&slackevents.MessageEvent{User: "U-eli", Text: "delivered", ThreadTimeStamp: "1111.000", Channel: "C123"})
	<-msgs

	starts := streamStartsAfter(t, a, "C123", "1111.000")
	if len(starts) != 1 || starts[0].recipientUserID != "U-eli" {
		t.Fatalf("StartStream calls = %+v, want U-eli: U-dana's message never reached the session", starts)
	}
}

type failingEventPublisher struct{}

func (failingEventPublisher) PublishSessionEvent(string, publishedEvent) error {
	return errors.New("publish failed")
}

func TestHandleAppMention_BotMentionIsNeverStreamRecipient(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T-home"

	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-bot2", BotID: "B1", TimeStamp: "1000.000001", Channel: "C-review"})

	if starts := streamStartsAfter(t, a, "C-review", "1000.000001"); len(starts) != 0 {
		t.Fatalf("StartStream calls = %+v, want none", starts)
	}
}
