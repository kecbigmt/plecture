package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunSubscribeCommandRejectsUnknownSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runSubscribeCommand([]string{"bound-mentions"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunSubscribeCommandRequiresBaseURL(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runSubscribeCommand([]string{"unbound-mentions", "--channel-ids", `["C1"]`}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "--base-url") {
		t.Errorf("stderr = %q, want a mention of --base-url", errOut.String())
	}
}

func TestRunSubscribeCommandRejectsMalformedChannelIDs(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runSubscribeCommand([]string{"unbound-mentions", "--base-url", "http://127.0.0.1:7890", "--channel-ids", "not-json"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "--channel-ids") {
		t.Errorf("stderr = %q, want a mention of --channel-ids", errOut.String())
	}
}

func TestRunSubscribeCommandRejectsMalformedUserIDs(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runSubscribeCommand([]string{"unbound-mentions", "--base-url", "http://127.0.0.1:7890", "--user-ids", "not-json"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "--user-ids") {
		t.Errorf("stderr = %q, want a mention of --user-ids", errOut.String())
	}
}

func TestRunSubscribeCommandReturnsNonZeroWhenTheResidentAdapterIsUnreachable(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runSubscribeCommand([]string{"unbound-mentions", "--base-url", "http://127.0.0.1:1"}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestRunSubscribeCommandStreamsUntilDisconnected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Writing nothing and returning ends the stream immediately, the
		// same shape a resident-adapter restart produces mid-connection.
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := runSubscribeCommand([]string{"unbound-mentions", "--base-url", srv.URL}, &out, &errOut)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (a supervisor restart signal) for an unexpected disconnect", code)
	}
}

func TestRunResourceCommandRejectsUnknownSubcommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runResourceCommand([]string{"observe-state"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunResourceCommandRequiresResource(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runResourceCommand([]string{"observe"}, &out, &errOut)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "--resource") {
		t.Errorf("stderr = %q, want a mention of --resource", errOut.String())
	}
}

func TestRunResourceCommandPrintsAnEmptyObjectForAResolvedResource(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runResourceCommand([]string{"observe", "--resource", "https://acme.slack.com/archives/C01234567/p1234567890123456"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "{}" {
		t.Errorf("stdout = %q, want %q", got, "{}")
	}
}

func TestRunResourceCommandRegistersAndRemovesThread(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		methods = append(methods, req.Method)
		if req.Method == http.MethodPost {
			var body struct {
				SessionName string `json:"session_name"`
				ThreadTS    string `json:"thread_ts"`
				ChannelID   string `json:"channel_id"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.SessionName != "one" || body.ThreadTS != "1700000000.000001" || body.ChannelID != "C1" {
				t.Errorf("subscribe body = %+v", body)
			}
		} else if req.URL.Query().Get("session_name") != "one" || req.URL.Query().Get("thread_ts") != "1700000000.000001" {
			t.Errorf("delete URL = %s", req.URL)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "slack-adapter")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("listen_addr = \""+server.Listener.Addr().String()+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resource := "https://acme.slack.com/archives/C1/p1700000000000001"
	for _, action := range []string{"subscribe", "unsubscribe"} {
		var out, errOut bytes.Buffer
		if code := runResourceCommand([]string{action, "--resource", resource, "--session", "one"}, &out, &errOut); code != 0 {
			t.Fatalf("%s exit = %d: %s", action, code, errOut.String())
		}
	}
	if !reflect.DeepEqual(methods, []string{http.MethodPost, http.MethodDelete}) {
		t.Fatalf("methods = %v", methods)
	}
}
