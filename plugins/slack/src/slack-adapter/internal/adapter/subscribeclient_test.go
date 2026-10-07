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
	go func() { done <- RunSubscribeUnboundMentions(ctx, srv.URL, MentionFilter{}, out) }()

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
	go func() {
		done <- RunSubscribeUnboundMentions(ctx, srv.URL, MentionFilter{ChannelIDs: []string{"C-wanted"}}, out)
	}()

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
	if err := RunSubscribeUnboundMentions(context.Background(), srv.URL, MentionFilter{}, &out); err == nil {
		t.Fatal("expected a non-nil error when the stream ends without the caller cancelling ctx")
	}
}

func TestRunSubscribeUnboundMentionsReturnsErrorOnConnectFailure(t *testing.T) {
	var out bytes.Buffer
	// Port 1 is a reserved, never-listening port: the connection is
	// refused immediately, no real adapter required.
	if err := RunSubscribeUnboundMentions(context.Background(), "http://127.0.0.1:1", MentionFilter{}, &out); err == nil {
		t.Fatal("expected a non-nil error when the resident adapter is unreachable")
	}
}

func TestRunSubscribeUnboundMentionsReturnsErrorOnNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	var out bytes.Buffer
	if err := RunSubscribeUnboundMentions(context.Background(), srv.URL, MentionFilter{}, &out); err == nil {
		t.Fatal("expected a non-nil error for a non-200 response")
	}
}

func TestMentionFilterJudge(t *testing.T) {
	item := unboundMentionItem{ChannelID: "C1", ThreadTS: "1.1", UserID: "U-dana"}
	tests := []struct {
		name   string
		filter MentionFilter
		item   unboundMentionItem
		want   mentionVerdict
	}{
		{"unset user_ids passes every user", MentionFilter{}, item, verdictEmit},
		{"member passes", MentionFilter{UserIDs: []string{"U-ann", "U-dana"}}, item, verdictEmit},
		{"non-member without deny_message is dropped", MentionFilter{UserIDs: []string{"U-ann"}}, item, verdictDrop},
		{"non-member with deny_message is denied", MentionFilter{UserIDs: []string{"U-ann"}, DenyMessage: "no"}, item, verdictDeny},
		{"member with deny_message still passes", MentionFilter{UserIDs: []string{"U-dana"}, DenyMessage: "no"}, item, verdictEmit},
		{"deny_message alone does not restrict", MentionFilter{DenyMessage: "no"}, item, verdictEmit},
		{"other channel is dropped before the user check", MentionFilter{ChannelIDs: []string{"C2"}, UserIDs: []string{"U-ann"}, DenyMessage: "no"}, item, verdictDrop},
		{"match is exact, not a prefix", MentionFilter{UserIDs: []string{"U-dan"}}, item, verdictDrop},
		{"an item without a user id is not a member", MentionFilter{UserIDs: []string{"U-ann"}}, unboundMentionItem{ChannelID: "C1"}, verdictDrop},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.judge(tt.item); got != tt.want {
				t.Errorf("judge = %v, want %v", got, tt.want)
			}
		})
	}
}

type denyPost struct {
	ChannelID string `json:"channel_id"`
	ThreadTS  string `json:"thread_ts"`
	Text      string `json:"text"`
}

// denyServer streams items, then records every POST /messages body.
func denyServer(t *testing.T, items []unboundMentionItem, posts chan<- denyPost, postStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/messages":
			var m denyPost
			if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
				t.Errorf("decode post: %v", err)
			}
			posts <- m
			w.WriteHeader(postStatus)
		case r.URL.Path == "/unbound-mentions":
			flusher := w.(http.Flusher)
			enc := json.NewEncoder(w)
			for _, it := range items {
				if err := enc.Encode(it); err != nil {
					t.Errorf("encode: %v", err)
				}
			}
			flusher.Flush()
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
}

func runUntilOutputContains(t *testing.T, baseURL string, f MentionFilter, marker string) *syncBuffer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- RunSubscribeUnboundMentions(ctx, baseURL, f, out) }()
	deadline := time.After(2 * time.Second)
	for !strings.Contains(out.String(), marker) {
		select {
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("marker %q never arrived; output = %q", marker, out.String())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	<-done
	return out
}

func TestRunSubscribeUnboundMentionsDeniesNonMembersOncePerThreadAndUser(t *testing.T) {
	items := []unboundMentionItem{
		{ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.1", UserID: "U-eve"},
		{ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.2", UserID: "U-eve"},
		{ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.3", UserID: "U-mallory"},
		{ChannelID: "C1", ThreadTS: "2.1", MentionTS: "2.1", UserID: "U-eve"},
		{ChannelID: "C1", ThreadTS: "3.1", MentionTS: "3.1", UserID: "U-ann"},
	}
	posts := make(chan denyPost, 16)
	srv := denyServer(t, items, posts, http.StatusOK)
	defer srv.Close()

	out := runUntilOutputContains(t, srv.URL, MentionFilter{UserIDs: []string{"U-ann"}, DenyMessage: "not allowed"}, `"3.1"`)

	var got []denyPost
	for len(posts) > 0 {
		got = append(got, <-posts)
	}
	want := []denyPost{
		{"C1", "1.1", "not allowed"},
		{"C1", "1.1", "not allowed"},
		{"C1", "2.1", "not allowed"},
	}
	if len(got) != len(want) {
		t.Fatalf("posted %d replies (%+v), want %d: one per (channel, thread, user)", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reply %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 1 {
		t.Errorf("emitted %d items, want only the member's: %q", len(lines), out.String())
	}
}

func TestRunSubscribeUnboundMentionsDropsNonMembersSilentlyWithoutDenyMessage(t *testing.T) {
	items := []unboundMentionItem{
		{ChannelID: "C1", ThreadTS: "1.1", UserID: "U-eve"},
		{ChannelID: "C1", ThreadTS: "3.1", UserID: "U-ann"},
	}
	posts := make(chan denyPost, 16)
	srv := denyServer(t, items, posts, http.StatusOK)
	defer srv.Close()

	out := runUntilOutputContains(t, srv.URL, MentionFilter{UserIDs: []string{"U-ann"}}, `"3.1"`)

	if len(posts) != 0 {
		t.Errorf("posted %d replies, want none when deny_message is unset", len(posts))
	}
	if strings.Contains(out.String(), "U-eve") {
		t.Errorf("a denied mention was emitted: %q", out.String())
	}
}

func TestRunSubscribeUnboundMentionsRetriesADenyReplyThatFailedToPost(t *testing.T) {
	items := []unboundMentionItem{
		{ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.1", UserID: "U-eve"},
		{ChannelID: "C1", ThreadTS: "1.1", MentionTS: "1.2", UserID: "U-eve"},
		{ChannelID: "C1", ThreadTS: "3.1", UserID: "U-ann"},
	}
	posts := make(chan denyPost, 16)
	srv := denyServer(t, items, posts, http.StatusInternalServerError)
	defer srv.Close()

	// A failed reply must neither end the subscription nor count as
	// delivered: the member's item after it still has to arrive.
	runUntilOutputContains(t, srv.URL, MentionFilter{UserIDs: []string{"U-ann"}, DenyMessage: "no"}, `"3.1"`)

	if len(posts) != 2 {
		t.Errorf("post attempts = %d, want 2 (the second mention retries the failed reply)", len(posts))
	}
}
