package adapter

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func TestDeniedMentionRepliesOnceAcrossServiceRestart(t *testing.T) {
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1700000000.000003"}`))
	}))
	defer server.Close()
	dataDir := t.TempDir()
	event := &slackevents.AppMentionEvent{User: "U2", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001"}
	for i := 0; i < 2; i++ {
		store, err := NewDenialStore(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		bot := &BotAdapter{bot: Bot{ChannelID: "C1", AllowedUserIDs: []string{"U1"}, DeniedUserMessage: "Only U1 may use this bot."}, botUserID: "UBOT", api: slack.New("test", slack.OptionAPIURL(server.URL+"/")), denials: store, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		bot.routeMention(event)
	}
	if posts != 1 {
		t.Fatalf("denial posts = %d, want 1", posts)
	}
}

func TestCorruptDenialRecordsFailStartup(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "denials.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDenialStore(dataDir); err == nil {
		t.Fatal("corrupt denial records accepted")
	}
}
