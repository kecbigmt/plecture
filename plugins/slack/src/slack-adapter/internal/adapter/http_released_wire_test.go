package adapter

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The request the released slack_subscribe declaration sends, and the
// subscribers listing and delete its alive probe and cleanup use, must still
// be accepted by the adapter as it is now.
func TestReleasedSubscribeWireShapesAreAccepted(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "testdata", "compat", "*", "subscribe_request.json"))
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("a released subscribe request fixture is required: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(filepath.Base(filepath.Dir(fixture)), func(t *testing.T) {
			raw, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			socketPath := "/ignored"
			body := strings.ReplaceAll(string(raw), "SOCKET_PATH", socketPath)
			var sent struct {
				ThreadTS string `json:"thread_ts"`
			}
			if err := json.Unmarshal([]byte(body), &sent); err != nil {
				t.Fatal(err)
			}
			a := newTestAdapter(&Config{ChannelID: "C0"})

			w := httptest.NewRecorder()
			a.HandleSubscribe(w, httptest.NewRequest(http.MethodPost, "/subscribe", bytes.NewBufferString(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("subscribe status %d, body=%s", w.Code, w.Body.String())
			}

			w = httptest.NewRecorder()
			a.HandleSubscribers(w, httptest.NewRequest(http.MethodGet, "/subscribers", nil))
			var listed []struct {
				ThreadTS string `json:"thread_ts"`
			}
			if err := json.NewDecoder(w.Body).Decode(&listed); err != nil {
				t.Fatalf("decode subscribers: %v", err)
			}
			if len(listed) != 1 || listed[0].ThreadTS != sent.ThreadTS {
				t.Fatalf("subscribers = %+v, want the released request's thread_ts %q", listed, sent.ThreadTS)
			}

			w = httptest.NewRecorder()
			a.HandleSubscribe(w, httptest.NewRequest(http.MethodDelete, "/subscribe?thread_ts="+sent.ThreadTS, nil))
			if w.Code != http.StatusNoContent {
				t.Fatalf("unsubscribe status %d", w.Code)
			}
			if _, ok := a.broker.Find("acme/widget-1", sent.ThreadTS); ok {
				t.Fatal("the subscription should be gone after the released unsubscribe")
			}
		})
	}
}
