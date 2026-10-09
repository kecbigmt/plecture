package adapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestAdapterSetThreadStatus_CallsSlackAssistantThreadsSetStatus(t *testing.T) {
	var gotPath string
	var gotValues url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		if err := req.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotValues = req.PostForm
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	a := &Adapter{api: slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))}
	if err := a.SetThreadStatus("C1", "111.0", "is thinking…", []string{"Checking…", "Reviewing…"}); err != nil {
		t.Fatalf("SetThreadStatus() error = %v", err)
	}

	if gotPath != "/assistant.threads.setStatus" {
		t.Errorf("path = %q, want /assistant.threads.setStatus", gotPath)
	}
	if got := gotValues.Get("channel_id"); got != "C1" {
		t.Errorf("channel_id = %q, want C1", got)
	}
	if got := gotValues.Get("thread_ts"); got != "111.0" {
		t.Errorf("thread_ts = %q, want 111.0", got)
	}
	if got := gotValues.Get("status"); got != "is thinking…" {
		t.Errorf("status = %q, want is thinking…", got)
	}
	if got := gotValues.Get("loading_messages"); got != "Checking…,Reviewing…" {
		t.Errorf("loading_messages = %q, want Checking…,Reviewing…", got)
	}
}

// An empty status is how SetAssistantThreadsStatus clears an existing
// status — this locks down that the "status" form field is always sent,
// even when empty, so Slack doesn't just ignore an absent field.
func TestAdapterSetThreadStatus_EmptyStatusStillSendsField(t *testing.T) {
	var sawStatusField bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		_, sawStatusField = req.PostForm["status"]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	a := &Adapter{api: slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))}
	if err := a.SetThreadStatus("C1", "111.0", "", nil); err != nil {
		t.Fatalf("SetThreadStatus() error = %v", err)
	}
	if !sawStatusField {
		t.Error("status form field should be present (even empty) to clear the thread's status")
	}
}

func streamStartsAfter(t *testing.T, a *Adapter, channelID, threadTS string) []startCall {
	t.Helper()
	if err := a.streamManager.Deliver(channelID, threadTS, "msg-1", 0, "reply", true); err != nil {
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

func TestHandleMessage_LaterSpeakerReceivesNextStreamAndThreadsStaySeparate(t *testing.T) {
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

	if err := a.streamManager.Deliver("C123", "2222.000", "msg-1", 0, "reply", true); err != nil {
		t.Fatalf("thread 2222.000: %v", err)
	}
	starts := streamStartsAfter(t, a, "C123", "1111.000")
	if len(starts) != 2 {
		t.Fatalf("StartStream calls = %+v, want 2", starts)
	}
	if starts[0].threadTS != "2222.000" || starts[0].recipientUserID != "U-eli" {
		t.Errorf("thread 2222.000 start = %+v, want U-eli", starts[0])
	}
	if starts[1].threadTS != "1111.000" || starts[1].recipientUserID != "U-eli" {
		t.Errorf("thread 1111.000 start = %+v, want U-eli, who spoke last there", starts[1])
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

func TestHandleAppMention_UnboundMentionerIsRecipientOfTheSessionItStarts(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T-home"

	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-eli", Text: "<@U-bot> go", TimeStamp: "1000.000001", Channel: "C-review"})

	starts := streamStartsAfter(t, a, "C-review", "1000.000001")
	if len(starts) != 1 || starts[0].recipientUserID != "U-eli" {
		t.Fatalf("StartStream calls = %+v, want one for U-eli: the mention is the turn's trigger", starts)
	}
}

func TestHandleAppMention_BotMentionIsNeverStreamRecipient(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T-home"

	a.handleAppMention(&slackevents.AppMentionEvent{User: "U-bot2", BotID: "B1", TimeStamp: "1000.000001", Channel: "C-review"})

	if starts := streamStartsAfter(t, a, "C-review", "1000.000001"); len(starts) != 0 {
		t.Fatalf("StartStream calls = %+v, want none", starts)
	}
}
