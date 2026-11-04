package adapter

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

// recordingPoster captures PostToThread and SetThreadStatus calls so tests
// can verify the framing the broker produces for /notify (emoji prefix +
// summary) and the shimmer status set/cleared on a thread.
type recordingPoster struct {
	calls       []recordedPost
	statusCalls []recordedStatus
	statusErr   error
	postErr     error
}

type recordedPost struct {
	ChannelID, ThreadTS, Text string
}

type recordedStatus struct {
	ChannelID, ThreadTS, Status string
}

func (p *recordingPoster) PostToThread(channelID, threadTS, text string) (string, error) {
	p.calls = append(p.calls, recordedPost{channelID, threadTS, text})
	if p.postErr != nil {
		return "", p.postErr
	}
	return "ts-" + threadTS, nil
}

func (p *recordingPoster) SetThreadStatus(channelID, threadTS, status string) error {
	p.statusCalls = append(p.statusCalls, recordedStatus{channelID, threadTS, status})
	return p.statusErr
}

type recordingThreader struct {
	calls []recordedThread
}

type recordedThread struct {
	ChannelID, Text string
}

func (t *recordingThreader) CreateThread(channelID, text string) (string, string, error) {
	t.calls = append(t.calls, recordedThread{ChannelID: channelID, Text: text})
	return "1234.5678", "https://example.slack.com/archives/C123/p12345678", nil
}

func newTestAdapter(cfg *Config) *Adapter {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := &Adapter{
		cfg:    cfg,
		broker: NewBroker("", logger),
		poster: &recordingPoster{},
		logger: logger,
	}
	a.statusManager = NewStatusManager(a.poster, cfg.StatusTTLDuration(), logger)
	a.streamManager = NewStreamManager(&recordingStreamer{}, a.poster, logger)
	a.mentions = newMentionStream()
	return a
}

