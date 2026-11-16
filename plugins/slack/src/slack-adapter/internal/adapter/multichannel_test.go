package adapter

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack/slackevents"
)

type channelPermalink struct{}

func (channelPermalink) permalink(channel, _ string) (string, error) {
	return "https://example.slack.com/archives/" + channel + "/p1700000000000001", nil
}

func TestOneAppAcceptsMentionsAndPostsInSeveralChannels(t *testing.T) {
	t.Setenv("APP_TOKEN", "xapp-test")
	t.Setenv("BOT_TOKEN", "xoxb-test")
	config, err := ParseServiceConfig([]byte(`{"bots":[{"app_token_env":"APP_TOKEN","bot_token_env":"BOT_TOKEN","channel_ids":["C1","C2"],"allowed_user_ids":["U1"]}]}`))
	bots := config.Bots
	if err != nil || len(bots) != 1 {
		t.Fatalf("bots = %+v, err = %v", bots, err)
	}
	for _, channel := range []string{"C1", "C2"} {
		t.Run(channel, func(t *testing.T) {
			publisher := &routePublisher{counts: []int{1}}
			bot := &BotAdapter{bot: bots[0], teamID: "T1", botUserID: "UBOT", publisher: publisher, fetcher: routeFetcher{}, linker: channelPermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			bot.routeMention(&slackevents.AppMentionEvent{User: "U1", Channel: channel, TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> hello"})
			if len(publisher.calls) != 1 || !strings.Contains(publisher.calls[0], "/archives/"+channel+"/") {
				t.Fatalf("published = %v", publisher.calls)
			}
		})
	}
	posted := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		posted[r.Form.Get("channel")] = true
		io.WriteString(w, `{"ok":true,"channel":"`+r.Form.Get("channel")+`","ts":"1700000000.000003"}`)
	}))
	defer server.Close()
	for _, channel := range []string{"C1", "C2"} {
		action, err := NewAction(bots, channel, server.URL+"/")
		if err != nil {
			t.Fatal(err)
		}
		if err := action.PostMessage("1700000000.000001", "reply"); err != nil {
			t.Fatal(err)
		}
	}
	if !posted["C1"] || !posted["C2"] {
		t.Fatalf("posted = %v", posted)
	}
}

func TestMultiChannelDeclarationRejectsAmbiguityAndOldKey(t *testing.T) {
	t.Setenv("APP_ONE", "xapp-one")
	t.Setenv("BOT_ONE", "xoxb-one")
	t.Setenv("APP_TWO", "xapp-two")
	t.Setenv("BOT_TWO", "xoxb-two")
	base := `{"bots":[{"app_token_env":"APP_ONE","bot_token_env":"BOT_ONE","channel_ids":["C1","C2"],"allowed_user_ids":[]},{"app_token_env":"APP_TWO","bot_token_env":"BOT_TWO","channel_ids":["C3"],"allowed_user_ids":[]}]}`
	for _, tc := range []struct{ name, input, want string }{
		{"overlap", strings.Replace(base, `["C3"]`, `["C2"]`, 1), "duplicate channel"},
		{"duplicate within App", strings.Replace(base, `["C1","C2"]`, `["C1","C1"]`, 1), "duplicate channel"},
		{"duplicate App token", strings.Replace(base, `"app_token_env":"APP_TWO"`, `"app_token_env":"APP_ONE"`, 1), "duplicate App"},
		{"empty channels", strings.Replace(base, `["C1","C2"]`, `[]`, 1), "channel_ids"},
		{"old key", strings.Replace(base, `"channel_ids":["C1","C2"]`, `"channel_id":"C1"`, 1), "channel_id"},
		{"relative socket", strings.Replace(base, `"bots":`, `"socket_path":"feed.sock","bots":`, 1), "absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseServiceConfig([]byte(tc.input)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
