package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionAPISendsStatus(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			t.Errorf("authorization header absent")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		switch r.URL.Path {
		case "/agents.sessions.setStatus":
			if r.PostForm.Get("channel_id") != "C1" || r.PostForm.Get("thread_ts") != "T1" || r.PostForm.Get("status") != "processing" {
				t.Errorf("setStatus form = %v", r.PostForm)
			}
			w.Write([]byte(`{"ok":true,"agent_status":"processing"}`))
		}
	}))
	defer server.Close()
	client := newSlackSessionClient("xoxb-test", server.URL+"/", server.Client())
	if err := client.SetStatus("C1", "T1", "processing"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != "/agents.sessions.setStatus" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSessionAPIRejectsHTTPFailureAndMalformedResponse(t *testing.T) {
	for _, response := range []struct {
		code int
		body string
	}{{503, "down"}, {200, "not json"}, {200, `{"ok":false,"error":"missing_scope"}`}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(response.code)
			w.Write([]byte(response.body))
		}))
		client := newSlackSessionClient("token", server.URL+"/", server.Client())
		if err := client.SetStatus("C1", "T1", "active"); err == nil {
			t.Errorf("response %q accepted", response.body)
		}
		server.Close()
	}
}

func TestSessionAPIReportsConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	client := newSlackSessionClient("token", server.URL+"/", server.Client())
	server.Close()
	if err := client.SetStatus("C1", "T1", "processing"); err == nil {
		t.Fatal("connection failure was accepted")
	}
}