func TestCreateThreadFetchesSlackPermalink(t *testing.T) {
	var sawPost, sawPermalink bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/chat.postMessage":
			sawPost = true
			if err := req.ParseForm(); err != nil {
				t.Errorf("ParseForm: %v", err)
			}
			if got := req.PostForm.Get("channel"); got != "C-review" {
				t.Errorf("postMessage channel = %q, want C-review", got)
			}
			if got := req.PostForm.Get("text"); got != "root text" {
				t.Errorf("postMessage text = %q, want root text", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true,"channel":"C-review","ts":"1234.5678"}`))
		case "/chat.getPermalink":
			sawPermalink = true
			if got := req.URL.Query().Get("channel"); got != "C-review" {
				t.Errorf("getPermalink channel = %q, want C-review", got)
			}
			if got := req.URL.Query().Get("message_ts"); got != "1234.5678" {
				t.Errorf("getPermalink message_ts = %q, want 1234.5678", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"ok":true,"channel":"C-review","permalink":"https://example.slack.com/archives/C-review/p12345678"}`))
		default:
			t.Errorf("unexpected Slack API path %s", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	a := &Adapter{api: slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/"))}
	threadTS, permalink, err := a.CreateThread("C-review", "root text")
	if err != nil {
		t.Fatalf("CreateThread() error = %v", err)
	}
	if threadTS != "1234.5678" {
		t.Errorf("threadTS = %q, want 1234.5678", threadTS)
	}
	if permalink != "https://example.slack.com/archives/C-review/p12345678" {
		t.Errorf("permalink = %q, want Slack permalink", permalink)
	}
	if !sawPost || !sawPermalink {
		t.Fatalf("Slack API calls: post=%v permalink=%v, want both", sawPost, sawPermalink)
	}
}

func TestHandleCreateThread_UsesRequestChannelAndReturnsPermalink(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C-default"})
	threader := &recordingThreader{}
	a.threader = threader

	body, _ := json.Marshal(createThreadRequest{
		ChannelID: "C-review",
		Text:      "[AI review] Fix widgets — https://github.com/acme/widgets/pull/7",
	})
	req := httptest.NewRequest(http.MethodPost, "/threads", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleCreateThread(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(threader.calls) != 1 {
		t.Fatalf("CreateThread calls = %d, want 1", len(threader.calls))
	}
	if got := threader.calls[0].ChannelID; got != "C-review" {
		t.Errorf("CreateThread channel_id = %q, want C-review", got)
	}

	var resp createThreadResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ThreadTS != "1234.5678" {
		t.Errorf("thread_ts = %q, want 1234.5678", resp.ThreadTS)
	}
	if resp.ChannelID != "C-review" {
		t.Errorf("channel_id = %q, want C-review", resp.ChannelID)
	}
	if resp.Permalink != "https://example.slack.com/archives/C123/p12345678" {
		t.Errorf("permalink = %q, want Slack permalink", resp.Permalink)
	}
}

func TestHandleCreateThread_FallsBackToConfiguredChannel(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C-default"})
	threader := &recordingThreader{}
	a.threader = threader

	body, _ := json.Marshal(createThreadRequest{Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/threads", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleCreateThread(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := threader.calls[0].ChannelID; got != "C-default" {
		t.Errorf("CreateThread channel_id = %q, want C-default", got)
	}
}

func TestHandleCreateThread_RejectsMissingChannelIDWithoutDefault(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.threader = &recordingThreader{}

	body, _ := json.Marshal(createThreadRequest{Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/threads", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleCreateThread(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleInfo(t *testing.T) {
	cfg := &Config{ChannelID: "C12345"}
	a := &Adapter{cfg: cfg, workspace: "exampleorg"}

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	a.HandleInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var resp infoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Workspace != "exampleorg" {
		t.Errorf("got workspace %q, want %q", resp.Workspace, "exampleorg")
	}
	if resp.ChannelID != "C12345" {
		t.Errorf("got channel_id %q, want %q", resp.ChannelID, "C12345")
	}
}

func TestHandleInfo_EmptyWorkspace(t *testing.T) {
	cfg := &Config{ChannelID: "C99999"}
	a := &Adapter{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	w := httptest.NewRecorder()
	a.HandleInfo(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var resp infoResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Workspace != "" {
		t.Errorf("got workspace %q, want empty", resp.Workspace)
	}
	if resp.ChannelID != "C99999" {
		t.Errorf("got channel_id %q, want %q", resp.ChannelID, "C99999")
	}
}

func TestHandlePostMessage_MentionLogic(t *testing.T) {
	tests := []struct {
		name          string
		notifyUserIDs []string
		mention       bool
		inputText     string
		wantText      string
	}{
		{
			name:          "mention false, text unchanged",
			notifyUserIDs: []string{"U111"},
			mention:       false,
			inputText:     "hello",
			wantText:      "hello",
		},
		{
			name:          "mention true with configured users",
			notifyUserIDs: []string{"U111"},
			mention:       true,
			inputText:     "hello",
			wantText:      "<@U111> hello",
		},
		{
			name:          "mention true but no users configured",
			notifyUserIDs: nil,
			mention:       true,
			inputText:     "hello",
			wantText:      "hello",
		},
		{
			name:          "mention true with multiple users",
			notifyUserIDs: []string{"U111", "U222"},
			mention:       true,
			inputText:     "done",
			wantText:      "<@U111> <@U222> done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{NotifyUserIDs: tt.notifyUserIDs}
			text := tt.inputText
			if tt.mention {
				text = cfg.MentionPrefix() + text
			}
			if text != tt.wantText {
				t.Errorf("got %q, want %q", text, tt.wantText)
			}
		})
	}
}

func TestHandlePostMessage_MethodNotAllowed(t *testing.T) {
	cfg := &Config{ChannelID: "C12345"}
	a := &Adapter{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/messages", nil)
	w := httptest.NewRecorder()
	a.HandlePostMessage(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("got status %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandlePostMessage_InvalidJSON(t *testing.T) {
	cfg := &Config{ChannelID: "C12345"}
	a := &Adapter{cfg: cfg}

	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	a.HandlePostMessage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePostMessage_MissingFields(t *testing.T) {
	cfg := &Config{ChannelID: "C12345"}
	a := &Adapter{cfg: cfg}

	body, _ := json.Marshal(postMessageRequest{Text: ""})
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandlePostMessage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandlePostMessage_RejectsMissingChannelIDWithoutDefault(t *testing.T) {
	a := newTestAdapter(&Config{})

	body, _ := json.Marshal(postMessageRequest{ThreadTS: "1111.000", Text: "done"})
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandlePostMessage(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSetStatus_NonEmptyStatusSetsWithoutPosting(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})
	poster := a.poster.(*recordingPoster)

	body, _ := json.Marshal(setStatusRequest{
		ChannelID: "C123",
		ThreadTS:  "1111.000",
		Status:    "is reviewing…",
		TurnID:    "turn-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/status", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(poster.calls) != 0 {
		t.Errorf("PostToThread calls = %d, want 0 (status update must not post a message)", len(poster.calls))
	}
	if len(poster.statusCalls) != 1 {
		t.Fatalf("SetThreadStatus calls = %d, want 1", len(poster.statusCalls))
	}
	got := poster.statusCalls[0]
	if got.ChannelID != "C123" || got.ThreadTS != "1111.000" || got.Status != "processing" {
		t.Errorf("show call = %+v, want C123/1111.000/processing", got)
	}
}

func TestHandleSetStatus_EmptyStatusClears(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})
	poster := a.poster.(*recordingPoster)

	if err := a.statusManager.Begin("C123", "1111.000", "turn-1"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(setStatusRequest{ChannelID: "C123", ThreadTS: "1111.000", Status: "", TurnID: "turn-1"})
	req := httptest.NewRequest(http.MethodPost, "/status", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(poster.statusCalls) != 2 {
		t.Fatalf("SetThreadStatus calls = %d, want 2", len(poster.statusCalls))
	}
	if got := poster.statusCalls[1].Status; got != "active" {
		t.Errorf("status = %q, want active", got)
	}
}

func TestHandleSetStatus_FallsBackToConfiguredChannel(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C-default"})
	poster := a.poster.(*recordingPoster)

	body, _ := json.Marshal(setStatusRequest{ThreadTS: "1111.000", Status: "is thinking…"})
	req := httptest.NewRequest(http.MethodPost, "/status", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if poster.statusCalls[0].ChannelID != "C-default" {
		t.Errorf("channel_id = %q, want C-default", poster.statusCalls[0].ChannelID)
	}
}

func TestHandleSetStatus_SlackAPIErrorMapsTo422(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})
	poster := a.poster.(*recordingPoster)
	poster.statusErr = slack.SlackErrorResponse{Err: "invalid_arguments"}

	body, _ := json.Marshal(setStatusRequest{ThreadTS: "1111.000", Status: "is thinking…"})
	req := httptest.NewRequest(http.MethodPost, "/status", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != 422 {
		t.Fatalf("got status %d, want 422, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_arguments") {
		t.Errorf("body = %q, want it to contain the slack error name", w.Body.String())
	}
}

func TestHandleSetStatus_RejectsMissingChannelIDWithoutDefault(t *testing.T) {
	a := newTestAdapter(&Config{})

	body, _ := json.Marshal(setStatusRequest{ThreadTS: "1111.000", Status: "is thinking…"})
	req := httptest.NewRequest(http.MethodPost, "/status", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSetStatus_RejectsMissingThreadTS(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	body, _ := json.Marshal(setStatusRequest{Status: "is thinking…"})
	req := httptest.NewRequest(http.MethodPost, "/status", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSetStatus_InvalidJSON(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	req := httptest.NewRequest(http.MethodPost, "/status", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSetStatus_MethodNotAllowed(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	a.HandleSetStatus(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleStream_DeliversChunkToStreamManager(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})
	streamer := a.streamManager.streamer.(*recordingStreamer)
	a.streamManager.RecordRecipient("C123", "1111.000", "U1", "T1")

	body, _ := json.Marshal(streamRequest{
		ChannelID: "C123",
		ThreadTS:  "1111.000",
		StreamKey: "msg-1",
		Text:      "Hello",
		Index:     "0",
		Final:     "false",
	})
	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(streamer.startCalls) != 1 {
		t.Fatalf("StartStream calls = %d, want 1", len(streamer.startCalls))
	}
	if got := streamer.startCalls[0]; got.channelID != "C123" || got.threadTS != "1111.000" || got.text != "Hello" {
		t.Errorf("StartStream call = %+v, want C123/1111.000/Hello", got)
	}
}

func TestHandleStream_TurnIDGroupsStreamsUnderOneRecipient(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})
	streamer := a.streamManager.streamer.(*recordingStreamer)
	a.streamManager.RecordRecipient("C123", "1111.000", "U-alice", "T1")
	a.streamManager.RecordRecipient("C123", "1111.000", "U-bob", "T1")

	for _, key := range []string{"msg-1", "msg-2"} {
		body, _ := json.Marshal(streamRequest{ChannelID: "C123", ThreadTS: "1111.000", StreamKey: key, TurnID: "turn-1", Text: "x", Index: "0", Final: "true"})
		w := httptest.NewRecorder()
		a.HandleStream(w, httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: got status %d, body=%s", key, w.Code, w.Body.String())
		}
	}

	if len(streamer.startCalls) != 2 || streamer.startCalls[0].recipientUserID != "U-alice" || streamer.startCalls[1].recipientUserID != "U-alice" {
		t.Errorf("StartStream calls = %+v, want both addressed to U-alice", streamer.startCalls)
	}
}

func TestHandleStream_FallsBackToConfiguredChannel(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C-default"})
	streamer := a.streamManager.streamer.(*recordingStreamer)
	a.streamManager.RecordRecipient("C-default", "1111.000", "U1", "T1")

	body, _ := json.Marshal(streamRequest{ThreadTS: "1111.000", StreamKey: "msg-1", Text: "Hello", Index: "0", Final: "true"})
	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(streamer.startCalls) != 1 || streamer.startCalls[0].channelID != "C-default" {
		t.Errorf("StartStream calls = %+v, want one call on C-default", streamer.startCalls)
	}
}

func TestHandleStream_EmptyFinalDefaultsToFalse(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})
	streamer := a.streamManager.streamer.(*recordingStreamer)

	body, _ := json.Marshal(streamRequest{ThreadTS: "1111.000", StreamKey: "msg-1", Text: "Hello", Index: "0"})
	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if len(streamer.stopCalls) != 0 {
		t.Errorf("StopStream calls = %d, want 0 (an omitted final must not finalize)", len(streamer.stopCalls))
	}
}

func TestHandleStream_RejectsMissingStreamKey(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	body, _ := json.Marshal(streamRequest{ThreadTS: "1111.000", Text: "Hello", Index: "0"})
	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleStream_RejectsMissingChannelIDWithoutDefault(t *testing.T) {
	a := newTestAdapter(&Config{})

	body, _ := json.Marshal(streamRequest{ThreadTS: "1111.000", StreamKey: "msg-1", Text: "Hello", Index: "0"})
	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleStream_RejectsNonIntegerIndex(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	body, _ := json.Marshal(streamRequest{ThreadTS: "1111.000", StreamKey: "msg-1", Text: "Hello", Index: "not-a-number"})
	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleStream_RejectsNonBooleanFinal(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	body, _ := json.Marshal(streamRequest{ThreadTS: "1111.000", StreamKey: "msg-1", Text: "Hello", Index: "0", Final: "not-a-bool"})
	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleStream_InvalidJSON(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	req := httptest.NewRequest(http.MethodPost, "/stream", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleStream_MethodNotAllowed(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()
	a.HandleStream(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleSubscribers_MethodNotAllowed(t *testing.T) {
	a := newTestAdapter(&Config{ChannelID: "C0"})
	req := httptest.NewRequest(http.MethodPost, "/subscribers", nil)
	w := httptest.NewRecorder()
	a.HandleSubscribers(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
