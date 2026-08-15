package ratebudget

import (
	"testing"
	"time"
)

func TestParseHTTPResponse_CRLFHeaders(t *testing.T) {
	raw := "HTTP/1.1 200 OK\r\nEtag: \"v1\"\r\nX-Ratelimit-Reset: 123\r\n\r\n{\"ok\":true}"
	resp, ok := ParseHTTPResponse([]byte(raw))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if resp.Status != 200 {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	if resp.Headers["etag"] != `"v1"` {
		t.Errorf("etag = %q", resp.Headers["etag"])
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Errorf("body = %q", resp.Body)
	}
}

func TestParseHTTPResponse_MixedLFStatusLine(t *testing.T) {
	// The leading status line is bare-LF while the rest of the header block
	// is CRLF-terminated — the exact shape observed live against
	// api.github.com via `gh api -i`.
	raw := "HTTP/2.0 304 Not Modified\nEtag: \"v1\"\r\n\r\n"
	resp, ok := ParseHTTPResponse([]byte(raw))
	if !ok {
		t.Fatal("expected ok=true")
	}
	if resp.Status != 304 {
		t.Errorf("status = %d, want 304", resp.Status)
	}
	if len(resp.Body) != 0 {
		t.Errorf("body = %q, want empty", resp.Body)
	}
}

func TestParseHTTPResponse_NotAnHTTPResponse(t *testing.T) {
	if _, ok := ParseHTTPResponse([]byte("not an http response")); ok {
		t.Error("expected ok=false for non-HTTP output")
	}
	if _, ok := ParseHTTPResponse(nil); ok {
		t.Error("expected ok=false for empty output")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	if got := RetryAfterSeconds(""); got != 0 {
		t.Errorf("empty = %v, want 0", got)
	}
	if got := RetryAfterSeconds("not-a-number"); got != 0 {
		t.Errorf("garbage = %v, want 0", got)
	}
	if got := RetryAfterSeconds("120"); got != 2*time.Minute {
		t.Errorf("120 = %v, want 2m", got)
	}
}

func TestRateLimitReset(t *testing.T) {
	if got := RateLimitReset(""); !got.IsZero() {
		t.Errorf("empty = %v, want zero", got)
	}
	want := time.Unix(1700000000, 0)
	if got := RateLimitReset("1700000000"); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
