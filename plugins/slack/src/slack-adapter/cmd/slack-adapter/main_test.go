package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResourceObserveAndRemovedCommands(t *testing.T) {
	var output, errors bytes.Buffer
	if err := run([]string{"resource", "observe", "--resource", "slack://T/U"}, strings.NewReader(""), &output, &errors); err != nil || output.String() != "{}\n" {
		t.Fatalf("output = %q, err = %v", output.String(), err)
	}
	for _, args := range [][]string{{"subscribe", "unbound-mentions"}, {"resource", "subscribe", "--resource", "slack://T/U"}} {
		if err := run(args, strings.NewReader(""), &output, &errors); err == nil {
			t.Fatalf("removed command %v accepted", args)
		}
	}
}

func TestServeRequiresConfigAsFirstProtocolMessage(t *testing.T) {
	var output, errors bytes.Buffer
	if err := run([]string{"serve"}, strings.NewReader("{\"op\":\"subscriptions\"}\n"), &output, &errors); err == nil || !strings.Contains(err.Error(), "first service message") {
		t.Fatalf("error = %v", err)
	}
}
