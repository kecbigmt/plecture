package adapter

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

func testRoutingBot(p *routePublisher) *BotAdapter {
	return &BotAdapter{bot: Bot{ChannelID: "C1", AllowedUserIDs: []string{"U1"}}, teamID: "T1", botUserID: "UBOT", publisher: p, fetcher: routeFetcher{}, linker: routePermalink{}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func testMention() *slackevents.AppMentionEvent {
	return &slackevents.AppMentionEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: "<@UBOT> hello"}
}

func TestMessageEventMentionFilterAndAppMentionDedup(t *testing.T) {
	for _, tc := range []struct {
		name, text, botID, subtype string
		want                       int
	}{
		{"mention", "hello <@UBOT>", "", "", 1},
		{"other bot", "hello <@OTHER>", "", "", 0},
		{"bot message", "hello <@UBOT>", "B1", "", 0},
		{"subtype", "hello <@UBOT>", "", "message_changed", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &routePublisher{counts: []int{1}}
			b := testRoutingBot(p)
			b.handleEventsAPI(slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: &slackevents.MessageEvent{User: "U1", Channel: "C1", TimeStamp: "1700000000.000002", ThreadTimeStamp: "1700000000.000001", Text: tc.text, BotID: tc.botID, SubType: tc.subtype}}})
			if len(p.calls) != tc.want {
				t.Fatalf("publish calls = %v", p.calls)
			}
			if tc.want == 1 {
				b.handleEventsAPI(slackevents.EventsAPIEvent{Type: slackevents.CallbackEvent, InnerEvent: slackevents.EventsAPIInnerEvent{Data: testMention()}})
				if len(p.calls) != 1 {
					t.Fatalf("duplicate published: %v", p.calls)
				}
			}
		})
	}
}

type failingPermalink struct{}

func (failingPermalink) permalink(string, string) (string, error) {
	return "", errors.New("link failed")
}

type failingFetcher struct{}

func (failingFetcher) fetchThreadReplies(string, string) ([]slack.Message, error) {
	return nil, errors.New("fetch failed")
}
func (failingFetcher) userDisplayName(string) string { return "" }

func TestMentionFailuresReleaseClaimForRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		breakBot func(*BotAdapter)
		log      string
	}{
		{"permalink", func(b *BotAdapter) { b.linker = failingPermalink{} }, "link failed"},
		{"transcript", func(b *BotAdapter) { b.fetcher = failingFetcher{} }, "fetch failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &routePublisher{counts: []int{1}}
			b := testRoutingBot(p)
			var logs strings.Builder
			b.logger = slog.New(slog.NewTextHandler(&logs, nil))
			tc.breakBot(b)
			b.routeMention(testMention())
			if len(p.calls) != 0 || !strings.Contains(logs.String(), tc.log) {
				t.Fatalf("calls=%v log=%s", p.calls, logs.String())
			}
			b.linker, b.fetcher = routePermalink{}, routeFetcher{}
			b.routeMention(testMention())
			if len(p.calls) != 1 {
				t.Fatalf("retry calls = %v", p.calls)
			}
		})
	}
}

func TestWrongChannelDenialPostsOnceAndRetriesFailedPost(t *testing.T) {
	posts := 0
	failFirst := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.URL.Path != "/chat.postMessage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("channel") != "C2" || r.Form.Get("thread_ts") != "1700000000.000001" || r.Form.Get("text") != "Wrong channel" {
			t.Errorf("post form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		if failFirst {
			failFirst = false
			w.Write([]byte(`{"ok":false,"error":"service_unavailable"}`))
			return
		}
		w.Write([]byte(`{"ok":true,"channel":"C2","ts":"1700000000.000003"}`))
	}))
	defer server.Close()
	store, err := NewDenialStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &routePublisher{}
	b := testRoutingBot(p)
	b.api = slack.New("test", slack.OptionAPIURL(server.URL+"/"))
	b.denials = store
	b.bot.DeniedChannelMessage = "Wrong channel"
	ev := testMention()
	ev.Channel = "C2"
	for i := 0; i < 3; i++ {
		b.routeMention(ev)
	}
	if posts != 2 || len(p.calls) != 0 {
		t.Fatalf("posts=%d publish=%v", posts, p.calls)
	}
}
