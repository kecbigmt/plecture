package adapter

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// fakeAuthTestServer answers auth.test with the bot identity its bearer
// token names, standing in for two Slack Apps installed in one workspace.
func fakeAuthTestServer(t *testing.T, botsByToken map[string]string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/auth.test" {
			http.NotFound(w, req)
			return
		}
		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			_ = req.ParseForm()
			token = req.Form.Get("token")
		}
		bot, ok := botsByToken[token]
		if !ok {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "url": "https://acme.slack.com/", "team_id": "T1", "user_id": bot})
	}))
	t.Cleanup(server.Close)
	previous := slackAPIURL
	slackAPIURL = server.URL + "/"
	t.Cleanup(func() { slackAPIURL = previous })
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestResolveStateDirPrecedence(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name      string
		flagValue string
		env       string
		stateHome string
		want      string
	}{
		{name: "home fallback", want: filepath.Join(home, ".local", "state", "slack-adapter")},
		{name: "XDG_STATE_HOME", stateHome: "/xdg", want: filepath.Join("/xdg", "slack-adapter")},
		{name: "env names the instance dir", stateHome: "/xdg", env: "/env-state", want: "/env-state"},
		{name: "flag wins over env", stateHome: "/xdg", env: "/env-state", flagValue: "/flag-state", want: "/flag-state"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", tt.stateHome)
			t.Setenv("SLACK_ADAPTER_STATE_DIR", tt.env)

			if got := ResolveStateDir(tt.flagValue); got != tt.want {
				t.Errorf("ResolveStateDir(%q) = %q, want %q", tt.flagValue, got, tt.want)
			}
		})
	}
}

func TestAdapterWithoutFlagsWritesTheReleasedStatePaths(t *testing.T) {
	fakeAuthTestServer(t, map[string]string{"xoxb-a": "UA"})
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SLACK_ADAPTER_STATE_DIR", "")

	a, err := New(&Config{SlackBotToken: "xoxb-a"}, ResolveStateDir(""), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.broker.Subscribe(Subscriber{SessionName: "one", ThreadTS: mentionSubscriptionKey})

	if _, err := os.Stat(filepath.Join(stateHome, "slack-adapter", "subscribers.json")); err != nil {
		t.Fatalf("subscribers.json not at the released path: %v", err)
	}
	if got, want := a.streamManager.statePath, filepath.Join(stateHome, "slack-adapter", "streams.json"); got != want {
		t.Fatalf("stream state path = %q, want %q", got, want)
	}
}

type mentionInstance struct {
	adapter   *Adapter
	published *recordingEventPublisher
	stateDir  string
}

func startMentionInstance(t *testing.T, token, session string) mentionInstance {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	a, err := New(&Config{SlackBotToken: token}, stateDir, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	published := &recordingEventPublisher{}
	a.eventPublisher = published
	a.threadFetcher = &fakeThreadFetcher{messages: []slack.Message{slackMessage("1700000000.000001", "U-root", "root")}}
	server := httptest.NewServer(http.HandlerFunc(a.HandleSubscribe))
	t.Cleanup(server.Close)
	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "subscribe", session, "slack://T1/"+a.botUserID+"/mentions"); err != nil {
		t.Fatalf("subscribe %s: %v", session, err)
	}
	return mentionInstance{adapter: a, published: published, stateDir: stateDir}
}

func TestTwoInstancesDeliverOnlyTheirOwnBotsMentionsAndKeepSeparateState(t *testing.T) {
	fakeAuthTestServer(t, map[string]string{"xoxb-a": "UA", "xoxb-b": "UB"})
	first := startMentionInstance(t, "xoxb-a", "owner-a/_orchestrator")
	second := startMentionInstance(t, "xoxb-b", "owner-b/_orchestrator")

	// Both Apps sit in the same channel, so each receives the message event
	// for a mention of either bot; only its own bot's mention may count.
	for _, inst := range []mentionInstance{first, second} {
		for _, mention := range []struct{ text, ts string }{{"<@UA> for a", "1700000000.000002"}, {"<@UB> for b", "1700000000.000003"}} {
			inst.adapter.handleMessage(&slackevents.MessageEvent{User: "U-person", Text: mention.text, TimeStamp: mention.ts, ThreadTimeStamp: "1700000000.000001", Channel: "C1"})
		}
	}

	for _, tc := range []struct {
		inst    mentionInstance
		session string
		text    string
	}{
		{first, "owner-a/_orchestrator", "for a"},
		{second, "owner-b/_orchestrator", "for b"},
	} {
		events := tc.inst.published.events
		if len(events) != 1 || events[0].Type != "user.emit" || events[0].SessionName != tc.session || !strings.Contains(events[0].Body, tc.text) {
			t.Fatalf("%s events = %+v, want exactly one user.emit carrying %q", tc.session, events, tc.text)
		}
	}

	firstLedger := readFile(t, filepath.Join(first.stateDir, "subscribers.json"))
	secondLedger := readFile(t, filepath.Join(second.stateDir, "subscribers.json"))
	if !strings.Contains(firstLedger, "owner-a/") || strings.Contains(firstLedger, "owner-b/") {
		t.Fatalf("first ledger = %s", firstLedger)
	}
	if !strings.Contains(secondLedger, "owner-b/") || strings.Contains(secondLedger, "owner-a/") {
		t.Fatalf("second ledger = %s", secondLedger)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMentionSubscriptionForAnotherBotIsRejected(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID, a.botUserID = "T1", "UA"
	server := httptest.NewServer(http.HandlerFunc(a.HandleSubscribe))
	defer server.Close()
	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "subscribe", "one", "slack://T1/UA/mentions"); err != nil {
		t.Fatal(err)
	}

	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "subscribe", "two", "slack://T1/UB/mentions"); err == nil {
		t.Fatal("an adapter accepted a mention subscription for another App's bot")
	}
	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "unsubscribe", "one", "slack://T1/UB/mentions"); err == nil {
		t.Fatal("an adapter removed a mention subscription addressed to another App's bot")
	}
	if got := a.broker.ListMentions(); len(got) != 1 || got[0].SessionName != "one" {
		t.Fatalf("mention subscriptions = %+v, want only the one for this adapter's bot", got)
	}
}

