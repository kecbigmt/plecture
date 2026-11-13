package adapter

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEachConfiguredBotOwnsASocketModeClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		token := r.Form.Get("token")
		if token == "" {
			token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		w.Header().Set("Content-Type", "application/json")
		if token == "bot-one" {
			w.Write([]byte(`{"ok":true,"team_id":"T1","user_id":"U1"}`))
		} else {
			w.Write([]byte(`{"ok":true,"team_id":"T2","user_id":"U2"}`))
		}
	}))
	defer server.Close()
	prior := slackAPIURL
	slackAPIURL = server.URL + "/"
	t.Cleanup(func() { slackAPIURL = prior })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := NewBotAdapter(Bot{AppToken: "app-one", BotToken: "bot-one", ChannelID: "C1"}, nil, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewBotAdapter(Bot{AppToken: "app-two", BotToken: "bot-two", ChannelID: "C2"}, nil, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	if first.socket == nil || second.socket == nil || first.socket == second.socket || first.botUserID == second.botUserID {
		t.Fatalf("bot identities = %s, %s", first.botUserID, second.botUserID)
	}
}
