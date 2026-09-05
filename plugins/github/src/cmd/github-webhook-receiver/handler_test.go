package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/plugins/github/src/internal/pullquery"
)

const testSecret = "s3cr3t"

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func pullRequestPayload(fullName, htmlURL, state string, draft bool, labels ...string) []byte {
	var labelObjs bytes.Buffer
	for i, l := range labels {
		if i > 0 {
			labelObjs.WriteByte(',')
		}
		labelObjs.WriteString(`{"name":"` + l + `"}`)
	}
	draftStr := "false"
	if draft {
		draftStr = "true"
	}
	return []byte(`{"action":"labeled","repository":{"full_name":"` + fullName + `"},"pull_request":{"html_url":"` + htmlURL + `","state":"` + state + `","draft":` + draftStr + `,"labels":[` + labelObjs.String() + `]}}`)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func post(t *testing.T, h http.HandlerFunc, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func TestHandler_ValidSignedMatchingDeliveryEmitsOneItem(t *testing.T) {
	var out bytes.Buffer
	in := pullquery.Inputs{Repositories: []string{"acme/widgets"}, Labels: []string{"agent-review"}, State: "open", Draft: false}
	h := newHandler(testSecret, in, &out, discardLogger())

	body := pullRequestPayload("acme/widgets", "https://github.com/acme/widgets/pull/9", "open", false, "agent-review")
	rec := post(t, h, body, map[string]string{
		"X-Hub-Signature-256": sign(body),
		"X-GitHub-Event":      "pull_request",
	})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stream lines = %v, want exactly one item", lines)
	}
	if !strings.Contains(lines[0], `"https://github.com/acme/widgets/pull/9"`) {
		t.Errorf("stream line = %q, want it to carry the matched resource", lines[0])
	}
}

func TestHandler_InvalidSignatureEmitsNoItem(t *testing.T) {
	var out bytes.Buffer
	in := pullquery.Inputs{Repositories: []string{"acme/widgets"}, State: "open", Draft: false}
	h := newHandler(testSecret, in, &out, discardLogger())

	body := pullRequestPayload("acme/widgets", "https://github.com/acme/widgets/pull/9", "open", false)
	rec := post(t, h, body, map[string]string{
		"X-Hub-Signature-256": "sha256=" + strings.Repeat("0", 64),
		"X-GitHub-Event":      "pull_request",
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if out.Len() != 0 {
		t.Fatalf("stream = %q, want nothing written for an unverified delivery", out.String())
	}
}

func TestHandler_MissingSignatureEmitsNoItem(t *testing.T) {
	var out bytes.Buffer
	in := pullquery.Inputs{Repositories: []string{"acme/widgets"}, State: "open"}
	h := newHandler(testSecret, in, &out, discardLogger())

	body := pullRequestPayload("acme/widgets", "https://github.com/acme/widgets/pull/9", "open", false)
	rec := post(t, h, body, map[string]string{"X-GitHub-Event": "pull_request"})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if out.Len() != 0 {
		t.Fatal("want nothing written for a delivery with no signature header at all")
	}
}

func TestHandler_VerifiedNonMatchingDeliveryEmitsNoItem(t *testing.T) {
	var out bytes.Buffer
	in := pullquery.Inputs{Repositories: []string{"acme/widgets"}, State: "open", Draft: false}
	h := newHandler(testSecret, in, &out, discardLogger())

	// Verified, but a draft pull request the query excludes.
	body := pullRequestPayload("acme/widgets", "https://github.com/acme/widgets/pull/9", "open", true)
	rec := post(t, h, body, map[string]string{
		"X-Hub-Signature-256": sign(body),
		"X-GitHub-Event":      "pull_request",
	})

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if out.Len() != 0 {
		t.Fatal("want nothing written for a verified delivery that does not match the query")
	}
}

func TestHandler_NonPullRequestEventIsIgnored(t *testing.T) {
	var out bytes.Buffer
	in := pullquery.Inputs{Repositories: []string{"acme/widgets"}, State: "open"}
	h := newHandler(testSecret, in, &out, discardLogger())

	body := []byte(`{"zen":"non-blocking is better than blocking."}`)
	rec := post(t, h, body, map[string]string{
		"X-Hub-Signature-256": sign(body),
		"X-GitHub-Event":      "ping",
	})

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if out.Len() != 0 {
		t.Fatal("want nothing written for a non-pull_request event")
	}
}

func TestHandler_MultipleMatchingDeliveriesEmitOneItemEach(t *testing.T) {
	var out bytes.Buffer
	in := pullquery.Inputs{Repositories: []string{"acme/widgets"}, State: "open"}
	h := newHandler(testSecret, in, &out, discardLogger())

	for _, number := range []string{"1", "2", "3"} {
		body := pullRequestPayload("acme/widgets", "https://github.com/acme/widgets/pull/"+number, "open", false)
		rec := post(t, h, body, map[string]string{
			"X-Hub-Signature-256": sign(body),
			"X-GitHub-Event":      "pull_request",
		})
		if rec.Code != http.StatusAccepted {
			t.Fatalf("pull #%s: status = %d", number, rec.Code)
		}
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("stream lines = %v, want one line per matching appearance", lines)
	}
}

func TestHandler_RejectsNonPostMethod(t *testing.T) {
	var out bytes.Buffer
	h := newHandler(testSecret, pullquery.Inputs{State: "open"}, &out, discardLogger())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
