package adapter

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

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

func TestHandleMessage_SetsThreadStatusOnSuccessfulDelivery(t *testing.T) {
	socketPath, _ := startCapturingListener(t)
	a := newTestAdapter(&Config{AllowedUserIDs: []string{"U-dana"}, StatusLoadingMessages: []string{"is thinking…"}})
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

	// Set clears before it sets (see StatusManager.Set), so a successful
	// delivery produces a clear call followed by the real show call.
	if len(poster.statusCalls) != 2 {
		t.Fatalf("SetThreadStatus calls = %d, want 2 (clear, then show)", len(poster.statusCalls))
	}
	if got := poster.statusCalls[0]; got.Status != "" {
		t.Errorf("first call = %+v, want an empty-status clear", got)
	}
	got := poster.statusCalls[1]
	if got.ChannelID != "C123" || got.ThreadTS != "1111.000" || got.Status == "" {
		t.Errorf("show call = %+v, want C123/1111.000/non-empty status", got)
	}
	if len(got.LoadingMessages) != 1 || got.LoadingMessages[0] != "is thinking…" {
		t.Errorf("loading_messages = %v, want [is thinking…]", got.LoadingMessages)
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
