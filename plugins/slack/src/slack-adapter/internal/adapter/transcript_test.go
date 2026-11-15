package adapter

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type transcriptFetcher struct{ names map[string]string }

func (f transcriptFetcher) fetchThreadReplies(string, string) ([]slack.Message, error) {
	return nil, nil
}
func (f transcriptFetcher) userDisplayName(user string) string { return f.names[user] }

func TestDeliberationTranscriptOrdersAndSelectsMessages(t *testing.T) {
	root := "1700000000.000001"
	mention := "1700000000.000004"
	replies := []slack.Message{
		{Msg: slack.Msg{Timestamp: "1700000000.000005", Text: "later", User: "U1"}},
		{Msg: slack.Msg{Timestamp: "1700000000.000003", Text: "reply", User: "U2"}},
		{Msg: slack.Msg{Timestamp: root, Text: "root", User: "U1"}},
		{Msg: slack.Msg{Timestamp: "1700000000.000002", Text: "hidden", User: "U1", Hidden: true}},
	}
	body, through, omitted := buildDeliberationTranscript(transcriptFetcher{map[string]string{"U1": "Root", "U2": "Reply", "U3": "Mention"}}, replies, slackevents.AppMentionEvent{User: "U3", Text: "request", TimeStamp: mention}, root)
	fullBody := body
	if through != mention || omitted != 0 {
		t.Fatalf("through=%q omitted=%d", through, omitted)
	}
	for _, part := range []string{"Root: root", "Reply: reply", "Mention: request"} {
		at := strings.Index(body, part)
		if at < 0 {
			t.Fatalf("missing %q in %q", part, body)
		}
		body = body[at+len(part):]
	}
	if strings.Contains(fullBody, "later") || strings.Contains(fullBody, "hidden") {
		t.Fatalf("unexpected reply in %q", fullBody)
	}
}

func TestTranscriptOmitsOldRepliesAndRetainsRootAndMention(t *testing.T) {
	root := "1700000000.000001"
	replies := []slack.Message{{Msg: slack.Msg{Timestamp: root, Text: "root", User: "U1"}}}
	for i := 2; i <= 301; i++ {
		replies = append(replies, slack.Message{Msg: slack.Msg{Timestamp: fmt.Sprintf("1700000000.%06d", i), Text: fmt.Sprintf("reply-%03d ", i) + strings.Repeat("x", 300), User: "U2"}})
	}
	body, _, omitted := buildDeliberationTranscript(transcriptFetcher{}, replies, slackevents.AppMentionEvent{User: "U3", Text: "mention", TimeStamp: "1700000000.000302"}, root)
	if omitted == 0 || len(body) > maxTranscriptBytes || !strings.Contains(body, "U1: root") || !strings.Contains(body, "U3: mention") || !strings.Contains(body, omissionMarker(omitted)) || strings.Contains(body, "reply-002") || !strings.Contains(body, "reply-301") {
		t.Fatalf("omitted=%d len=%d body edges=%q", omitted, len(body), body[:min(len(body), 100)])
	}
}

func TestTranscriptSharesBudgetAndCutsOnRuneBoundary(t *testing.T) {
	// The Japanese hiragana test data uses three bytes per rune.
	for _, tc := range []struct{ name, root, mention string }{
		{"root", "root " + strings.Repeat("あ", 45000), "mention"},
		{"mention", "root", "mention " + strings.Repeat("あ", 45000)},
		{"both", "root " + strings.Repeat("あ", 30000), "mention " + strings.Repeat("あ", 15000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := "1700000000.000001"
			body, _, _ := buildDeliberationTranscript(transcriptFetcher{}, []slack.Message{{Msg: slack.Msg{Timestamp: root, Text: tc.root, User: "U1"}}}, slackevents.AppMentionEvent{User: "U2", Text: tc.mention, TimeStamp: "1700000000.000002"}, root)
			if len(body) > maxTranscriptBytes || !utf8.ValidString(body) || !strings.Contains(body, "root") || !strings.Contains(body, "mention") || !strings.Contains(body, truncationMarker) {
				t.Fatalf("len=%d valid=%v", len(body), utf8.ValidString(body))
			}
		})
	}
	if got := truncateLine("aあb", 16); got != "aあb" {
		t.Fatalf("unneeded truncation = %q", got)
	}
	if got := truncateLine("aあb"+strings.Repeat("x", 30), len(truncationMarker)+3); got != "a"+truncationMarker {
		t.Fatalf("rune cut = %q", got)
	}
}
