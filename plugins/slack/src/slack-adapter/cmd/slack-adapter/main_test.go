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

func TestParseServerFlags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantConfig string
		wantState  string
		wantErr    bool
	}{
		{name: "no flags", args: nil},
		{name: "both flags", args: []string{"--config", "/etc/a.toml", "--state-dir", "/var/a"}, wantConfig: "/etc/a.toml", wantState: "/var/a"},
		{name: "unknown flag", args: []string{"--listen", ":1"}, wantErr: true},
		{name: "positional argument", args: []string{"serve"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errOut bytes.Buffer
			got, err := parseServerFlags(tt.args, &errOut)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && (got.configPath != tt.wantConfig || got.stateDir != tt.wantState) {
				t.Fatalf("flags = %+v, want config %q state %q", got, tt.wantConfig, tt.wantState)
			}
		})
	}
}

func mentionEndpoint(t *testing.T, seen *[]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost {
			var body struct {
				BotUserID string `json:"bot_user_id"`
			}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			*seen = append(*seen, "subscribe "+body.BotUserID)
			return
		}
		*seen = append(*seen, "unsubscribe "+req.URL.Query().Get("bot_user_id"))
	}))
	t.Cleanup(server.Close)
	return server
}

func writeRegistryEntry(t *testing.T, stateHome, team, bot, addr string) {
	t.Helper()
	dir := filepath.Join(stateHome, "slack-adapter", "instances")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]string{"team_id": team, "bot_user_id": bot, "listen_addr": addr})
	if err := os.WriteFile(filepath.Join(dir, team+"-"+bot+".json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunResourceCommandRoutesEachMentionsResourceToItsOwnAdapter(t *testing.T) {
	var seenA, seenB, seenDefault []string
	serverA := mentionEndpoint(t, &seenA)
	serverB := mentionEndpoint(t, &seenB)
	serverDefault := mentionEndpoint(t, &seenDefault)
	home, stateHome := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SLACK_ADAPTER_CONFIG", "")
	t.Setenv("SLACK_ADAPTER_STATE_DIR", "")
	t.Setenv("SLACK_ADAPTER_LISTEN_ADDR", serverDefault.Listener.Addr().String())
	writeRegistryEntry(t, stateHome, "T1", "UA", serverA.Listener.Addr().String())
	writeRegistryEntry(t, stateHome, "T1", "UB", serverB.Listener.Addr().String())

	for _, resource := range []string{"slack://T1/UA/mentions", "slack://T1/UB/mentions", "slack://T1/UC/mentions"} {
		for _, action := range []string{"subscribe", "unsubscribe"} {
			var out, errOut bytes.Buffer
			if code := runResourceCommand([]string{action, "--resource", resource, "--session", "one"}, &out, &errOut); code != 0 {
				t.Fatalf("%s %s exit = %d: %s", action, resource, code, errOut.String())
			}
		}
	}

	for _, tc := range []struct {
		name string
		got  []string
		bot  string
	}{{"first adapter", seenA, "UA"}, {"second adapter", seenB, "UB"}, {"unregistered bot falls back to the configured address", seenDefault, "UC"}} {
		if want := []string{"subscribe " + tc.bot, "unsubscribe " + tc.bot}; !reflect.DeepEqual(tc.got, want) {
			t.Errorf("%s saw %v, want %v", tc.name, tc.got, want)
		}
	}
}
