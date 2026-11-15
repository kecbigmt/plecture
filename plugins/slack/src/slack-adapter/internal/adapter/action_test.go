package adapter

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestReplyClearsWorkingStatus(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		switch r.URL.Path {
		case "/agents.sessions.setStatus":
			calls = append(calls, "status:"+r.PostForm.Get("status"))
			if r.PostForm.Get("thread_ts") != "T1" {
				t.Errorf("thread_ts = %q", r.PostForm.Get("thread_ts"))
			}
		case "/chat.postMessage":
			calls = append(calls, "post:"+r.PostForm.Get("text"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Write([]byte(`{"ok":true,"channel":"C1","ts":"T2"}`))
	}))
	defer server.Close()
	action, err := NewAction([]Bot{{ChannelID: "C1", BotToken: "token"}}, "C1", server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.SetStatus("T1", "working"); err != nil {
		t.Fatal(err)
	}
	if err := action.PostReply("T1", "complete reply", &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"status:processing", "post:complete reply", "status:"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestEmptyStatusClearsAtTurnEnd(t *testing.T) {
	var statuses []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		statuses = append(statuses, r.PostForm.Get("status"))
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	action, err := NewAction([]Bot{{ChannelID: "C1", BotToken: "token"}}, "C1", server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.SetStatus("T1", "working"); err != nil {
		t.Fatal(err)
	}
	if err := action.SetStatus("T1", ""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(statuses, []string{"processing", ""}) {
		t.Fatalf("statuses = %v", statuses)
	}
}

func TestFailedReplyDoesNotClearWorkingStatus(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/chat.postMessage" {
			w.Write([]byte(`{"ok":false,"error":"channel_not_found"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	action, err := NewAction([]Bot{{ChannelID: "C1", BotToken: "token"}}, "C1", server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.PostReply("T1", "reply", &bytes.Buffer{}); err == nil {
		t.Fatal("failed post accepted")
	}
	if !reflect.DeepEqual(paths, []string{"/chat.postMessage"}) {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSuccessfulReplyDoesNotRetryWhenStatusClearFails(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/agents.sessions.setStatus" {
			w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"channel":"C1","ts":"T2"}`))
	}))
	defer server.Close()
	action, err := NewAction([]Bot{{ChannelID: "C1", BotToken: "token"}}, "C1", server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	var errors bytes.Buffer
	if err := action.PostReply("T1", "reply", &errors); err != nil {
		t.Fatalf("successful post reported retryable failure: %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"/chat.postMessage", "/agents.sessions.setStatus"}) {
		t.Fatalf("paths = %v", paths)
	}
	if !strings.Contains(errors.String(), "missing_scope") {
		t.Fatalf("status clear failure absent from stderr: %q", errors.String())
	}
}

func TestOrdinaryMessageDoesNotClearWorkingStatus(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Write([]byte(`{"ok":true,"channel":"C1","ts":"T2"}`))
	}))
	defer server.Close()
	action, err := NewAction([]Bot{{ChannelID: "C1", BotToken: "token"}}, "C1", server.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if err := action.PostMessage("T1", "other event"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"/chat.postMessage"}) {
		t.Fatalf("paths = %v", paths)
	}
}
