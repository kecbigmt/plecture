package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResourceObserveAndSubscribeCommand(t *testing.T) {
	var output, errors bytes.Buffer
	if err := run([]string{"resource", "observe", "--resource", "slack://T/U"}, strings.NewReader(""), &output, &errors); err != nil || output.String() != "{}\n" {
		t.Fatalf("output = %q, err = %v", output.String(), err)
	}
	if err := run([]string{"subscribe", "unbound-mentions", "--socket", "/missing/feed.sock", "--channel-ids", `["C1"]`}, strings.NewReader(""), &output, &errors); err == nil || strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("subscribe command error = %v", err)
	}
	if err := run([]string{"resource", "subscribe", "--resource", "slack://T/U"}, strings.NewReader(""), &output, &errors); err == nil {
		t.Fatal("removed resource subscribe command accepted")
	}
}

func TestServeRequiresConfigAsFirstProtocolMessage(t *testing.T) {
	var output, errors bytes.Buffer
	if err := run([]string{"serve"}, strings.NewReader("{\"op\":\"subscriptions\"}\n"), &output, &errors); err == nil || !strings.Contains(err.Error(), "first service message") {
		t.Fatalf("error = %v", err)
	}
}

func TestReplyCommandDispatchesToAction(t *testing.T) {
	var output, errors bytes.Buffer
	err := run([]string{"reply", "post", "--event-type", "plect.message"}, strings.NewReader(""), &output, &errors)
	if err == nil || !strings.Contains(err.Error(), "--config and --channel-id are required") {
		t.Fatalf("reply command error = %v", err)
	}
}

func TestReplyCommandIgnoresMessageDelta(t *testing.T) {
	var output, errors bytes.Buffer
	err := run([]string{"reply", "post", "--event-type", "plect.message_delta"}, strings.NewReader(""), &output, &errors)
	if err != nil {
		t.Fatalf("delta delivery returned an error: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("delta delivery wrote stdout: %q", output.String())
	}
	if got, want := errors.String(), "plect.message_delta should be removed from include\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestReplyCommandRejectsUnknownOrMissingEventType(t *testing.T) {
	for _, args := range [][]string{
		{"reply", "post"},
		{"reply", "post", "--event-type", "user.emit"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output, errors bytes.Buffer
			err := run(args, strings.NewReader(""), &output, &errors)
			if err == nil || err.Error() != "reply post requires --event-type plect.message or plect.message_delta" {
				t.Fatalf("reply command error = %v", err)
			}
		})
	}
}
