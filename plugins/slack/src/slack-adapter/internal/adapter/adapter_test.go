package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
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
