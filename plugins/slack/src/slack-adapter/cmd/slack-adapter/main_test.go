package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