func TestMentionSubscriptionIsRejectedWhileTheBotIdentityIsUnknown(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T1"
	server := httptest.NewServer(http.HandlerFunc(a.HandleSubscribe))
	defer server.Close()

	if err := MentionSubscription(context.Background(), server.Client(), server.URL, "subscribe", "one", "slack://T1/UA/mentions"); err == nil {
		t.Fatal("an adapter that never learned its bot user id accepted a mention subscription")
	}
}

func TestInstanceRegistryRoutesEachBotToItsOwnAdapter(t *testing.T) {
	fakeAuthTestServer(t, map[string]string{"xoxb-a": "UA", "xoxb-b": "UB"})
	registry := t.TempDir()
	for token, addr := range map[string]string{"xoxb-a": "127.0.0.1:7890", "xoxb-b": "127.0.0.1:7891"} {
		a, err := New(&Config{SlackBotToken: token}, "", quietLogger())
		if err != nil {
			t.Fatal(err)
		}
		if err := a.RegisterInstance(registry, addr); err != nil {
			t.Fatal(err)
		}
		a.Close()
	}

	tests := []struct {
		bot  string
		want string
	}{
		{"UA", "http://127.0.0.1:7890"},
		{"UB", "http://127.0.0.1:7891"},
		{"UC", "http://127.0.0.1:9999"},
	}
	for _, tt := range tests {
		if got := MentionBaseURL(registry, "T1", tt.bot, "127.0.0.1:9999"); got != tt.want {
			t.Errorf("MentionBaseURL(%s) = %q, want %q", tt.bot, got, tt.want)
		}
	}
}

func TestRegisterInstanceFailsWithoutABotIdentity(t *testing.T) {
	a := newTestAdapter(&Config{})
	a.teamID = "T1"

	if err := a.RegisterInstance(t.TempDir(), "127.0.0.1:7890"); err == nil {
		t.Fatal("an adapter without a bot user id registered itself")
	}
}
