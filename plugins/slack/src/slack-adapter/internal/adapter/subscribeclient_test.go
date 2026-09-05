package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer guards a bytes.Buffer with a mutex: RunSubscribeUnboundMentions
// writes to it from a background goroutine while the test polls its content
// from the main goroutine, which a plain bytes.Buffer does not allow safely.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitForOutput(t *testing.T, out *syncBuffer) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for out.String() == "" {
		select {
		case <-deadline:
			t.Fatal("no item written before deadline")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestRunSubscribeUnboundMentionsEmitsOneItemPerAppearance covers the
// client side of one mention's delivery; TestHandleAppMentionPublishesToStream*
// in unbound_mention_test.go covers the server side of the same appearance.
func TestRunSubscribeUnboundMentionsEmitsOneItemPerAppearance(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		if err := json.NewEncoder(w).Encode(unboundMentionItem{
			Resource: "https://example.slack.com/archives/C1/p1", ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.2",
		}); err != nil {
			t.Errorf("encode: %v", err)
		}
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- RunSubscribeUnboundMentions(ctx, srv.URL, nil, out) }()

	waitForOutput(t, out)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("RunSubscribeUnboundMentions returned %v after a deliberate cancel, want nil", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want exactly 1: %q", len(lines), out.String())
	}
	var item unboundMentionItem
	if err := json.Unmarshal([]byte(lines[0]), &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	want := unboundMentionItem{Resource: "https://example.slack.com/archives/C1/p1", ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.2"}
	if item != want {
		t.Errorf("item = %+v, want %+v", item, want)
	}
}

func TestRunSubscribeUnboundMentionsFiltersToChannelIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		enc := json.NewEncoder(w)
		if err := enc.Encode(unboundMentionItem{ChannelID: "C-other"}); err != nil {
			t.Errorf("encode: %v", err)
		}
		if err := enc.Encode(unboundMentionItem{ChannelID: "C-wanted", ThreadTS: "wanted"}); err != nil {
			t.Errorf("encode: %v", err)
		}
		flusher.Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- RunSubscribeUnboundMentions(ctx, srv.URL, []string{"C-wanted"}, out) }()

	// Wait for the wanted (second, filtered-in) item specifically: the
	// unwanted item alone would never populate out, so a plain
	// waitForOutput could pass without proving the filter did anything.
	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), "C-wanted") {
		select {
		case <-deadline:
			t.Fatal("the allowed channel's item never arrived")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done

	if strings.Contains(out.String(), "C-other") {
		t.Errorf("output includes a channel outside channel_ids: %q", out.String())
	}
}

func TestRunSubscribeUnboundMentionsReturnsErrorWhenStreamDisconnects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Writing nothing and returning closes the body immediately,
		// simulating the resident adapter disappearing mid-stream — the
		// client never cancels ctx itself.
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := RunSubscribeUnboundMentions(context.Background(), srv.URL, nil, &out); err == nil {
		t.Fatal("expected a non-nil error when the stream ends without the caller cancelling ctx")
	}
}

func TestRunSubscribeUnboundMentionsReturnsErrorOnConnectFailure(t *testing.T) {
	var out bytes.Buffer
	// Port 1 is a reserved, never-listening port: the connection is
	// refused immediately, no real adapter required.
	if err := RunSubscribeUnboundMentions(context.Background(), "http://127.0.0.1:1", nil, &out); err == nil {
		t.Fatal("expected a non-nil error when the resident adapter is unreachable")
	}
}

func TestRunSubscribeUnboundMentionsReturnsErrorOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := RunSubscribeUnboundMentions(context.Background(), srv.URL, nil, &out); err == nil {
		t.Fatal("expected a non-nil error for a non-200 response")
	}
}
