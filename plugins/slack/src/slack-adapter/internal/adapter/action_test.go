package adapter

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestActionUsesTheBotForTheSelectedChannel(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		credential := r.Header.Get("Authorization")
		if credential == "" {
			credential = r.Form.Get("token")
		}
		methods = append(methods, r.URL.Path+":"+credential)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/chat.postMessage":
			w.Write([]byte(`{"ok":true,"channel":"C2","ts":"1700000000.000001"}`))
		case "/chat.getPermalink":
			w.Write([]byte(`{"ok":true,"permalink":"https://example.slack.com/archives/C2/p1700000000000001"}`))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer server.Close()
	bots := []Bot{{ChannelID: "C1", BotToken: "one"}, {ChannelID: "C2", BotToken: "two"}}
	action, err := NewAction(bots, "C2", server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	thread, link, err := action.CreateThread("hello")
	if err != nil || thread != "1700000000.000001" || !strings.Contains(link, "/C2/") {
		t.Fatalf("thread = %q, link = %q, err = %v", thread, link, err)
	}
	if err := action.PostMessage(thread, "reply"); err != nil {
		t.Fatal(err)
	}
	if err := action.SetStatus(thread, "processing"); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 4 {
		t.Fatalf("methods = %v", methods)
	}
	for _, method := range methods {
		if !strings.HasSuffix(method, ":Bearer two") && !strings.HasSuffix(method, ":two") {
			t.Fatalf("wrong bot: %v", methods)
		}
	}
	if _, err := NewAction(bots, "C3", server.URL+"/"); err == nil {
		t.Fatal("unknown channel accepted")
	}
}
