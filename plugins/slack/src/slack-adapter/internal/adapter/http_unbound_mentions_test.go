package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleUnboundMentionsRejectsNonGet(t *testing.T) {
	a := newTestAdapter(&Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/unbound-mentions", nil)
	a.HandleUnboundMentions(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleUnboundMentionsStreamsPublishedItems(t *testing.T) {
	a := newTestAdapter(&Config{})
	srv := httptest.NewServer(http.HandlerFunc(a.HandleUnboundMentions))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /unbound-mentions: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	waitForSubscriberCount(t, a.mentions, 1)

	want := unboundMentionItem{Resource: "https://example.slack.com/archives/C1/p1", ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.2"}
	a.mentions.publish(want)

	var got unboundMentionItem
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode item: %v", err)
	}
	if got != want {
		t.Errorf("item = %+v, want %+v", got, want)
	}
}

func TestHandleUnboundMentionsUnregistersOnClientDisconnect(t *testing.T) {
	a := newTestAdapter(&Config{})
	srv := httptest.NewServer(http.HandlerFunc(a.HandleUnboundMentions))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /unbound-mentions: %v", err)
	}
	// Check registration before disconnecting: closing the body or
	// cancelling ctx below races the handler's own unregister against this
	// goroutine, so a count==1 check placed after either would be racy
	// rather than meaningful.
	waitForSubscriberCount(t, a.mentions, 1)

	resp.Body.Close()
	cancel()
	waitForSubscriberCount(t, a.mentions, 0)
}

func waitForSubscriberCount(t *testing.T, s *mentionStream, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		has := s.hasSubscribers()
		if want == 0 && !has {
			return
		}
		if want > 0 && has {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscriber count never reached the expected state (want subscribers=%v)", want > 0)
		}
		time.Sleep(time.Millisecond)
	}
}
