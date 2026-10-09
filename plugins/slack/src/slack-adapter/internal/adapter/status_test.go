package adapter

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack"
)

func TestWriteStatusError_SlackAPIErrorMapsTo422(t *testing.T) {
	w := httptest.NewRecorder()
	writeStatusError(testLogger(), w, slack.SlackErrorResponse{Err: "invalid_arguments"})
	if w.Code != 422 {
		t.Fatalf("status = %d, want 422", w.Code)
	}
	if body := w.Body.String(); !strings.Contains(body, `"error":"invalid_arguments"`) {
		t.Errorf("body = %q, want Slack error code", body)
	}
}

func TestWriteStatusError_OtherErrorStays500(t *testing.T) {
	w := httptest.NewRecorder()
	writeStatusError(testLogger(), w, errors.New("dial tcp: connection refused"))
	if w.Code != 500 {
		t.Fatalf("status = %d, want 500", w.Code)
	}
}
